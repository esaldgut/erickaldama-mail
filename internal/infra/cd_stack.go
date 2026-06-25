package infra

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// NewCdStack creates the GitHub Actions OIDC provider + two scoped deploy roles. Uses the L1 CfnOIDCProvider
// (native AWS::IAM::OIDCProvider) — the L2 NewOpenIdConnectProvider synthesizes a custom resource + Lambda + a
// 3rd role without boundary (audit A1). The trust is built with NewFederatedPrincipal(AttrArn,...) because the
// L1 exposes AttrArn(), not IOIDCProviderRef.
func NewCdStack(scope constructs.Construct, id string, props *awscdk.StackProps) awscdk.Stack {
	stack := awscdk.NewStack(scope, jsii.String(id), props)

	// Native OIDC provider (L1). ThumbprintList omitted → IAM autocompletes via trusted CA.
	oidc := awsiam.NewCfnOIDCProvider(stack, jsii.String("GithubOidc"), &awsiam.CfnOIDCProviderProps{
		Url:          jsii.String(OidcProviderUrl),
		ClientIdList: jsii.Strings(OidcAudience),
	})

	boundary := awsiam.ManagedPolicy_FromManagedPolicyName(stack, jsii.String("Boundary"),
		jsii.String(BoundaryManagedPolicyName))

	// diff role — read-only, any PR (sub scoped to pull_request).
	diffConditions := map[string]interface{}{
		"StringEquals": map[string]interface{}{
			"token.actions.githubusercontent.com:aud": OidcAudience,
		},
		"StringLike": map[string]interface{}{
			"token.actions.githubusercontent.com:sub": "repo:" + GithubRepo + ":pull_request",
		},
	}
	diffRole := awsiam.NewRole(stack, jsii.String("DiffRole"), &awsiam.RoleProps{
		RoleName: jsii.String(DiffRoleName),
		AssumedBy: awsiam.NewFederatedPrincipal(oidc.AttrArn(), &diffConditions,
			jsii.String("sts:AssumeRoleWithWebIdentity")),
		PermissionsBoundary: boundary,
	})
	diffRole.AddToPolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Effect:    awsiam.Effect_ALLOW,
		Actions:   jsii.Strings("sts:AssumeRole"),
		Resources: jsii.Strings(CdkLookupRoleArn),
	}))

	// deploy role — only assumable from environment:production (StringEquals).
	deployConditions := map[string]interface{}{
		"StringEquals": map[string]interface{}{
			"token.actions.githubusercontent.com:aud": OidcAudience,
			"token.actions.githubusercontent.com:sub": "repo:" + GithubRepo + ":environment:production",
		},
	}
	deployRole := awsiam.NewRole(stack, jsii.String("DeployRole"), &awsiam.RoleProps{
		RoleName: jsii.String(DeployRoleName),
		AssumedBy: awsiam.NewFederatedPrincipal(oidc.AttrArn(), &deployConditions,
			jsii.String("sts:AssumeRoleWithWebIdentity")),
		PermissionsBoundary: boundary,
	})
	deployRole.AddToPolicy(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Effect:  awsiam.Effect_ALLOW,
		Actions: jsii.Strings("sts:AssumeRole"),
		Resources: jsii.Strings(CdkDeployRoleArn, CdkFilePublishingRoleArn,
			CdkImagePublishingRoleArn, CdkLookupRoleArn),
	}))

	// PRAC-1: tags — paridad con los 4 stacks existentes ("every resource is attributable").
	for k, v := range cdTags() {
		awscdk.Tags_Of(stack).Add(jsii.String(k), v, nil)
	}

	return stack
}

// cdTags mirrors sp2Tags but marks Subproject=CD.
func cdTags() map[string]*string {
	return map[string]*string{
		"Project":    jsii.String("erickaldama-mail"),
		"Subproject": jsii.String("CD"),
		"ManagedBy":  jsii.String("CDK-Go"),
	}
}
