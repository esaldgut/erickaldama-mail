package aiassist

import (
	"context"
	"encoding/json"
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

// fakeSearcher records the args the tool passed and returns canned headers.
type fakeSearcher struct {
	gotMailbox, gotQuery string
	gotLimit             int
	ret                  []mailbox.Header
	err                  error
}

func (f *fakeSearcher) Search(_ context.Context, mb, q string, n int) ([]mailbox.Header, error) {
	f.gotMailbox, f.gotQuery, f.gotLimit = mb, q, n
	return f.ret, f.err
}

func TestReadMessageRedactsBody(t *testing.T) {
	// audit NUEVO-2: read_message must redact the raw S3 body BEFORE it re-enters the agent loop and can
	// reach a network backend (Claude). A leaked AKIA token in the body must come back masked.
	r := mailbox.NewReader(fakeDDB{}, fakeS3{body: "secreto AKIAIOSFODNN7EXAMPLE en el cuerpo"})
	out := execTool(context.Background(), toolDeps{reader: r}, "inbox", ToolCall{Name: "read_message", Args: map[string]any{"s3Key": "inbound/x"}})
	if strings.Contains(out, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("read_message leaked an unredacted secret: %q", out)
	}
	if !strings.Contains(out, "<REDACTED-SECRET>") {
		t.Fatalf("expected redaction placeholder, got %q", out)
	}
}

func TestSearchMessagesUsesSearcher(t *testing.T) {
	fs := &fakeSearcher{ret: []mailbox.Header{{Subject: "café", From: "x@y"}}}
	out := execTool(context.Background(), toolDeps{searcher: fs}, "inbox",
		ToolCall{Name: "search_messages", Args: map[string]any{"query": "caf", "limit": 5.0}})
	if fs.gotQuery != "caf" || fs.gotLimit != 5 || fs.gotMailbox != "inbox" {
		t.Errorf("searcher got mb=%q q=%q limit=%d", fs.gotMailbox, fs.gotQuery, fs.gotLimit)
	}
	var got []mailbox.Header
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output not JSON []Header: %v (%q)", err, out)
	}
	if len(got) != 1 || got[0].Subject != "café" {
		t.Errorf("want 1 header 'café', got %v", got)
	}
}

func TestSearchMessagesDefaultLimit(t *testing.T) {
	fs := &fakeSearcher{}
	execTool(context.Background(), toolDeps{searcher: fs}, "inbox",
		ToolCall{Name: "search_messages", Args: map[string]any{"query": "x"}})
	if fs.gotLimit != 50 {
		t.Errorf("default limit = %d, want 50", fs.gotLimit)
	}
}

func TestSearchMessagesSearcherError(t *testing.T) {
	fs := &fakeSearcher{err: context.DeadlineExceeded}
	out := execTool(context.Background(), toolDeps{searcher: fs}, "inbox",
		ToolCall{Name: "search_messages", Args: map[string]any{"query": "x"}})
	if !strings.HasPrefix(out, "error:") {
		t.Errorf("searcher error should surface as 'error: ...', got %q", out)
	}
}
