# SP-3 Receive Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Provision the complete inbound email pipeline for `erickaldama.com` — any `*@` address lands parsed and indexed in DynamoDB with raw MIME archived in S3 — via two CDK-Go stacks.

**Architecture:** `MailStorageStack` (S3 SSE-S3 bucket) deploys first; `ReceivingStack` receives the bucket as a Go prop (Approach A) and adds the SES v1 receipt rule set (catch-all → S3 → Go Lambda → DynamoDB + SQS DLQ), activated via a CDK custom resource, plus the apex MX, the re-pointed DMARC rua, the SNS operator subscription, and the read-only IAM extension on FoundationStack. The agent writes code and runs synth/test read-only; the human deploys out-of-band with SSO Admin.

**Tech Stack:** AWS CDK Go (awscdk v2.258.1 + `awscdklambdagoalpha/v2 v2.258.1-alpha.0`), Go 1.26.4, aws-lambda-go, aws-sdk-go-v2 (dynamodb, s3). All construct signatures below were compiled + `cdk synth`-verified by the adversarial audit (2026-06-17).

**Source of truth:** `docs/superpowers/specs/2026-06-17-sp3-receiving-design.md` + `~/.claude/plans/email-project-research/12-sp3-audit-findings.md`.

---

## File structure

- Create: `internal/infra/mail_storage_stack.go` — `NewMailStorageStack` → returns `(awscdk.Stack, awss3.Bucket)`. Owns only the raw bucket.
- Create: `internal/infra/mail_storage_stack_test.go` — template asserts (SSE-S3, BLOCK_ALL, lifecycle).
- Create: `internal/infra/receiving_stack.go` — `NewReceivingStack(scope, id, props, bucket)`. Orchestrator + helpers `addReceiveTable`, `addReceiveLambda`, `addReceiptRule`, `addBucketPolicy`, `addRuleSetActivation`, `addDmarcAndMx`, `addObservability`.
- Create: `internal/infra/receiving_stack_test.go` — template asserts for every resource.
- Create: `cmd/lambda/receive/main.go` — the Go Lambda handler.
- Create: `cmd/lambda/receive/main_test.go` — unit tests (event→item, idempotency, multi-recipient).
- Modify: `internal/infra/naming.go` — add SP-3 constants.
- Modify: `internal/infra/foundation_stack.go` — extend `mail-readonly` with dynamodb/lambda/sqs reads.
- Modify: `internal/infra/foundation_stack_test.go` — assert the new reads.
- Modify: `internal/infra/sending_stack.go` + `naming.go` — re-point `DmarcValue` rua.
- Modify: `cmd/cdk/main.go` — register both new stacks.
- Modify: `go.mod` — add `awscdklambdagoalpha/v2` + the Go Lambda runtime deps.

---

## Task 1: Add SP-3 naming constants

**Files:**
- Modify: `internal/infra/naming.go`

- [ ] **Step 1: Add the constants** to `internal/infra/naming.go`, inside the existing `const (...)` block, after the SP-2 constants:

```go
	// SP-3 — receive pipeline.
	RawBucketName       = "erickaldama-mail-raw"
	MailIndexTableName  = "mail-index"
	ReceiveFunctionName = "mail-receive"
	ReceiveLambdaRole   = "mail-receive-lambda-role"
	ReceiptRuleSetName  = "erickaldama-inbound"
	ReceiptRuleName     = "store-and-index"
	ReceiveDlqName      = "mail-receive-dlq"
	InboundObjectPrefix = "inbound/" // SES appends messageId verbatim; trailing slash required.
	InboundMxHost       = "inbound-smtp.us-east-1.amazonaws.com"
	DmarcReportsAddress = "dmarc-reports@erickaldama.com"
	OperatorEmail       = "esaldgut@gmail.com" // publishable / benign
	// ReceiptRuleArn is the ARN used to scope the SES S3 bucket policy and Lambda invoke permission.
	// Format verified against the SES docs: receipt-rule-set/<set>:receipt-rule/<rule>.
	ReceiptRuleArn = "arn:aws:ses:us-east-1:367707589526:receipt-rule-set/erickaldama-inbound:receipt-rule/store-and-index"
```

- [ ] **Step 2: Re-point the DMARC rua** — in `internal/infra/naming.go`, change the existing `DmarcValue`:

```go
	DmarcValue = "v=DMARC1; p=none; rua=mailto:dmarc-reports@erickaldama.com"
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./...`
Expected: exit 0, no output.

- [ ] **Step 4: Commit**

```bash
git add internal/infra/naming.go
git commit -m "feat(sp-3): add receive-pipeline naming constants + re-point DMARC rua"
```

---

## Task 2: Add the Go Lambda module dependencies

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the alpha CDK module + Lambda runtime deps**

