// Package aiassist is the AI layer: a neutral LLMProvider interface with a shared agent loop. Two providers
// (ollama local default / claude API opt-in) translate to/from these neutral types. ToolCall carries ID
// (Anthropic correlation) + Name (Ollama correlation by name+order).
package aiassist

import "context"

type Message struct {
	Role      string // "user" | "assistant" | "tool"
	Content   string
	ToolCalls []ToolCall // assistant turn
	ToolName  string     // tool result turn (Ollama-style correlation)
	ToolID    string     // tool result turn (Anthropic tool_use_id correlation)
}

type ToolSpec struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON-schema-ish
}

type ToolCall struct {
	ID   string         // Anthropic tool_use_id; empty for Ollama
	Name string         // both
	Args map[string]any // Ollama arguments is an OBJECT (not string); Anthropic input is object
}

type Response struct {
	Text      string
	ToolCalls []ToolCall
	Stop      string // "tool_use" continues the loop; anything else ends it
}

type LLMProvider interface {
	Chat(ctx context.Context, msgs []Message, tools []ToolSpec) (Response, error)
	Name() string
}
