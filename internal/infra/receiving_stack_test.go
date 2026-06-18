package infra

import (
	"testing"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/assertions"
	"github.com/aws/jsii-runtime-go"
)

func synthReceiving(t *testing.T) assertions.Template {
	t.Helper()
	app := awscdk.NewApp(nil)
	_, bucket := NewMailStorageStack(app, "MailStorageStack", &awscdk.StackProps{})
	stack := NewReceivingStack(app, "ReceivingStack", &awscdk.StackProps{}, bucket)
	return assertions.Template_FromStack(stack, nil)
}

func TestMailIndexTable(t *testing.T) {
	template := synthReceiving(t)

	template.ResourceCountIs(jsii.String("AWS::DynamoDB::Table"), jsii.Number(1))
	template.HasResourceProperties(jsii.String("AWS::DynamoDB::Table"), map[string]any{
		"TableName":   "mail-index",
		"BillingMode": "PAY_PER_REQUEST",
		"KeySchema": assertions.Match_ArrayWith(&[]any{
			map[string]any{"AttributeName": "PK", "KeyType": "HASH"},
			map[string]any{"AttributeName": "SK", "KeyType": "RANGE"},
		}),
	})
}
