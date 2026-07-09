package infra

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// NewMailStorageStack owns the raw inbound-mail bucket (SP-3). It deploys before ReceivingStack and
// hands the bucket to it as a Go prop (Approach A: cross-stack object reference). SSE-S3 (not SES
// message-encryption, which is Java/Ruby client-side only) so a Go reader can GetObject transparently.
func NewMailStorageStack(scope constructs.Construct, id string, props *awscdk.StackProps) (awscdk.Stack, awss3.Bucket) {
	stack := awscdk.NewStack(scope, jsii.String(id), props)

	bucket := awss3.NewBucket(stack, jsii.String("RawMailBucket"), &awss3.BucketProps{
		BucketName:        jsii.String(RawBucketName),
		Encryption:        awss3.BucketEncryption_S3_MANAGED,
		BlockPublicAccess: awss3.BlockPublicAccess_BLOCK_ALL(),
		EnforceSSL:        jsii.Bool(true),
		RemovalPolicy:     awscdk.RemovalPolicy_RETAIN,
		LifecycleRules: &[]*awss3.LifecycleRule{
			{
				Transitions: &[]*awss3.Transition{
					{
						StorageClass:    awss3.StorageClass_INFREQUENT_ACCESS(),
						TransitionAfter: awscdk.Duration_Days(jsii.Number(90)),
					},
				},
			},
		},
	})

	for k, v := range sp3Tags() {
		awscdk.Tags_Of(stack).Add(jsii.String(k), v, nil)
	}

	return stack, bucket
}

// sp3Tags labels every SP-3 resource for attribution.
func sp3Tags() map[string]*string {
	return map[string]*string{
		"Project":    strptr("erickaldama-mail"),
		"Subproject": strptr("SP-3"),
		"ManagedBy":  strptr("CDK-Go"),
	}
}
