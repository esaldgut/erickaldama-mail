package infra

import (
	"fmt"
	"os"

	"github.com/aws/aws-cdk-go/awscdk/v2"
)

// awsAccount resolves the target AWS account: CDK_DEFAULT_ACCOUNT in CI/deploy (populated from the
// OIDC-assumed credentials — see the explicit export step in .github/workflows/cd.yml), falling back
// to a NON-SECRET placeholder for offline synth/test determinism. The real account never lives in source.
func awsAccount() string {
	if a := os.Getenv("CDK_DEFAULT_ACCOUNT"); a != "" {
		return a
	}
	return "000000000000" // placeholder — never the real account; keeps offline synth/test deterministic
}

// awsRegion resolves the target region. Region is not secret; this default keeps offline synth deterministic.
func awsRegion() string {
	if r := os.Getenv("CDK_DEFAULT_REGION"); r != "" {
		return r
	}
	return "us-east-1"
}

// Env carries the account/region-derived identifiers, built once by NewEnv and injected into the stacks
// via EnvStackProps. It replaces the account-bearing constants that used to live in naming.go, so the real
// account ID no longer appears as a literal in the source (NDA / attack-surface reduction). The ARN strings
// are byte-identical to the former literals when the real account/region are resolved from env.
type Env struct {
	Account, Region                                                                         string
	IdentityArn, ConfigSetArn, ReceiptRuleArn                                               string
	CdkLookupRoleArn, CdkDeployRoleArn, CdkFilePublishingRoleArn, CdkImagePublishingRoleArn string
	DynamoTableArn, BounceTopicArn                                                          string
}

// EnvStackProps wraps StackProps with the resolved Env. It is the uniform threading vehicle across all
// stacks: the 5 stack signatures are not homogeneous (ReceivingStack takes a bucket param, MailStorageStack
// returns two values), so a positional env param would not fit them all — a wrapping props type does.
type EnvStackProps struct {
	*awscdk.StackProps
	Env Env
}

// NewEnv builds the Env once from the resolved account/region. Each ARN is composed with fmt.Sprintf so it
// is byte-identical to the literal it replaces (verified by the byte-identity gate: cdk synth diff == 0).
func NewEnv() Env {
	a, r := awsAccount(), awsRegion()
	return Env{
		Account: a, Region: r,
		IdentityArn:    fmt.Sprintf("arn:aws:ses:%s:%s:identity/%s", r, a, DomainName),
		ConfigSetArn:   fmt.Sprintf("arn:aws:ses:%s:%s:configuration-set/%s", r, a, ConfigSetName),
		ReceiptRuleArn: fmt.Sprintf("arn:aws:ses:%s:%s:receipt-rule-set/%s:receipt-rule/%s", r, a, ReceiptRuleSetName, ReceiptRuleName),
		// The 4 cdk-hnb659fds-* bootstrap roles: the account appears TWICE per ARN (iam::<acct>:role/...-<acct>-<region>).
		CdkLookupRoleArn:          fmt.Sprintf("arn:aws:iam::%s:role/cdk-hnb659fds-lookup-role-%s-%s", a, a, r),
		CdkDeployRoleArn:          fmt.Sprintf("arn:aws:iam::%s:role/cdk-hnb659fds-deploy-role-%s-%s", a, a, r),
		CdkFilePublishingRoleArn:  fmt.Sprintf("arn:aws:iam::%s:role/cdk-hnb659fds-file-publishing-role-%s-%s", a, a, r),
		CdkImagePublishingRoleArn: fmt.Sprintf("arn:aws:iam::%s:role/cdk-hnb659fds-image-publishing-role-%s-%s", a, a, r),
		DynamoTableArn:            fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/%s", r, a, MailIndexTableName),
		BounceTopicArn:            fmt.Sprintf("arn:aws:sns:%s:%s:%s", r, a, BounceTopicName),
	}
}
