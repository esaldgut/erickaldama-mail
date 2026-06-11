// Package infra defines the AWS CDK Go stacks for the erickaldama.com email project.
package infra

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsroute53"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// NewFoundationStack creates the SP-1 foundation: public hosted zone (+CAA) and the
// read-only IAM managed policy attached to the imported mail-readonly user. The permissions
// boundary is a bootstrap artifact (out-of-band), not a stack resource — see note below.
func NewFoundationStack(scope constructs.Construct, id string, props *awscdk.StackProps) awscdk.Stack {
	stack := awscdk.NewStack(scope, jsii.String(id), props)

	// Tag every resource in the stack for attribution.
	for k, v := range projectTags() {
		awscdk.Tags_Of(stack).Add(jsii.String(k), v, nil)
	}

	// Public hosted zone for erickaldama.com. Route53 auto-creates NS+SOA.
	// CaaAmazon adds a CAA record restricting cert issuance to Amazon (ACM) —
	// safe because the stack is 100% AWS (ACM + SES).
	zone := awsroute53.NewPublicHostedZone(stack, jsii.String("ErickaldamaZone"),
		&awsroute53.PublicHostedZoneProps{
			ZoneName:  jsii.String(DomainName),
			CaaAmazon: jsii.Bool(true),
			Comment:   jsii.String("erickaldama.com email project foundation (SP-1)"),
		})

	// Export the 4 name servers (a list token — join, do NOT dereference in Go)
	// so the human can update the registrar via route53domains:UpdateDomainNameservers.
	awscdk.NewCfnOutput(stack, jsii.String("NameServers"), &awscdk.CfnOutputProps{
		Value:       awscdk.Fn_Join(jsii.String(","), zone.HostedZoneNameServers()),
		Description: jsii.String("Set these 4 NS at the registrar (route53domains update-domain-nameservers)"),
	})
	awscdk.NewCfnOutput(stack, jsii.String("HostedZoneId"), &awscdk.CfnOutputProps{
		Value: zone.HostedZoneId(),
	})

	// Import the existing mail-readonly user by name (reference, NOT a CFN resource).
	readonlyUser := awsiam.User_FromUserName(stack, jsii.String("MailReadonlyUser"),
		jsii.String(ReadonlyUserName))

	// The 4-statement boundary (mirror of iam/readonly-policy.json), attached via the
	// Users prop (NOT AddManagedPolicy — that throws on imported users).
	awsiam.NewManagedPolicy(stack, jsii.String("MailReadonlyManagedPolicy"),
		&awsiam.ManagedPolicyProps{
			ManagedPolicyName: jsii.String(ReadonlyManagedPolicyName),
			Users:             &[]awsiam.IUser{readonlyUser},
			Statements:        readonlyStatements(),
		})

	// NOTE: the permissions boundary `erickaldama-boundary` is intentionally NOT a stack
	// resource. It must pre-exist before `cdk bootstrap --custom-permissions-boundary`, so it
	// is a BOOTSTRAP artifact managed out-of-band (iam/erickaldama-boundary.json), not owned by
	// CFN. Having the stack create it caused a 409 AlreadyExists on the first deploy.

	return stack
}

// readonlyStatements mirrors iam/readonly-policy.json (verified vs Service Authorization Reference).
func readonlyStatements() *[]awsiam.PolicyStatement {
	usEast1 := &map[string]interface{}{
		"StringEquals": map[string]interface{}{"aws:RequestedRegion": "us-east-1"},
	}
	return &[]awsiam.PolicyStatement{
		awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
			Sid:       jsii.String("AllowGlobalReadsUnconditioned"),
			Effect:    awsiam.Effect_ALLOW,
			Actions:   jsii.Strings("sts:GetCallerIdentity", "route53:ListHostedZones", "route53:GetHostedZone", "route53:ListResourceRecordSets"),
			Resources: jsii.Strings("*"),
		}),
		awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
			Sid:        jsii.String("AllowRegionalReadsUsEast1"),
			Effect:     awsiam.Effect_ALLOW,
			Actions:    jsii.Strings("ses:Get*", "ses:List*", "ses:Describe*", "cloudformation:Describe*", "cloudformation:List*", "cloudwatch:DescribeAlarms", "cloudwatch:ListMetrics", "cloudwatch:GetMetricData", "cloudwatch:GetMetricStatistics"),
			Resources:  jsii.Strings("*"),
			Conditions: usEast1,
		}),
		awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
			Sid:        jsii.String("AllowS3BucketLevelScopedUsEast1"),
			Effect:     awsiam.Effect_ALLOW,
			Actions:    jsii.Strings("s3:ListBucket", "s3:GetBucketLocation", "s3:GetBucketPolicy", "s3:GetBucketPublicAccessBlock", "s3:GetEncryptionConfiguration"),
			Resources:  jsii.Strings("arn:aws:s3:::*erickaldama*"),
			Conditions: usEast1,
		}),
		awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
			Sid:       jsii.String("HardDenyMutationReconAndCredentialMinting"),
			Effect:    awsiam.Effect_DENY,
			Actions:   jsii.Strings("ses:Send*", "sts:AssumeRole", "sts:AssumeRoleWithWebIdentity", "sts:AssumeRoleWithSAML", "sts:GetSessionToken", "sts:GetFederationToken", "s3:GetObject", "cloudformation:GetTemplate", "cloudformation:GetTemplateSummary", "ses:GetIdentityPolicies", "ses:GetEmailIdentityPolicies", "iam:*"),
			Resources: jsii.Strings("*"),
		}),
	}
}
