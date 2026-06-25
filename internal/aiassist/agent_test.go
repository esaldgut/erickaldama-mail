package aiassist

import (
	"context"
	"testing"
)

// fakeProvider scripts a tool-call on turn 1, then a final answer on turn 2.
type fakeProvider struct{ turn int }

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Chat(_ context.Context, _ []Message, _ []ToolSpec) (Response, error) {
	f.turn++
	if f.turn == 1 {
		return Response{Stop: "tool_use", ToolCalls: []ToolCall{{ID: "t1", Name: "list_messages", Args: map[string]any{"limit": 1.0}}}}, nil
	}
	return Response{Stop: "end", Text: "Tienes 1 mensaje."}, nil
}

func TestRunAgentExecutesToolThenAnswers(t *testing.T) {
	fp := &fakeProvider{}
	out, err := RunAgent(context.Background(), fp, nil, "test@erickaldama.com", "¿cuántos correos?", 5)
	if err != nil || out != "Tienes 1 mensaje." {
		t.Fatalf("agent: %q err=%v", out, err)
	}
	if fp.turn != 2 {
		t.Fatalf("expected 2 turns, got %d", fp.turn)
	}
}

func TestRunAgentCapsIterations(t *testing.T) {
	// provider that always asks for a tool → must hit the cap, not loop forever.
	loop := providerFunc(func() Response {
		return Response{Stop: "tool_use", ToolCalls: []ToolCall{{ID: "x", Name: "list_messages", Args: map[string]any{}}}}
	})
	_, err := RunAgent(context.Background(), loop, nil, "m", "g", 3)
	if err == nil {
		t.Fatal("expected error on iteration cap")
	}
}

type providerFunc func() Response

func (p providerFunc) Name() string { return "loop" }
func (p providerFunc) Chat(_ context.Context, _ []Message, _ []ToolSpec) (Response, error) {
	return p(), nil
}