Run:
```bash
go get github.com/aws/aws-cdk-go/awscdklambdagoalpha/v2@v2.258.1-alpha.0
go get github.com/aws/aws-lambda-go@latest
go get github.com/aws/aws-sdk-go-v2/config@latest
go get github.com/aws/aws-sdk-go-v2/service/dynamodb@latest
go get github.com/aws/aws-sdk-go-v2/service/s3@latest
go get github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue@latest
```
Expected: `go.mod` gains the requires; `go.sum` updated.

- [ ] **Step 2: Verify the build still resolves**

Run: `go build ./... && go mod verify`
Expected: exit 0; `all modules verified`.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "feat(sp-3): add awscdklambdagoalpha + aws-lambda-go + sdk-v2 deps"
```

---

## Task 3: MailStorageStack — the raw bucket (TDD)

**Files:**
- Create: `internal/infra/mail_storage_stack.go`
- Create: `internal/infra/mail_storage_stack_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/infra/mail_storage_stack_test.go`:

```go
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
	// SSE-S3 (AES256) — NOT SES message-encryption (Java/Ruby only; a Go reader couldn't decrypt).
	template.HasResourceProperties(jsii.String("AWS::S3::Bucket"), map[string]any{
		"BucketName": "erickaldama-mail-raw",
		"BucketEncryption": map[string]any{
			"ServerSideEncryptionConfiguration": assertions.Match_ArrayWith(&[]any{
				assertions.Match_ObjectLike(&map[string]any{
					"ServerSideEncryptionByDefault": map[string]any{"SSEAlgorithm": "AES256"},
				}),
			}),
		},
		// Block ALL public access — inbound email is sensitive.
		"PublicAccessBlockConfiguration": map[string]any{
			"BlockPublicAcls":       true,
			"BlockPublicPolicy":     true,
			"IgnorePublicAcls":      true,
			"RestrictPublicBuckets": true,
		},
	})
}
```

- [ ] **Step 2: Run the test — verify it FAILS**

Run: `go test ./internal/infra/ -run TestRawBucket`
Expected: FAIL — `undefined: NewMailStorageStack`.

- [ ] **Step 3: Implement the stack** — create `internal/infra/mail_storage_stack.go`:

```go
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
		RemovalPolicy:     awscdk.RemovalPolicy_RETAIN, // never auto-delete received mail
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
```

- [ ] **Step 4: Run the test — verify it PASSES**

Run: `go test ./internal/infra/ -run TestRawBucket`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/infra/mail_storage_stack.go internal/infra/mail_storage_stack_test.go
git commit -m "feat(sp-3): MailStorageStack — SSE-S3 raw bucket, BLOCK_ALL, IA@90d"
```

---

## Task 4: ReceivingStack skeleton + DynamoDB table (TDD)

**Files:**
- Create: `internal/infra/receiving_stack.go`
- Create: `internal/infra/receiving_stack_test.go`

- [ ] **Step 1: Write the failing test** — create `internal/infra/receiving_stack_test.go`:

```go
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
	storage, bucket := NewMailStorageStack(app, "MailStorageStack", &awscdk.StackProps{})
	_ = storage
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
```

- [ ] **Step 2: Run the test — verify it FAILS**

Run: `go test ./internal/infra/ -run TestMailIndexTable`
Expected: FAIL — `undefined: NewReceivingStack`.

- [ ] **Step 3: Implement the skeleton + table** — create `internal/infra/receiving_stack.go`:

```go
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
```

- [ ] **Step 4: Run the test — verify it PASSES**

Run: `go test ./internal/infra/ -run TestMailIndexTable`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/infra/receiving_stack.go internal/infra/receiving_stack_test.go
git commit -m "feat(sp-3): ReceivingStack skeleton + mail-index DynamoDB table (on-demand)"
```

---

## Task 5: The receive Lambda handler (Go, TDD)

**Files:**
- Create: `cmd/lambda/receive/main.go`
- Create: `cmd/lambda/receive/main_test.go`

- [ ] **Step 1: Write the failing test** — create `cmd/lambda/receive/main_test.go`:

```go
package main

