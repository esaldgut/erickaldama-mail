package aiassist

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"erickaldama-mail/internal/mailbox"
	"erickaldama-mail/internal/redact"
)

// ReadOnlyTools are the agent's tools — NO send tool (blast radius bounded by design).
func ReadOnlyTools() []ToolSpec {
	return []ToolSpec{
		{Name: "list_messages", Description: "List recent message headers for the mailbox.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{"limit": map[string]any{"type": "integer"}}}},
		{Name: "read_message", Description: "Read one message body by s3Key.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{"s3Key": map[string]any{"type": "string"}}, "required": []string{"s3Key"}}},
		{Name: "search_messages",
			Description: "Search messages by sender or subject and return matching headers as JSON, ranked best-first with prefix matching on the last word. Use plain search keywords, for example \"amazon\", \"payment receipt\", or a person's name. This tool does not support field operators like \"from:\" or \"subject:\": a query such as \"from:amazon\" is searched as the two literal words \"from\" and \"amazon\", not as a sender filter, so use plain keywords like \"amazon\" instead. Pass the terms you want to appear anywhere in the sender or subject.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Plain search keywords matched against the sender and subject text, e.g. \"amazon\" or \"invoice march\". A single keyword works best; multiple words are matched as an adjacent phrase, in order. Field-operator prefixes such as \"from:\" or \"subject:\" are searched as literal words, not as filters."},
				"limit": map[string]any{"type": "integer", "description": "Max results (default 50)."},
			}, "required": []string{"query"}}},
	}
}

// toolDeps carries what execTool needs: the live Reader (list/read tools) and the Searcher (search tool).
type toolDeps struct {
	reader   *mailbox.Reader
	searcher Searcher
}

// execTool runs one read-only tool.
func execTool(ctx context.Context, deps toolDeps, mb string, call ToolCall) string {
	switch call.Name {
	case "list_messages":
		if deps.reader == nil {
			return "[]"
		}
		limit := int32(20)
		if v, ok := call.Args["limit"].(float64); ok && v > 0 {
			limit = int32(v)
		}
		hs, _, err := deps.reader.List(ctx, mb, limit, nil)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return marshalHeaders(hs)
	case "read_message":
		if deps.reader == nil {
			return "[]"
		}
		key, _ := call.Args["s3Key"].(string)
		rc, err := deps.reader.Open(ctx, key)
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
		// Redact at the point the raw mail body leaves the mailbox, BEFORE it re-enters the agent loop and
		// reaches any backend (incl. Claude, which crosses the network). Without this, an `ai agent --backend
		// claude` would ship third-party PII unmasked — only `goal`/`summarize` redact at the call site (audit NUEVO-2).
		return redact.Redact(string(body))
	case "search_messages":
		if deps.searcher == nil {
			return "[]"
		}
		q, _ := call.Args["query"].(string)
		limit := 50
		if l, ok := call.Args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}
		// AUDIT FIX (plan MAYOR): clamp the upper bound. Without this, an LLM sending a huge limit
		// (e.g. 3e9) overflows int32(limit) to negative in substringSearcher → reader.List rejects
		// it ("limit must be > 0") and the degraded fallback returns an error instead of results.
		if limit > 1000 {
			limit = 1000
		}
		hs, err := deps.searcher.Search(ctx, mb, q, limit)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return marshalHeaders(hs)
	}
	return fmt.Sprintf("unknown tool %q", call.Name)
}

// marshalHeaders serializes headers to a JSON array. A nil/empty slice becomes "[]" (not "null"),
// so the LLM always receives a valid JSON array as the tool's Description promises.
func marshalHeaders(hs []mailbox.Header) string {
	if len(hs) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(hs)
	return string(b)
}
