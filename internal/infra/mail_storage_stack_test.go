package infra

import (
	"testing"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/assertions"
	"github.com/aws/jsii-runtime-go"
)

func synthStorage(t *testing.T) assertions.Template {
	t.Helper()
	app := awscdk.NewApp(nil)
	stack, _ := NewMailStorageStack(app, "MailStorageStack", &awscdk.StackProps{})
	return assertions.Template_FromStack(stack, nil)
}

func TestRawBucket(t *testing.T) {
	template := synthStorage(t)

	template.ResourceCountIs(jsii.String("AWS::S3::Bucket"), jsii.Number(1))
	template.HasResourceProperties(jsii.String("AWS::S3::Bucket"), map[string]any{
		"BucketName": "erickaldama-mail-raw",
		"BucketEncryption": map[string]any{
			"ServerSideEncryptionConfiguration": assertions.Match_ArrayWith(&[]any{
				assertions.Match_ObjectLike(&map[string]any{
					"ServerSideEncryptionByDefault": map[string]any{"SSEAlgorithm": "AES256"},
				}),
			}),
		},
		"PublicAccessBlockConfiguration": map[string]any{
			"BlockPublicAcls":       true,
			"BlockPublicPolicy":     true,
			"IgnorePublicAcls":      true,
			"RestrictPublicBuckets": true,
		},
	})
}