import (
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

func sampleMail(recipients []string) events.SimpleEmailService {
	return events.SimpleEmailService{
		Mail: events.SimpleEmailMessage{
			MessageID: "ses-object-key-id",
			Timestamp: time.Date(2026, 6, 17, 21, 30, 0, 0, time.UTC),
			CommonHeaders: events.SimpleEmailCommonHeaders{
				From:      []string{"sender@example.com"},
				Subject:   "hi",
				MessageID: "<rfc5322-id@example.com>",
			},
		},
		Receipt: events.SimpleEmailReceipt{Recipients: recipients},
	}
}

func TestItemsForMessage_FiltersDomainRecipients(t *testing.T) {
	ses := sampleMail([]string{"erick@erickaldama.com", "outsider@gmail.com", "dmarc-reports@erickaldama.com"})
	items := itemsForMessage(ses, "erickaldama.com")

	if len(items) != 2 {
		t.Fatalf("expected 2 domain recipients, got %d", len(items))
	}
	if !strings.HasPrefix(items[0].PK, "mailbox#") {
		t.Fatalf("PK should be mailbox#<addr>, got %s", items[0].PK)
	}
	// SK = ts#<rfc3339>#<rfc5322-message-id>
	if !strings.Contains(items[0].SK, "2026-06-17T21:30:00Z") || !strings.Contains(items[0].SK, "<rfc5322-id@example.com>") {
		t.Fatalf("SK must embed Mail.Timestamp + CommonHeaders.MessageID, got %s", items[0].SK)
	}
	// S3 key uses Mail.MessageID (the SES id), NOT the RFC5322 header id.
	if items[0].S3Key != "inbound/ses-object-key-id" {
		t.Fatalf("S3Key must be inbound/<Mail.MessageID>, got %s", items[0].S3Key)
	}
}
```

- [ ] **Step 2: Run the test — verify it FAILS**

Run: `go test ./cmd/lambda/receive/ -run TestItemsForMessage`
Expected: FAIL — `undefined: itemsForMessage`.

- [ ] **Step 3: Implement the handler** — create `cmd/lambda/receive/main.go`:

```go
// Command receive is the SES inbound Lambda: SES writes the raw MIME to S3 first, then invokes this
// (Event/async) with metadata+headers only (no body). It indexes one item per domain recipient into
// DynamoDB, idempotent by the RFC 5322 Message-ID. Real failures return an error → async retries →
// OnFailure SQS destination.
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"os"
)

// mailItem is one DynamoDB row: a message as seen by one recipient mailbox.
type mailItem struct {
	PK        string `dynamodbav:"PK"`        // mailbox#<addr>
	SK        string `dynamodbav:"SK"`        // ts#<rfc3339>#<rfc5322-message-id>
	MessageID string `dynamodbav:"messageId"` // Mail.MessageID (SES id)
	S3Key     string `dynamodbav:"s3Key"`     // inbound/<Mail.MessageID>
	From      string `dynamodbav:"from"`
	Subject   string `dynamodbav:"subject"`
	Date      string `dynamodbav:"date"`
}

// itemsForMessage builds one mailItem per recipient under domain. Pure (no AWS) — unit-tested.
// Recipients come from Receipt.Recipients (envelope RCPT TO, authoritative — includes Bcc, not
// forgeable like the To/Cc headers). SK timestamp = Mail.Timestamp (RFC3339, reliable, not the Date
// header). SK + idempotency key = the RFC 5322 Message-ID (CommonHeaders.MessageID), distinct from
// Mail.MessageID (the SES id, which is the S3 object key).
func itemsForMessage(ses events.SimpleEmailService, domain string) []mailItem {
	mail := ses.Mail
	ts := mail.Timestamp.UTC().Format(time.RFC3339)
	rfcID := mail.CommonHeaders.MessageID
	s3Key := InboundPrefix + mail.MessageID
	from := ""
	if len(mail.CommonHeaders.From) > 0 {
		from = mail.CommonHeaders.From[0]
	}

	suffix := "@" + domain
	items := make([]mailItem, 0, len(ses.Receipt.Recipients))
	for _, addr := range ses.Receipt.Recipients {
		if !strings.HasSuffix(strings.ToLower(addr), suffix) {
			continue
		}
		items = append(items, mailItem{
			PK:        "mailbox#" + strings.ToLower(addr),
			SK:        "ts#" + ts + "#" + rfcID,
			MessageID: mail.MessageID,
			S3Key:     s3Key,
			From:      from,
			Subject:   mail.CommonHeaders.Subject,
			Date:      mail.CommonHeaders.Date,
		})
	}
	return items
}

// InboundPrefix mirrors infra.InboundObjectPrefix; SES appends Mail.MessageID verbatim to it.
const InboundPrefix = "inbound/"

var (
	once  sync.Once
	ddb   *dynamodb.Client
	table string
	dom   string
	initErr error
)

func initClients(ctx context.Context) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		initErr = fmt.Errorf("load aws config: %w", err)
		return
	}
	ddb = dynamodb.NewFromConfig(cfg)
	table = os.Getenv("MAIL_INDEX_TABLE")
	dom = os.Getenv("MAIL_DOMAIN")
}

func handler(ctx context.Context, evt events.SimpleEmailEvent) error {
	once.Do(func() { initClients(ctx) })
	if initErr != nil {
		return initErr // a failed cold-start also routes to the OnFailure destination
	}
	for _, rec := range evt.Records {
		for _, item := range itemsForMessage(rec.SES, dom) {
			av, err := attributevalue.MarshalMap(item)
			if err != nil {
				return fmt.Errorf("marshal item: %w", err)
			}
			_, err = ddb.PutItem(ctx, &dynamodb.PutItemInput{
				TableName:           aws.String(table),
				Item:                av,
				ConditionExpression: aws.String("attribute_not_exists(SK)"),
			})
			if err != nil {
				var cfe *ddbtypes.ConditionalCheckFailedException
				if errors.As(err, &cfe) {
					continue // re-delivery: idempotent; index the remaining recipients
				}
				return fmt.Errorf("putitem %s: %w", item.PK, err)
			}
		}
	}
	return nil
}

