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
	template.HasResourceProperties(jsii.String("AWS::Route53::HostedZone"), map[string]any{
		"Name": "erickaldama.com.",
	})

	// CAA record restricting issuance to Amazon (ACM) — foundation hardening.
	template.ResourceCountIs(jsii.String("AWS::Route53::RecordSet"), jsii.Number(1))
	template.HasResourceProperties(jsii.String("AWS::Route53::RecordSet"), map[string]any{
		"Type": "CAA",
	})
}

func TestReadonlyManagedPolicy(t *testing.T) {
	template := synth(t)

	template.HasResourceProperties(jsii.String("AWS::IAM::ManagedPolicy"), map[string]any{
		"ManagedPolicyName": "mail-readonly-managed",
		"Users":             []any{"mail-readonly"},
	})
	// Importing a user must NOT emit an AWS::IAM::User.
	template.ResourceCountIs(jsii.String("AWS::IAM::User"), jsii.Number(0))
	// Hard-deny statement present.
	template.HasResourceProperties(jsii.String("AWS::IAM::ManagedPolicy"), map[string]any{
		"PolicyDocument": map[string]any{
			"Statement": assertions.Match_ArrayWith(&[]any{
				assertions.Match_ObjectLike(&map[string]any{
					"Sid":    "HardDenyMutationReconAndCredentialMinting",
					"Effect": "Deny",
				}),
			}),
		},
	})
	// The regional-reads statement must include the observability reads the agent uses to
	// self-verify SP-2's event path (SNS topic + EventBridge rule). These were added when the
	// SP-2 deploy revealed mail-readonly could not read SNS/events to confirm bounce/complaint
	// routing. Pinned here so a future refactor cannot silently blind the verifier.
	template.HasResourceProperties(jsii.String("AWS::IAM::ManagedPolicy"), map[string]any{
		"PolicyDocument": map[string]any{
			"Statement": assertions.Match_ArrayWith(&[]any{
				assertions.Match_ObjectLike(&map[string]any{
					"Sid":    "AllowRegionalReadsUsEast1",
					"Effect": "Allow",
					"Action": assertions.Match_ArrayWith(&[]any{
						"sns:GetTopicAttributes",
						"events:DescribeRule",
						"events:ListTargetsByRule",
					}),
				}),
			}),
		},
	})
	// SP-3: the agent must read the receive pipeline it deploys (dynamodb/lambda/sqs).
	// SES receipt reads are already covered by ses:Describe*. Pinned so a refactor can't re-blind the verifier.
	template.HasResourceProperties(jsii.String("AWS::IAM::ManagedPolicy"), map[string]any{
		"PolicyDocument": map[string]any{
			"Statement": assertions.Match_ArrayWith(&[]any{
				assertions.Match_ObjectLike(&map[string]any{
					"Sid":    "AllowRegionalReadsUsEast1",
					"Effect": "Allow",
					"Action": assertions.Match_ArrayWith(&[]any{
						"dynamodb:DescribeTable",
						"lambda:GetFunction",
						"sqs:GetQueueAttributes",
					}),
				}),
			}),
		},
	})
}

func TestPermissionsBoundaryNotInStack(t *testing.T) {
	template := synth(t)

	// The stack owns exactly ONE managed policy: the readonly one.
	// The permissions boundary (erickaldama-boundary) is a BOOTSTRAP artifact — it must
	// pre-exist for `cdk bootstrap --custom-permissions-boundary`, so CFN does NOT own it
	// (owning it caused a 409 AlreadyExists on first deploy). It lives only in
	// iam/erickaldama-boundary.json, managed out-of-band like the exec-policy.
	template.ResourceCountIs(jsii.String("AWS::IAM::ManagedPolicy"), jsii.Number(1))
	template.HasResourceProperties(jsii.String("AWS::IAM::ManagedPolicy"), map[string]any{
		"ManagedPolicyName": "mail-readonly-managed",
	})
}

func TestStackTags(t *testing.T) {
	template := synth(t)
	// The hosted zone carries the project tags (Tags are applied stack-wide).
	template.HasResourceProperties(jsii.String("AWS::Route53::HostedZone"), map[string]any{
		"HostedZoneTags": assertions.Match_ArrayWith(&[]any{
			assertions.Match_ObjectLike(&map[string]any{"Key": "Project", "Value": "erickaldama-mail"}),
		}),
	})
}
