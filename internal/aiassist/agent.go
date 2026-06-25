package aiassist

import (
	"context"
	"fmt"

	"erickaldama-mail/internal/mailbox"
)

// Summarize asks for a summary + required action + urgency. One turn, no tools.
func Summarize(ctx context.Context, p LLMProvider, body string) (string, error) {
	resp, err := p.Chat(ctx, []Message{
		{Role: "user", Content: "Resume este correo en 3 líneas: tema, acción requerida, urgencia.\n\n" + body},
	}, nil)
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

// Draft produces a reply draft given the thread context and a short instruction. Never sends.
func Draft(ctx context.Context, p LLMProvider, thread, instruction string) (string, error) {
	resp, err := p.Chat(ctx, []Message{
		{Role: "user", Content: "Redacta SOLO el cuerpo de una respuesta. Instrucción: " + instruction + "\n\nHilo:\n" + thread},
	}, nil)
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

// RunAgent runs the shared agent loop: Chat → if tool_use, exec read-only tools, append results, loop.
// Bounded by maxIters (anti-runaway). Each tool result occupies its own Message{Role:"tool"} in the slice;
// the Anthropic adapter (Task 8) must pack all same-turn tool_result blocks into ONE user message to satisfy
// the API (multiple tool_use in a turn → one user message of tool_result blocks). Tools are read-only — no send.
func RunAgent(ctx context.Context, p LLMProvider, reader *mailbox.Reader, mb, goal string, maxIters int) (string, error) {
	if maxIters <= 0 {
		return "", fmt.Errorf("RunAgent: maxIters must be > 0, got %d", maxIters)
	}
	msgs := []Message{{Role: "user", Content: goal}}
	tools := ReadOnlyTools()
	for i := 0; i < maxIters; i++ {
		resp, err := p.Chat(ctx, msgs, tools)
		if err != nil {
			return "", err
		}
		if resp.Stop != "tool_use" || len(resp.ToolCalls) == 0 {
			return resp.Text, nil
		}
		// assistant turn that requested tools
		msgs = append(msgs, Message{Role: "assistant", ToolCalls: resp.ToolCalls})
		// execute every requested tool, append each result (correlated by ID for Anthropic / Name for Ollama)
		for _, call := range resp.ToolCalls {
			result := execTool(ctx, reader, mb, call)
			msgs = append(msgs, Message{Role: "tool", ToolName: call.Name, ToolID: call.ID, Content: result})
		}
	}
	return "", fmt.Errorf("agent exceeded %d iterations without final answer", maxIters)
}