func main() {
	lambda.Start(handler)
}
```

- [ ] **Step 4: Run the test — verify it PASSES + vet**

Run: `go test ./cmd/lambda/receive/ -run TestItemsForMessage && go vet ./cmd/lambda/receive/`
Expected: PASS; vet exit 0 (confirms the `errors.As` form is correct — `&cfe` where `cfe` is `*ConditionalCheckFailedException`).

- [ ] **Step 5: Commit**

```bash
git add cmd/lambda/receive/main.go cmd/lambda/receive/main_test.go
git commit -m "feat(sp-3): receive Lambda — index per domain recipient, idempotent by Message-ID"
```

---

## Task 6: Wire the Lambda + DLQ into ReceivingStack (TDD)

**Files:**
- Modify: `internal/infra/receiving_stack.go`
- Modify: `internal/infra/receiving_stack_test.go`

- [ ] **Step 1: Append the failing test** to `internal/infra/receiving_stack_test.go`:

```go
func TestReceiveLambdaAndDlq(t *testing.T) {
	template := synthReceiving(t)

	// The Go Lambda on provided.al2023 / arm64.
	template.HasResourceProperties(jsii.String("AWS::Lambda::Function"), map[string]any{
		"FunctionName":  "mail-receive",
		"Runtime":       "provided.al2023",
		"Architectures": []any{"arm64"},
	})
	// An SSE-SQS DLQ.
	template.HasResourceProperties(jsii.String("AWS::SQS::Queue"), map[string]any{
		"QueueName":       "mail-receive-dlq",
		"SqsManagedSseEnabled": true,
	})
	// Async OnFailure destination wired (EventInvokeConfig).
	template.ResourceCountIs(jsii.String("AWS::Lambda::EventInvokeConfig"), jsii.Number(1))
}
```

- [ ] **Step 2: Run the test — verify it FAILS**

Run: `go test ./internal/infra/ -run TestReceiveLambdaAndDlq`
Expected: FAIL — no Lambda/SQS resources yet.

- [ ] **Step 3: Add the Lambda + DLQ helper** — in `internal/infra/receiving_stack.go`, add the imports and the helper, and call it from `NewReceivingStack` (capture the table so the Lambda can be granted PutItem):

Add to imports:
```go
	"github.com/aws/aws-cdk-go/awscdklambdagoalpha/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambdadestinations"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssqs"
```

Change the orchestrator body so the table flows to the Lambda:
```go
	table := addReceiveTable(stack)
	fn := addReceiveLambda(stack, table)
```

Add the helper:
```go
// addReceiveLambda builds the Go Lambda (provided.al2023, arm64), grants it PutItem-only on the table,
// and routes async failures to an SSE-SQS DLQ via an OnFailure destination (richer than the legacy
// DeadLetterQueue prop). The handler reads MAIL_INDEX_TABLE + MAIL_DOMAIN from the environment.
func addReceiveLambda(stack awscdk.Stack, table awsdynamodb.Table) awslambda.IFunction {
	dlq := awssqs.NewQueue(stack, jsii.String("ReceiveDlq"), &awssqs.QueueProps{
		QueueName:     jsii.String(ReceiveDlqName),
		Encryption:    awssqs.QueueEncryption_SQS_MANAGED,
		RetentionPeriod: awscdk.Duration_Days(jsii.Number(14)),
	})

	fn := awscdklambdagoalpha.NewGoFunction(stack, jsii.String("ReceiveFunction"), &awscdklambdagoalpha.GoFunctionProps{
		FunctionName: jsii.String(ReceiveFunctionName),
		Entry:        jsii.String("cmd/lambda/receive"),
		Runtime:      awslambda.Runtime_PROVIDED_AL2023(),
		Architecture: awslambda.Architecture_ARM_64(),
		Timeout:      awscdk.Duration_Seconds(jsii.Number(30)),
		MemorySize:   jsii.Number(128),
		RetryAttempts: jsii.Number(2),
		OnFailure:    awslambdadestinations.NewSqsDestination(dlq),
		Environment: &map[string]*string{
			"MAIL_INDEX_TABLE": jsii.String(MailIndexTableName),
			"MAIL_DOMAIN":      jsii.String(DomainName),
		},
	})

	// PutItem-only — NOT GrantReadWriteData (idempotent insert needs only writes).
	table.GrantWriteData(fn)
	return fn
}
```

- [ ] **Step 4: Run the test — verify it PASSES**

Run: `go test ./internal/infra/ -run TestReceiveLambdaAndDlq`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/infra/receiving_stack.go internal/infra/receiving_stack_test.go
git commit -m "feat(sp-3): receive Lambda (GoFunction arm64) + SSE-SQS DLQ via OnFailure destination"
```

