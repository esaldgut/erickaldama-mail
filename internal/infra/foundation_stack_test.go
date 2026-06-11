package infra

import (
	"testing"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/assertions"
	"github.com/aws/jsii-runtime-go"
)

// synth builds the stack and returns the assertion Template. No AWS calls.
func synth(t *testing.T) assertions.Template {
	t.Helper()
	app := awscdk.NewApp(nil)
	stack := NewFoundationStack(app, "TestStack", nil)
	return assertions.Template_FromStack(stack, nil)
}

func TestHostedZone(t *testing.T) {
	template := synth(t)

	// Exactly one public hosted zone, named with the trailing dot Route53 adds.
	template.ResourceCountIs(jsii.String("AWS::Route53::HostedZone"), jsii.Number(1))
	template.HasResourceProperties(jsii.String("AWS::Route53::HostedZone"), map[string]interface{}{
		"Name": "erickaldama.com.",
	})

	// CAA record restricting issuance to Amazon (ACM) — foundation hardening.
	template.ResourceCountIs(jsii.String("AWS::Route53::RecordSet"), jsii.Number(1))
	template.HasResourceProperties(jsii.String("AWS::Route53::RecordSet"), map[string]interface{}{
		"Type": "CAA",
	})
}

func TestReadonlyManagedPolicy(t *testing.T) {
	template := synth(t)

	template.HasResourceProperties(jsii.String("AWS::IAM::ManagedPolicy"), map[string]interface{}{
		"ManagedPolicyName": "mail-readonly-managed",
		"Users":             []interface{}{"mail-readonly"},
	})
	// Importing a user must NOT emit an AWS::IAM::User.
	template.ResourceCountIs(jsii.String("AWS::IAM::User"), jsii.Number(0))
	// Hard-deny statement present.
	template.HasResourceProperties(jsii.String("AWS::IAM::ManagedPolicy"), map[string]interface{}{
		"PolicyDocument": map[string]interface{}{
			"Statement": assertions.Match_ArrayWith(&[]interface{}{
				assertions.Match_ObjectLike(&map[string]interface{}{
					"Sid":    "HardDenyMutationReconAndCredentialMinting",
					"Effect": "Deny",
				}),
			}),
		},
	})
}

func TestPermissionsBoundary(t *testing.T) {
	template := synth(t)

	// Now there are exactly 2 managed policies: readonly + boundary.
	template.ResourceCountIs(jsii.String("AWS::IAM::ManagedPolicy"), jsii.Number(2))
	template.HasResourceProperties(jsii.String("AWS::IAM::ManagedPolicy"), map[string]interface{}{
		"ManagedPolicyName": "erickaldama-boundary",
		"PolicyDocument": map[string]interface{}{
			"Statement": assertions.Match_ArrayWith(&[]interface{}{
				// Allows the project services. ssm:* is required so the boundary (which CFN
				// stamps onto the cfn-exec-role) does not block the CDK bootstrap version check
				// (ssm:GetParameters on /cdk-bootstrap/*) — found during the first real deploy.
				assertions.Match_ObjectLike(&map[string]interface{}{
					"Effect": "Allow",
					"Action": assertions.Match_ArrayWith(&[]interface{}{"ssm:*"}),
				}),
				// Denies the escalation/out-of-scope services.
				assertions.Match_ObjectLike(&map[string]interface{}{
					"Effect": "Deny",
					"Action": assertions.Match_ArrayWith(&[]interface{}{
						"route53domains:*", "ec2:*", "rds:*", "organizations:*",
						"iam:PutUserPermissionsBoundary", "iam:PutRolePermissionsBoundary",
						"iam:DeleteUserPermissionsBoundary", "iam:DeleteRolePermissionsBoundary",
					}),
				}),
			}),
		},
	})
}

func TestStackTags(t *testing.T) {
	template := synth(t)
	// The hosted zone carries the project tags (Tags are applied stack-wide).
	template.HasResourceProperties(jsii.String("AWS::Route53::HostedZone"), map[string]interface{}{
		"HostedZoneTags": assertions.Match_ArrayWith(&[]interface{}{
			assertions.Match_ObjectLike(&map[string]interface{}{"Key": "Project", "Value": "erickaldama-mail"}),
		}),
	})
}
