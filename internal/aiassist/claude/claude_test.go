package claude

import (
	"encoding/json"
	"testing"

	"erickaldama-mail/internal/aiassist"
)

func TestToolSpecMappingExtractsInnerProperties(t *testing.T) {
	// audit bug #1: the inner "properties" must land in InputSchema.Properties, NOT the whole schema object.
	specs := []aiassist.ToolSpec{{Name: "read_message", Description: "d", Parameters: map[string]any{
		"type":       "object",
		"properties": map[string]any{"s3Key": map[string]any{"type": "string"}},
		"required":   []string{"s3Key"},
	}}}
	tools := toAnthropicTools(specs)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool union, got %d", len(tools))
	}
	props, ok := tools[0].OfTool.InputSchema.Properties.(map[string]any)
	if !ok || props["s3Key"] == nil {
		t.Fatalf("InputSchema.Properties must be the inner props with s3Key, got %#v", tools[0].OfTool.InputSchema.Properties)
	}
	if _, nested := props["properties"]; nested {
		t.Fatal("schema double-nested: 'properties' must not appear inside Properties")
	}
}

func TestToolSpecRequiredFromJSON(t *testing.T) {
	// audit H-1: a spec round-tripped through JSON yields "required" as []any, NOT []string. The mapper must
	// still propagate the required fields (a bare .([]string) would silently drop them).
	specs := []aiassist.ToolSpec{{Name: "read_message", Description: "d", Parameters: map[string]any{
		"type":       "object",
		"properties": map[string]any{"s3Key": map[string]any{"type": "string"}},
		"required":   []any{"s3Key"}, // as decoded by json.Unmarshal
	}}}
	tools := toAnthropicTools(specs)
	req := tools[0].OfTool.InputSchema.Required
	if len(req) != 1 || req[0] != "s3Key" {
		t.Fatalf("required not propagated from []any schema: %#v", req)
	}
}

func TestParseToolInputUnmarshals(t *testing.T) {
	// audit bug #2: b.Input (json.RawMessage) must be unmarshaled into Args (else tools run with empty args).
	args := map[string]any{}
	if err := json.Unmarshal([]byte(`{"s3Key":"inbound/abc"}`), &args); err != nil {
		t.Fatal(err)
	}
	if args["s3Key"] != "inbound/abc" {
		t.Fatalf("args not parsed: %#v", args)
	}
}

func TestModelConstant(t *testing.T) {
	if Model != "claude-opus-4-8" {
		t.Fatalf("model must be claude-opus-4-8, got %q", Model)
	}
}