---

## Task 7: Receipt rule set + rule + S3/Lambda actions + bucket policy + invoke permission (TDD)

**Files:**
- Modify: `internal/infra/receiving_stack.go`
- Modify: `internal/infra/receiving_stack_test.go`

- [ ] **Step 1: Append the failing test** to `internal/infra/receiving_stack_test.go`:

```go
func TestReceiptRuleAndBucketPolicy(t *testing.T) {
	template := synthReceiving(t)

	template.ResourceCountIs(jsii.String("AWS::SES::ReceiptRuleSet"), jsii.Number(1))
	// Catch-all rule (no Recipients), TLS required, scan enabled, S3 action then Lambda action.
	template.HasResourceProperties(jsii.String("AWS::SES::ReceiptRule"), map[string]any{
		"Rule": assertions.Match_ObjectLike(&map[string]any{
			"Enabled":     true,
			"ScanEnabled": true,
			"TlsPolicy":   "Require",
		}),
	})
	// SES is granted PutObject on the bucket, scoped by SourceAccount + the receipt-rule ARN.
	template.HasResourceProperties(jsii.String("AWS::S3::BucketPolicy"), map[string]any{
		"PolicyDocument": map[string]any{
			"Statement": assertions.Match_ArrayWith(&[]any{
				assertions.Match_ObjectLike(&map[string]any{
					"Action":    "s3:PutObject",
					"Principal": map[string]any{"Service": "ses.amazonaws.com"},
					"Condition": map[string]any{
						"StringEquals": assertions.Match_ObjectLike(&map[string]any{
							"AWS:SourceAccount": "367707589526",
						}),
					},
				}),
			}),
		},
	})
	// SES is granted lambda:InvokeFunction (or the invoke fails silently → no item, empty DLQ).
	template.HasResourceProperties(jsii.String("AWS::Lambda::Permission"), map[string]any{
		"Action":    "lambda:InvokeFunction",
		"Principal": "ses.amazonaws.com",
	})
}
```

- [ ] **Step 2: Run the test — verify it FAILS**

Run: `go test ./internal/infra/ -run TestReceiptRuleAndBucketPolicy`
Expected: FAIL.

- [ ] **Step 3: Add the receipt-rule helper** — in `internal/infra/receiving_stack.go`, add imports and the helper, and call it from the orchestrator (after the Lambda, passing the bucket + fn):

Add to imports:
```go
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsses"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssesactions"
```

In `NewReceivingStack`, after `fn := addReceiveLambda(...)`:
```go
	addReceiptRule(stack, bucket, fn)
```

Add the helper:
```go
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

	// Bucket policy created in THIS stack (avoids the cross-stack cycle). Scope to this account + the
	// exact receipt-rule ARN with StringEquals (tighter than ArnLike).
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

	// SES must be allowed to invoke the Lambda action; absence = silent indexing loss (finding I1).
	fn.AddPermission(jsii.String("AllowSESInvoke"), &awslambda.Permission{
		Principal:    awsiam.NewServicePrincipal(jsii.String("ses.amazonaws.com"), nil),
		Action:       jsii.String("lambda:InvokeFunction"),
		SourceAccount: jsii.String(Account),
		SourceArn:    jsii.String(ReceiptRuleArn),
	})
}
```

- [ ] **Step 4: Run the test — verify it PASSES**

Run: `go test ./internal/infra/ -run TestReceiptRuleAndBucketPolicy`
Expected: PASS.

- [ ] **Step 5: Verify the whole stack synthesizes WITHOUT a dependency cycle**

Run: `go test ./internal/infra/ -run TestMailIndexTable` then `go build ./...`
Expected: PASS / exit 0. (If a cycle appears, the BucketPolicy is in the wrong stack — it must be in ReceivingStack per C1.)

- [ ] **Step 6: Commit**

```bash
git add internal/infra/receiving_stack.go internal/infra/receiving_stack_test.go
git commit -m "feat(sp-3): catch-all receipt rule (S3→Lambda) + SES bucket policy + invoke permission"
```

---

## Task 8: Rule-set activation custom resource + DNS (MX + DMARC) + observability (TDD)

**Files:**
- Modify: `internal/infra/receiving_stack.go`
- Modify: `internal/infra/receiving_stack_test.go`

- [ ] **Step 1: Append the failing test** to `internal/infra/receiving_stack_test.go`:

