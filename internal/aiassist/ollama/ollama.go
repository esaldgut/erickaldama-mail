// Package ollama implements aiassist.LLMProvider against a local Ollama daemon (default localhost:11434).
// Verified shape (docs.ollama.com/capabilities/tool-calling): tools wrapped {type:function,function:{...}};
// arguments is an OBJECT; tool_calls have no id (correlate by tool_name+order); result {role:tool,tool_name,content};
// stream:false explicit. DEFAULT-safe: the mail body never leaves the Mac.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"erickaldama-mail/internal/aiassist"
)

type Provider struct {
	model  string
	host   string
	client *http.Client
}

func New(model, host string) *Provider {
	if host == "" {
		host = "http://localhost:11434"
	}
	// NOT http.DefaultClient (Timeout=0 → hangs if the daemon accepts but never responds, e.g. model loading).
	return &Provider{model: model, host: host, client: &http.Client{Timeout: 120 * time.Second}}
}

func (p *Provider) Name() string { return "ollama:" + p.model }

type olToolFn struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}
type olTool struct {
	Type     string   `json:"type"`
	Function olToolFn `json:"function"`
}
type olMsg struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	ToolName  string `json:"tool_name,omitempty"`
	ToolCalls []struct {
		Function struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"` // OBJECT, not string
		} `json:"function"`
	} `json:"tool_calls,omitempty"`
}
type olReq struct {
	Model    string   `json:"model"`
	Messages []olMsg  `json:"messages"`
	Tools    []olTool `json:"tools,omitempty"`
	Stream   bool     `json:"stream"`
}
type olResp struct {
	Message olMsg `json:"message"`
}

func (p *Provider) Chat(ctx context.Context, msgs []aiassist.Message, tools []aiassist.ToolSpec) (aiassist.Response, error) {
	req := olReq{Model: p.model, Stream: false}
	for _, m := range msgs {
		om := olMsg{Role: m.Role, Content: m.Content, ToolName: m.ToolName}
		req.Messages = append(req.Messages, om)
	}
	for _, ts := range tools {
		req.Tools = append(req.Tools, olTool{Type: "function", Function: olToolFn{Name: ts.Name, Description: ts.Description, Parameters: ts.Parameters}})
	}
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.host+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return aiassist.Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return aiassist.Response{}, fmt.Errorf("ollama unreachable (is the daemon running on %s?): %w", p.host, err)
	}
	defer resp.Body.Close()
	var or olResp
	if err := json.NewDecoder(resp.Body).Decode(&or); err != nil {
		return aiassist.Response{}, err
	}
	out := aiassist.Response{Text: or.Message.Content, Stop: "end"}
	for _, tc := range or.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, aiassist.ToolCall{Name: tc.Function.Name, Args: tc.Function.Arguments})
	}
	if len(out.ToolCalls) > 0 {
		out.Stop = "tool_use"
	}
	return out, nil
}
