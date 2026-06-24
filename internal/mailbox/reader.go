// Package mailbox is the mail data plane: Reader (DynamoDB Query + S3 GetObject) and Sender (SES).
package mailbox

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"erickaldama-mail/internal/awsconf"
)

// Header mirrors one mail-index row (schema source: cmd/lambda/receive/main.go).
type Header struct {
	PK        string `dynamodbav:"PK"        json:"pk,omitempty"`
	SK        string `dynamodbav:"SK"        json:"sk,omitempty"`
	MessageID string `dynamodbav:"messageId" json:"messageId,omitempty"`
	S3Key     string `dynamodbav:"s3Key"     json:"s3Key,omitempty"`
	From      string `dynamodbav:"from"      json:"from,omitempty"`
	Subject   string `dynamodbav:"subject"   json:"subject,omitempty"`
	Date      string `dynamodbav:"date"      json:"date,omitempty"`
}

// DynamoAPI is the minimal DynamoDB interface required by Reader.
type DynamoAPI interface {
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// S3API is the minimal S3 interface required by Reader.
type S3API interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// Reader provides read access to the mail store: DynamoDB for headers, S3 for raw MIME.
type Reader struct {
	ddb DynamoAPI
	s3c S3API
}

// NewReader constructs a Reader backed by the given DynamoDB and S3 clients.
func NewReader(ddb DynamoAPI, s3c S3API) *Reader { return &Reader{ddb: ddb, s3c: s3c} }

// List queries one mailbox, newest first (ScanIndexForward=false). start is the pagination cursor (nil for first page).
func (r *Reader) List(ctx context.Context, mailbox string, limit int32, start map[string]ddbtypes.AttributeValue) ([]Header, map[string]ddbtypes.AttributeValue, error) {
	if limit <= 0 { // DynamoDB rejects Limit<=0; give the caller a contextual error instead of a raw AWS ValidationException
		return nil, nil, fmt.Errorf("List: limit must be > 0, got %d", limit)
	}
	out, err := r.ddb.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(awsconf.TableName),
		KeyConditionExpression: aws.String("PK = :pk"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":pk": &ddbtypes.AttributeValueMemberS{Value: "mailbox#" + mailbox},
		},
		ScanIndexForward:  aws.Bool(false),
		Limit:             aws.Int32(limit),
		ExclusiveStartKey: start,
	})
	if err != nil {
		return nil, nil, err
	}
	var hs []Header
	if err := attributevalue.UnmarshalListOfMaps(out.Items, &hs); err != nil {
		return nil, nil, err
	}
	return hs, out.LastEvaluatedKey, nil
}

// Open streams the raw MIME from S3.
// CALLER MUST Close the returned ReadCloser — the AWS SDK does not close it automatically; failure to do so leaks the HTTP connection.
func (r *Reader) Open(ctx context.Context, s3Key string) (io.ReadCloser, error) {
	out, err := r.s3c.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(awsconf.BucketName),
		Key:    aws.String(s3Key),
	})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}