```go
func TestActivationDnsAndObservability(t *testing.T) {
	template := synthReceiving(t)

	// Custom resource activates the rule set (AWS::SES::ReceiptRuleSet has no declarative Active).
	template.ResourceCountIs(jsii.String("Custom::AWS"), jsii.Number(1))
	// Apex MX for inbound.
	template.HasResourceProperties(jsii.String("AWS::Route53::RecordSet"), map[string]any{
		"Type": "MX",
		"Name": "erickaldama.com.",
	})
	// DLQ-depth alarm.
	template.HasResourceProperties(jsii.String("AWS::CloudWatch::Alarm"), map[string]any{
		"Namespace":          "AWS/SQS",
		"MetricName":         "ApproximateNumberOfMessagesVisible",
		"ComparisonOperator": "GreaterThanThreshold",
		"Threshold":          0,
	})
	// Operator email subscription on the SP-2 bounce/complaint topic.
	template.HasResourceProperties(jsii.String("AWS::SNS::Subscription"), map[string]any{
		"Protocol": "email",
		"Endpoint": "esaldgut@gmail.com",
	})
}
```

- [ ] **Step 2: Run the test — verify it FAILS**

Run: `go test ./internal/infra/ -run TestActivationDnsAndObservability`
Expected: FAIL.

- [ ] **Step 3: Add the helpers** — in `internal/infra/receiving_stack.go`, add imports and three helpers, called from the orchestrator. Pass the DLQ out of `addReceiveLambda` so the alarm can reference it (change its return to `(awslambda.IFunction, awssqs.Queue)` and update the call site).

Add to imports:
```go
	"github.com/aws/aws-cdk-go/awscdk/v2/awscloudwatch"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscloudwatchactions"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsroute53"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssns"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssnssubscriptions"
	"github.com/aws/aws-cdk-go/awscdk/v2/customresources"
```

Change `addReceiveLambda` return + orchestrator:
```go
	table := addReceiveTable(stack)
	fn, dlq := addReceiveLambda(stack, table)
	addReceiptRule(stack, bucket, fn)
	addRuleSetActivation(stack)
	addDmarcAndMx(stack)
	addObservability(stack, dlq)
```
(In `addReceiveLambda`, change the signature to `(awslambda.IFunction, awssqs.Queue)` and `return fn, dlq`.)

Add the helpers:
```go
// addRuleSetActivation marks the rule set active via the SES API (no declarative CFN field — C2).
func addRuleSetActivation(stack awscdk.Stack) {
	customresources.NewAwsCustomResource(stack, jsii.String("ActivateRuleSet"), &customresources.AwsCustomResourceProps{
		OnCreate: &customresources.AwsSdkCall{
			Service:    jsii.String("SES"),
			Action:     jsii.String("setActiveReceiptRuleSet"),
			Parameters: map[string]any{"RuleSetName": ReceiptRuleSetName},
			PhysicalResourceId: customresources.PhysicalResourceId_Of(jsii.String("erickaldama-inbound-active")),
		},
		OnUpdate: &customresources.AwsSdkCall{
			Service:    jsii.String("SES"),
			Action:     jsii.String("setActiveReceiptRuleSet"),
			Parameters: map[string]any{"RuleSetName": ReceiptRuleSetName},
			PhysicalResourceId: customresources.PhysicalResourceId_Of(jsii.String("erickaldama-inbound-active")),
		},
		Policy: customresources.AwsCustomResourcePolicy_FromSdkCalls(&customresources.SdkCallsPolicyOptions{
			Resources: customresources.AwsCustomResourcePolicy_ANY_RESOURCE(),
		}),
	})
}

// addDmarcAndMx publishes the apex inbound MX. The DMARC TXT is owned by SendingStack (re-pointed
// there via DmarcValue) — NOT duplicated here, to avoid two stacks managing one record.
func addDmarcAndMx(stack awscdk.Stack) {
	zone := awsroute53.HostedZone_FromHostedZoneAttributes(stack, jsii.String("ImportedZone"),
		&awsroute53.HostedZoneAttributes{
			HostedZoneId: jsii.String(HostedZoneID),
			ZoneName:     jsii.String(DomainName),
		})
	awsroute53.NewMxRecord(stack, jsii.String("InboundMx"), &awsroute53.MxRecordProps{
		Zone: zone,
		Values: &[]*awsroute53.MxRecordValue{
			{Priority: jsii.Number(10), HostName: jsii.String(InboundMxHost)},
		},
	})
}

// addObservability alarms on DLQ depth>0 and routes it — plus the SP-2 bounce/complaint topic — to the
// operator's email. Closes the fan-out SP-2 left open (the topic had no subscriber).
func addObservability(stack awscdk.Stack, dlq awssqs.Queue) {
	topic := awssns.Topic_FromTopicArn(stack, jsii.String("BounceTopic"),
		jsii.String("arn:aws:sns:us-east-1:"+Account+":"+BounceTopicName))
	topic.AddSubscription(awssnssubscriptions.NewEmailSubscription(jsii.String(OperatorEmail), nil))

	alarm := awscloudwatch.NewAlarm(stack, jsii.String("DlqDepthAlarm"), &awscloudwatch.AlarmProps{
		Metric:             dlq.MetricApproximateNumberOfMessagesVisible(nil),
		Threshold:          jsii.Number(0),
		EvaluationPeriods:  jsii.Number(1),
		ComparisonOperator: awscloudwatch.ComparisonOperator_GREATER_THAN_THRESHOLD,
		TreatMissingData:   awscloudwatch.TreatMissingData_NOT_BREACHING,
	})
	alarm.AddAlarmAction(awscloudwatchactions.NewSnsAction(topic))
}
```

