// Command receive is the SES inbound Lambda: SES writes the raw MIME to S3 first, then invokes this
// (Event/async) with metadata+headers only (no body). It indexes one item per domain recipient into
// DynamoDB, idempotent by the RFC 5322 Message-ID. Real failures return an error → async retries →
// OnFailure SQS destination.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
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
)

// mailItem is one DynamoDB row: a message as seen by one recipient mailbox.
type mailItem struct {
	PK        string `dynamodbav:"PK"`
	SK        string `dynamodbav:"SK"`
	MessageID string `dynamodbav:"messageId"`
	S3Key     string `dynamodbav:"s3Key"`
	From      string `dynamodbav:"from"`
	Subject   string `dynamodbav:"subject"`
	Date      string `dynamodbav:"date"`
}

// InboundPrefix mirrors infra.InboundObjectPrefix; SES appends Mail.MessageID verbatim to it.
const InboundPrefix = "inbound/"

// itemsForMessage builds one mailItem per recipient under domain. Pure (no AWS) — unit-tested.
// Recipients come from Receipt.Recipients (envelope RCPT TO, authoritative — includes Bcc, not the
// forgeable To/Cc headers). SK timestamp = Mail.Timestamp (RFC3339, reliable). SK + idempotency key =
// the RFC 5322 Message-ID (CommonHeaders.MessageID), distinct from Mail.MessageID (the SES id = S3 key).
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

var (
	once    sync.Once
	ddb     *dynamodb.Client
	table   string
	dom     string
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
		return initErr
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
					continue
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
