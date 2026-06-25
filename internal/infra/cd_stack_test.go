package infra

import (
	"testing"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/assertions"
	"github.com/aws/jsii-runtime-go"
)

func synthCd(t *testing.T) assertions.Template {
	t.Helper()
	app := awscdk.NewApp(nil)
	stack := NewCdStack(app, "CdStack", &awscdk.StackProps{
		Env: &awscdk.Environment{Account: jsii.String(Account), Region: jsii.String(Region)},
	})
	return assertions.Template_FromStack(stack, nil)
}

func TestCdStackOidcProviderIsNative(t *testing.T) {
	tpl := synthCd(t)
	// L1 CfnOIDCProvider → native resource, NOT a custom resource + Lambda.
	tpl.ResourceCountIs(jsii.String("AWS::IAM::OIDCProvider"), jsii.Number(1))
	tpl.ResourceCountIs(jsii.String("AWS::Lambda::Function"), jsii.Number(0)) // L2 would add one
	tpl.HasResourceProperties(jsii.String("AWS::IAM::OIDCProvider"), map[string]any{
		"Url":          OidcProviderUrl,
		"ClientIdList": []any{OidcAudience},
	})
}

func TestCdStackHasExactlyTwoRoles(t *testing.T) {
	tpl := synthCd(t)
	// L1 → exactly 2 roles (diff + deploy); L2 would add a 3rd (custom-resource provider role).
	tpl.ResourceCountIs(jsii.String("AWS::IAM::Role"), jsii.Number(2))
}

func TestCdDeployRoleTrustIsScopedToProductionEnv(t *testing.T) {
	tpl := synthCd(t)
	tpl.HasResourceProperties(jsii.String("AWS::IAM::Role"), assertions.Match_ObjectLike(&map[string]any{
		"RoleName": DeployRoleName,
		"AssumeRolePolicyDocument": assertions.Match_ObjectLike(&map[string]any{
			"Statement": []any{
				assertions.Match_ObjectLike(&map[string]any{
					"Action": "sts:AssumeRoleWithWebIdentity",
					"Condition": map[string]any{
						"StringEquals": map[string]any{
							"token.actions.githubusercontent.com:aud": OidcAudience,
							"token.actions.githubusercontent.com:sub": "repo:" + GithubRepo + ":environment:production",
						},
					},
				}),
			},
		}),
	}))
}

func TestCdDiffRoleTrustIsScopedToPullRequest(t *testing.T) {
	tpl := synthCd(t)
	tpl.HasResourceProperties(jsii.String("AWS::IAM::Role"), assertions.Match_ObjectLike(&map[string]any{
		"RoleName": DiffRoleName,
		"AssumeRolePolicyDocument": assertions.Match_ObjectLike(&map[string]any{
			"Statement": []any{
				assertions.Match_ObjectLike(&map[string]any{
					"Condition": map[string]any{
						"StringEquals": map[string]any{"token.actions.githubusercontent.com:aud": OidcAudience},
						"StringLike":   map[string]any{"token.actions.githubusercontent.com:sub": "repo:" + GithubRepo + ":pull_request"},
					},
				}),
			},
		}),
	}))
}

func TestCdRolesHaveBoundary(t *testing.T) {
	tpl := synthCd(t)
	// PermissionsBoundary in the synthesized template is NOT a plain string — CDK emits a
	// Fn::Join that resolves to the full ARN. We assert the exact boundary name appears in the
	// join array so the test fails if anyone swaps or removes the boundary policy.
	// Shape verified by synth: {"Fn::Join":["",["arn:",{"Ref":"AWS::Partition"},":iam::<acct>:policy/<name>"]]}
	boundaryArn := ":iam::" + Account + ":policy/" + BoundaryManagedPolicyName
	tpl.AllResourcesProperties(jsii.String("AWS::IAM::Role"), assertions.Match_ObjectLike(&map[string]any{
		"PermissionsBoundary": map[string]any{
			"Fn::Join": []any{
				"",
				assertions.Match_ArrayWith(&[]interface{}{boundaryArn}),
			},
		},
	}))
}

// TEST-1 (menor privilegio — el corazón de la separación read/write). El AddToPolicy materializa un
// AWS::IAM::Policy separado. Shape VERIFICADO por synth real (2026-06-24): el diff role produce
// Resource como STRING único (solo el lookup); el deploy role produce Resource como ARRAY de los 4.
func TestCdDiffRoleCanOnlyAssumeLookupRole(t *testing.T) {
	tpl := synthCd(t)
	// El diff role NO puede asumir los roles de deploy/publishing — solo el lookup (read-only).
	tpl.HasResourceProperties(jsii.String("AWS::IAM::Policy"), assertions.Match_ObjectLike(&map[string]any{
		"PolicyDocument": assertions.Match_ObjectLike(&map[string]any{
			"Statement": []any{
				assertions.Match_ObjectLike(&map[string]any{
					"Action":   "sts:AssumeRole",
					"Resource": CdkLookupRoleArn, // string exacto — NO un array, NO los 4
				}),
			},
		}),
	}))
}

func TestCdDeployRoleAssumesTheFourBootstrapRoles(t *testing.T) {
	tpl := synthCd(t)
	// El deploy role asume exactamente los 4 roles cdk-* (array).
	tpl.HasResourceProperties(jsii.String("AWS::IAM::Policy"), assertions.Match_ObjectLike(&map[string]any{
		"PolicyDocument": assertions.Match_ObjectLike(&map[string]any{
			"Statement": []any{
				assertions.Match_ObjectLike(&map[string]any{
					"Action": "sts:AssumeRole",
					"Resource": []any{
						CdkDeployRoleArn, CdkFilePublishingRoleArn, CdkImagePublishingRoleArn, CdkLookupRoleArn,
					},
				}),
			},
		}),
	}))
}