- [ ] **Step 4: Run the test — verify it PASSES**

Run: `go test ./internal/infra/ -run TestActivationDnsAndObservability`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/infra/receiving_stack.go internal/infra/receiving_stack_test.go
git commit -m "feat(sp-3): rule-set activation custom resource + apex MX + DLQ alarm + SNS operator sub"
```

---

## Task 9: Extend mail-readonly with dynamodb/lambda/sqs reads (TDD)

**Files:**
- Modify: `internal/infra/foundation_stack.go`
- Modify: `internal/infra/foundation_stack_test.go`

- [ ] **Step 1: Append the failing test** to `internal/infra/foundation_stack_test.go` (inside `TestReadonlyManagedPolicy`, add a third assertion block):

```go
	// SP-3: the agent must be able to read the receive pipeline it deploys (dynamodb/lambda/sqs).
	// Pinned so a refactor cannot re-blind the verifier. SES receipt reads are already covered by ses:Describe*.
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
```

- [ ] **Step 2: Run the test — verify it FAILS**

Run: `go test ./internal/infra/ -run TestReadonlyManagedPolicy`
Expected: FAIL — the new actions aren't in the policy yet.

- [ ] **Step 3: Add the reads** — in `internal/infra/foundation_stack.go`, in the `AllowRegionalReadsUsEast1` statement's `Actions`, append (after the existing sns/events reads):

```go
				"dynamodb:DescribeTable", "dynamodb:Query", "dynamodb:GetItem",
				"lambda:GetFunction", "lambda:GetFunctionConfiguration",
				"sqs:GetQueueAttributes",
```

- [ ] **Step 4: Run the test — verify it PASSES**

Run: `go test ./internal/infra/ -run TestReadonlyManagedPolicy`
Expected: PASS.

- [ ] **Step 5: Mirror in the JSON + commit**

In `iam/readonly-policy.json`, append the same six actions to the `AllowRegionalReadsUsEast1` Action array. Then:
```bash
go test ./internal/infra/
git add internal/infra/foundation_stack.go internal/infra/foundation_stack_test.go iam/readonly-policy.json
git commit -m "feat(sp-3): extend mail-readonly with dynamodb/lambda/sqs reads for self-verification"
```

---

## Task 10: Register the stacks + full-suite green + DMARC re-point in SendingStack

**Files:**
- Modify: `cmd/cdk/main.go`
- Modify: `internal/infra/sending_stack_test.go` (the DMARC assertion now includes the rua)

- [ ] **Step 1: Register both stacks** — in `cmd/cdk/main.go`, after the `SendingStack` registration, add:

```go
	storage, rawBucket := infra.NewMailStorageStack(app, "MailStorageStack", &awscdk.StackProps{
		Env: env(),
	})
	_ = storage

	infra.NewReceivingStack(app, "ReceivingStack", &awscdk.StackProps{
		Env: env(),
	}, rawBucket)
```

- [ ] **Step 2: Update the SendingStack DMARC assertion** — in `internal/infra/sending_stack_test.go`, the DMARC `TxtRecord` test now expects the rua. Find the DMARC assertion and change the expected value to:

```go
		"v=DMARC1; p=none; rua=mailto:dmarc-reports@erickaldama.com",
