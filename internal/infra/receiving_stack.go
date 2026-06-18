package infra

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsdynamodb"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// NewReceivingStack builds the SP-3 inbound pipeline on top of the imported raw bucket (Approach A).
// Fleshed out helper-by-helper in tasks 4–9. The bucket policy and rule-set activation live HERE
// (not in MailStorageStack) to avoid the bucket↔rule cross-stack dependency cycle (audit finding C1).
func NewReceivingStack(scope constructs.Construct, id string, props *awscdk.StackProps, bucket awss3.IBucket) awscdk.Stack {
	stack := awscdk.NewStack(scope, jsii.String(id), props)

	addReceiveTable(stack)

	for k, v := range sp3Tags() {
		awscdk.Tags_Of(stack).Add(jsii.String(k), v, nil)
	}

	return stack
}

// addReceiveTable creates the on-demand mail-index table. PK=mailbox#addr, SK=ts#message-id —
// lets the SP-4 TUI Query a mailbox ordered by date desc without scanning.
func addReceiveTable(stack awscdk.Stack) awsdynamodb.Table {
	return awsdynamodb.NewTable(stack, jsii.String("MailIndex"), &awsdynamodb.TableProps{
		TableName:   jsii.String(MailIndexTableName),
		BillingMode: awsdynamodb.BillingMode_PAY_PER_REQUEST,
		PartitionKey: &awsdynamodb.Attribute{
			Name: jsii.String("PK"),
			Type: awsdynamodb.AttributeType_STRING,
		},
		SortKey: &awsdynamodb.Attribute{
			Name: jsii.String("SK"),
			Type: awsdynamodb.AttributeType_STRING,
		},
		RemovalPolicy: awscdk.RemovalPolicy_RETAIN,
	})
}
