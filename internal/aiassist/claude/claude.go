// Package claude implements aiassist.LLMProvider against the Anthropic Messages API (claude-opus-4-8).
// OPT-IN: the mail body crosses the network to api.anthropic.com (not trained on by default; ZDR recommended).
// Tools is []ToolUnionParam (wrap with OfTool). Adaptive thinking via union (no helper). NO temperature/top_p/
// budget_tokens (Opus 4.8 → 400). Key from ANTHROPIC_API_KEY or option.WithAPIKey(keyFromKeychain).
package claude

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"erickaldama-mail/internal/aiassist"
)

const Model = "claude-opus-4-8"

type Provider struct{ client anthropic.Client }

func New(apiKey string) *Provider {
	var opts []option.RequestOption
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	return &Provider{client: anthropic.NewClient(opts...)} // falls back to ANTHROPIC_API_KEY env
}

func (p *Provider) Name() string { return "claude:" + Model }

// toAnthropicTools maps neutral ToolSpec → Anthropic. ToolInputSchemaParam already injects type:"object" and
// has a separate Required field — so we extract the INNER "properties"/"required" from ToolSpec.Parameters,
// NOT the whole schema object (audit bug #1: passing the full schema nests it under a literal "properties" key).
func toAnthropicTools(specs []aiassist.ToolSpec) []anthropic.ToolUnionParam {
	var out []anthropic.ToolUnionParam
	for _, s := range specs {
		var props any = map[string]any{}
		if p, ok := s.Parameters["properties"]; ok {
			props = p
		}
		// "required" may be []string (Go literal, e.g. ReadOnlyTools) or []any (JSON round-trip from a config
		// file or network). Handle both — a bare .([]string) silently drops required fields when the spec
		// came through json.Unmarshal, registering the tool with no required args (audit H-1).
		var required []string
		switch rv := s.Parameters["required"].(type) {
		case []string:
			required = rv
		case []any:
			for _, v := range rv {
				if str, ok := v.(string); ok {
					required = append(required, str)
				}
			}
		}
		tp := anthropic.ToolParam{
			Name:        s.Name,
			Description: anthropic.String(s.Description),
			InputSchema: anthropic.ToolInputSchemaParam{Properties: props, Required: required},
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &tp})
	}
	return out
}

func (p *Provider) Chat(ctx context.Context, msgs []aiassist.Message, tools []aiassist.ToolSpec) (aiassist.Response, error) {
	var amsgs []anthropic.MessageParam
	for _, m := range msgs {
		switch m.Role {
		case "user":
			amsgs = append(amsgs, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		case "assistant":
			var blocks []anthropic.ContentBlockParamUnion
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, tc.Args, tc.Name))
			}
			amsgs = append(amsgs, anthropic.NewAssistantMessage(blocks...))
		case "tool":
			amsgs = append(amsgs, anthropic.NewUserMessage(anthropic.NewToolResultBlock(m.ToolID, m.Content, false)))
		}
	}
	params := anthropic.MessageNewParams{
		Model:     Model,
		MaxTokens: 2048,
		Messages:  amsgs,
		Thinking:  anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{}},
	}
	if len(tools) > 0 {
		params.Tools = toAnthropicTools(tools)
	}
	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return aiassist.Response{}, err
	}
	// Stop carries StopReason verbatim ("tool_use" → loop continues; "end_turn"/"refusal"/"max_tokens" → ends).
	out := aiassist.Response{Stop: string(resp.StopReason)}
	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			out.Text += b.Text
		case anthropic.ToolUseBlock:
			args := map[string]any{}
			if len(b.Input) > 0 {
				if err := json.Unmarshal(b.Input, &args); err != nil { // b.Input is json.RawMessage — MUST unmarshal (audit bug #2)
					return aiassist.Response{}, fmt.Errorf("unmarshal tool input for %s: %w", b.Name, err)
				}
			}
			out.ToolCalls = append(out.ToolCalls, aiassist.ToolCall{ID: b.ID, Name: b.Name, Args: args})
		}
	}
	return out, nil
}