```

- [ ] **Step 3: Run the FULL suite + build**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all green (infra, lambda, hook, eval). vet clean (confirms the Lambda `errors.As`).

- [ ] **Step 4: Synthesize all stacks (read-only) to confirm no cycle + correct inventory**

Run: `gofmt -l . ` (expect empty) then verify the app synthesizes — the agent runs `go run ./cmd/cdk` is NOT needed; the unit tests synth each stack. Confirm `go test ./internal/infra/` exercises `synthReceiving` (which builds MailStorage + Receiving together) with no `DependencyCycle` panic.

- [ ] **Step 5: Commit**

```bash
git add cmd/cdk/main.go internal/infra/sending_stack_test.go
git commit -m "feat(sp-3): register MailStorageStack + ReceivingStack; SendingStack DMARC rua re-pointed"
```

---

## Task 11 (HUMAN-EXECUTED): deploy + verify

The agent never deploys. The human runs the mutations out-of-band with SSO `AdministratorAccess-367707589526`; the agent prepares exact commands and verifies read-only after.

- [ ] **Step 1: Agent — pre-flight (read-only, hook-allowed)**

```bash
aws sts get-caller-identity --profile AdministratorAccess-367707589526   # expect 367707589526
go build ./... && go test ./...                                          # all green
```

- [ ] **Step 2: HUMAN — deploy in order, paste outputs back**

```bash
cdk deploy MailStorageStack --profile AdministratorAccess-367707589526   # bucket first
cdk deploy ReceivingStack   --profile AdministratorAccess-367707589526   # rule set + Lambda + table + DLQ + activation
cdk deploy SendingStack     --profile AdministratorAccess-367707589526   # re-pointed DMARC rua
cdk deploy FoundationStack  --profile AdministratorAccess-367707589526   # mail-readonly reads
```
(No exec-policy/boundary change needed — audit finding I3: Go bundling is a zip asset, no new service.)

- [ ] **Step 3: HUMAN — confirm the SNS email subscription**

Click the confirmation link in the email AWS sends to `esaldgut@gmail.com` (or it stays PendingConfirmation).

- [ ] **Step 4: HUMAN — send a real test email**

From any external mailbox (the Mailbox Simulator does NOT exercise receiving), send a message to `test@erickaldama.com`.

- [ ] **Step 5: Agent — verify the pipeline (read-only as mail-readonly)**

```bash
# object landed in S3 (bucket-level read; agent cannot read the body — hard-deny on GetObject)
aws s3api list-objects-v2 --bucket erickaldama-mail-raw --prefix inbound/ --profile mail-readonly --region us-east-1 --query 'KeyCount'
# item indexed in DynamoDB
aws dynamodb query --table-name mail-index --profile mail-readonly --region us-east-1 \
  --key-condition-expression 'PK = :pk' \
  --expression-attribute-values '{":pk":{"S":"mailbox#test@erickaldama.com"}}' --query 'Count'
# DLQ empty (no failures)
aws sqs get-queue-attributes --queue-url $(aws sqs get-queue-url --queue-name mail-receive-dlq --profile mail-readonly --region us-east-1 --query QueueUrl --output text) --attribute-names ApproximateNumberOfMessages --profile mail-readonly --region us-east-1
# active rule set is ours
aws ses describe-active-receipt-rule-set --profile mail-readonly --region us-east-1 --query 'Metadata.Name'
```
Expected: KeyCount ≥ 1; Count ≥ 1; ApproximateNumberOfMessages = 0; Name = `erickaldama-inbound`.

---

## Task 12: Persist state + EXECUTION-LOG + merge

- [ ] **Step 1: Append the SP-3 record to `docs/superpowers/EXECUTION-LOG.md`** — per-task commit hashes + the live results (deploy, real-email receive verified in S3+DynamoDB, DLQ empty, active rule set) + any findings.

- [ ] **Step 2: Mark all plan checkboxes `[x]`** in this file.

- [ ] **Step 3: Update task #17 → completed** via TaskUpdate.

- [ ] **Step 4: Commit**

```bash
git add docs/superpowers/EXECUTION-LOG.md docs/superpowers/plans/2026-06-17-sp3-receiving.md
git commit -m "docs(sp-3): record receive-pipeline deploy + verification (Task 11+12)"
```

- [ ] **Step 5: Update `RETOMAR-AQUI.md`** (SP-3 done → SP-4 / CD next) and finish via `superpowers:finishing-a-development-branch`. NOTE: this repo now has a remote with Git Flow + branch protection — the finish is a PR `feature → develop` (CI must be green), NOT a local `--no-ff` merge. Use option 2 (push + PR), let CI pass, merge the PR.

---

## Notes for the implementer

- **The agent never deploys.** Tasks 1–10 and 12 are agent work; Task 11 is the human gate.
- **Cross-stack cycle (C1):** the bucket policy MUST be created with `awss3.NewBucketPolicy` in ReceivingStack, never `bucket.AddToResourcePolicy` (which lands it in the owning stack and cycles). If `synthReceiving` panics with `DependencyCycle`, this is the cause.
- **Lambda correctness (C3):** S3 key = `Mail.MessageID`; idempotency/SK key = `CommonHeaders.MessageID`; recipients = `Receipt.Recipients`; SK timestamp = `Mail.Timestamp`. The `errors.As` form is `var cfe *types.ConditionalCheckFailedException; errors.As(err, &cfe)` — `go vet` (run in Task 5 step 4 and Task 10 step 3) is the gate that catches the wrong form.
- **Silent-loss trap (I1):** the SES→Lambda invoke permission is non-optional — without it the object lands in S3 but nothing indexes and the DLQ stays empty.
- **No boundary/exec-policy change (I3):** unlike SP-2, Go Lambda bundling needs no new service.
- **Disciplines:** aws-cli-pre-flight-canonical, modern-go-guidelines (Go 1.26, `any`), avoid-string-match-error-silencing (idempotency via errors.As), infra-plan-three-source-cross-check.
- **Git Flow:** finish as a PR into `develop` (CI green), not a local merge.
