package aiassist

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"erickaldama-mail/internal/mailbox"
)

// ReadOnlyTools are the agent's tools — NO send tool (blast radius bounded by design).
func ReadOnlyTools() []ToolSpec {
	return []ToolSpec{
		{Name: "list_messages", Description: "List recent message headers for the mailbox.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{"limit": map[string]any{"type": "integer"}}}},
		{Name: "read_message", Description: "Read one message body by s3Key.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{"s3Key": map[string]any{"type": "string"}}, "required": []string{"s3Key"}}},
		{Name: "search_subject", Description: "Find messages whose subject contains the query.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}}},
	}
}

// execTool runs one read-only tool. Tolerant of reader==nil (returns "[]") for unit tests.
func execTool(ctx context.Context, reader *mailbox.Reader, mb string, call ToolCall) string {
	if reader == nil {
		return "[]"
	}
	switch call.Name {
	case "list_messages":
		limit := int32(20)
		if v, ok := call.Args["limit"].(float64); ok && v > 0 {
			limit = int32(v)
		}
		hs, _, err := reader.List(ctx, mb, limit, nil)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		b, _ := json.Marshal(hs)
		return string(b)
	case "read_message":
		key, _ := call.Args["s3Key"].(string)
		rc, err := reader.Open(ctx, key)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		defer rc.Close()
		// io.Reader.Read may return <cap in one call (S3 streaming); ReadAll over a LimitReader reads the
		// whole capped body, not a single truncating chunk (audit bug: single rc.Read).
		body, err := io.ReadAll(io.LimitReader(rc, 256*1024))
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return string(body)
	case "search_subject":
		q, _ := call.Args["query"].(string)
		hs, _, err := reader.List(ctx, mb, 100, nil)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		var hits []mailbox.Header
		for _, h := range hs {
			// stdlib case-insensitive contains — handles Unicode (e.g. "café"); NO hand-rolled toLower (audit).
			if strings.Contains(strings.ToLower(h.Subject), strings.ToLower(q)) {
				hits = append(hits, h)
			}
		}
		b, _ := json.Marshal(hits)
		return string(b)
	}
	return fmt.Sprintf("unknown tool %q", call.Name)
}
