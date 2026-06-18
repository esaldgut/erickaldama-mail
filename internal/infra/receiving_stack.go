package infra

import (
	"path/filepath"
	"runtime"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsdynamodb"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambdadestinations"
	"github.com/aws/aws-cdk-go/awscdk/v2/awss3"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsses"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssesactions"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssqs"
	"github.com/aws/aws-cdk-go/awscdklambdagoalpha/v2"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// receiveLambdaEntry resolves cmd/lambda/receive to an absolute path anchored at the module root,
// independent of the process cwd. NewGoFunction resolves a relative Entry against the caller's cwd,
// which under `go test` is the package dir (internal/infra), not the module root — so we anchor on
// this source file's location (<module-root>/internal/infra/receiving_stack.go) instead.
func receiveLambdaEntry() string {
	_, thisFile, _, _ := runtime.Caller(0)
	moduleRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	return filepath.Join(moduleRoot, "cmd", "lambda", "receive")
}

// NewReceivingStack builds the SP-3 inbound pipeline on top of the imported raw bucket (Approach A).
// Fleshed out helper-by-helper in tasks 4–9. The bucket policy and rule-set activation live HERE
// (not in MailStorageStack) to avoid the bucket↔rule cross-stack dependency cycle (audit finding C1).
func NewReceivingStack(scope constructs.Construct, id string, props *awscdk.StackProps, bucket awss3.IBucket) awscdk.Stack {
	stack := awscdk.NewStack(scope, jsii.String(id), props)

	table := addReceiveTable(stack)
	fn := addReceiveLambda(stack, table)
	addReceiptRule(stack, bucket, fn)

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

// addReceiveLambda builds the Go Lambda (provided.al2023, arm64), grants it PutItem-only on the table,
// and routes async failures to an SSE-SQS DLQ via an OnFailure destination (richer than the legacy
// DeadLetterQueue prop). The handler reads MAIL_INDEX_TABLE + MAIL_DOMAIN from the environment.
func addReceiveLambda(stack awscdk.Stack, table awsdynamodb.Table) awslambda.IFunction {
	dlq := awssqs.NewQueue(stack, jsii.String("ReceiveDlq"), &awssqs.QueueProps{
		QueueName:       jsii.String(ReceiveDlqName),
		Encryption:      awssqs.QueueEncryption_SQS_MANAGED,
		RetentionPeriod: awscdk.Duration_Days(jsii.Number(14)),
	})

	fn := awscdklambdagoalpha.NewGoFunction(stack, jsii.String("ReceiveFunction"), &awscdklambdagoalpha.GoFunctionProps{
		FunctionName:  jsii.String(ReceiveFunctionName),
		Entry:         jsii.String(receiveLambdaEntry()),
		Runtime:       awslambda.Runtime_PROVIDED_AL2023(),
		Architecture:  awslambda.Architecture_ARM_64(),
		Timeout:       awscdk.Duration_Seconds(jsii.Number(30)),
		MemorySize:    jsii.Number(128),
		RetryAttempts: jsii.Number(2),
		OnFailure:     awslambdadestinations.NewSqsDestination(dlq),
		Environment: &map[string]*string{
			"MAIL_INDEX_TABLE": jsii.String(MailIndexTableName),
			"MAIL_DOMAIN":      jsii.String(DomainName),
		},
	})

	table.GrantWriteData(fn)
	return fn
}

// addReceiptRule creates the catch-all rule set (S3 action first, Lambda action second), grants SES
// PutObject on the imported bucket via a BucketPolicy created HERE (not bucket.AddToResourcePolicy,
// which would land the policy in the owning stack and cycle on the rule ARN — audit finding C1), and
// grants SES lambda:InvokeFunction (or the invoke fails silently — finding I1).
func addReceiptRule(stack awscdk.Stack, bucket awss3.IBucket, fn awslambda.IFunction) {
	ruleSet := awsses.NewReceiptRuleSet(stack, jsii.String("InboundRuleSet"), &awsses.ReceiptRuleSetProps{
		ReceiptRuleSetName: jsii.String(ReceiptRuleSetName),
	})

	ruleSet.AddRule(jsii.String("StoreAndIndex"), &awsses.ReceiptRuleOptions{
		ReceiptRuleName: jsii.String(ReceiptRuleName),
		Enabled:         jsii.Bool(true),
		ScanEnabled:     jsii.Bool(true),
		TlsPolicy:       awsses.TlsPolicy_REQUIRE,
		Actions: &[]awsses.IReceiptRuleAction{
			awssesactions.NewS3(&awssesactions.S3Props{
				Bucket:          bucket,
				ObjectKeyPrefix: jsii.String(InboundObjectPrefix),
			}),
			awssesactions.NewLambda(&awssesactions.LambdaProps{
				Function:       fn,
				InvocationType: awssesactions.LambdaInvocationType_EVENT,
			}),
		},
	})

	policy := awss3.NewBucketPolicy(stack, jsii.String("SesPutPolicy"), &awss3.BucketPolicyProps{
		Bucket: bucket,
	})
	policy.Document().AddStatements(awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
		Sid:        jsii.String("AllowSESPuts"),
		Effect:     awsiam.Effect_ALLOW,
		Principals: &[]awsiam.IPrincipal{awsiam.NewServicePrincipal(jsii.String("ses.amazonaws.com"), nil)},
		Actions:    jsii.Strings("s3:PutObject"),
		Resources:  jsii.Strings(*bucket.BucketArn() + "/*"),
		Conditions: &map[string]any{
			"StringEquals": map[string]any{
				"AWS:SourceAccount": Account,
				"AWS:SourceArn":     ReceiptRuleArn,
			},
		},
	}))

	fn.AddPermission(jsii.String("AllowSESInvoke"), &awslambda.Permission{
		Principal:     awsiam.NewServicePrincipal(jsii.String("ses.amazonaws.com"), nil),
		Action:        jsii.String("lambda:InvokeFunction"),
		SourceAccount: jsii.String(Account),
		SourceArn:     jsii.String(ReceiptRuleArn),
	})
}
