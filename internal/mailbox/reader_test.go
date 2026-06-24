package mailbox

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type fakeDDB struct{ out *dynamodb.QueryOutput }

func (f fakeDDB) Query(ctx context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return f.out, nil
}

type fakeS3 struct{ body string }

func (f fakeS3) GetObject(ctx context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(f.body))}, nil
}

func TestListUnmarshals(t *testing.T) {
	out := &dynamodb.QueryOutput{Items: []map[string]ddbtypes.AttributeValue{{
		"PK":        &ddbtypes.AttributeValueMemberS{Value: "mailbox#test@erickaldama.com"},
		"SK":        &ddbtypes.AttributeValueMemberS{Value: "ts#2026-06-23T10:00:00Z#<m@x>"},
		"messageId": &ddbtypes.AttributeValueMemberS{Value: "abc123"},
		"s3Key":     &ddbtypes.AttributeValueMemberS{Value: "inbound/abc123"},
		"from":      &ddbtypes.AttributeValueMemberS{Value: "alice@example.com"},
		"subject":   &ddbtypes.AttributeValueMemberS{Value: "Hola"},
		"date":      &ddbtypes.AttributeValueMemberS{Value: "Mon, 23 Jun 2026 10:00:00 +0000"},
	}}}
	r := NewReader(fakeDDB{out: out}, fakeS3{})
	hs, _, err := r.List(context.Background(), "test@erickaldama.com", 10, nil)
	if err != nil || len(hs) != 1 {
		t.Fatalf("list: %+v err=%v", hs, err)
	}
	// Assert ALL seven fields unmarshal (guards against a dynamodbav tag drifting silently). (audit H-4)
	got := hs[0]
	want := Header{
		PK: "mailbox#test@erickaldama.com", SK: "ts#2026-06-23T10:00:00Z#<m@x>",
		MessageID: "abc123", S3Key: "inbound/abc123", From: "alice@example.com",
		Subject: "Hola", Date: "Mon, 23 Jun 2026 10:00:00 +0000",
	}
	if got != want {
		t.Fatalf("unmarshal mismatch:\n got  %+v\n want %+v", got, want)
	}
}

func TestOpenReadsBody(t *testing.T) {
	r := NewReader(fakeDDB{}, fakeS3{body: "RAW MIME"})
	rc, err := r.Open(context.Background(), "inbound/abc123")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != "RAW MIME" {
		t.Fatalf("body: %q", b)
	}
}
