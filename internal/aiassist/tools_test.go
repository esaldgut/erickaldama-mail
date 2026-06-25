package aiassist

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"erickaldama-mail/internal/mailbox"
)

type fakeDDB struct{ out *dynamodb.QueryOutput }

func (f fakeDDB) Query(_ context.Context, _ *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return f.out, nil
}

type fakeS3 struct{ body string }

func (f fakeS3) GetObject(_ context.Context, _ *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(f.body))}, nil
}

func TestReadMessageRedactsBody(t *testing.T) {
	// audit NUEVO-2: read_message must redact the raw S3 body BEFORE it re-enters the agent loop and can
	// reach a network backend (Claude). A leaked AKIA token in the body must come back masked.
	r := mailbox.NewReader(fakeDDB{}, fakeS3{body: "secreto AKIAIOSFODNN7EXAMPLE en el cuerpo"})
	out := execTool(context.Background(), r, "inbox", ToolCall{Name: "read_message", Args: map[string]any{"s3Key": "inbound/x"}})
	if strings.Contains(out, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("read_message leaked an unredacted secret: %q", out)
	}
	if !strings.Contains(out, "<REDACTED-SECRET>") {
		t.Fatalf("expected redaction placeholder, got %q", out)
	}
}
