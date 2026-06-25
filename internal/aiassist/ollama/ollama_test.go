package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"erickaldama-mail/internal/aiassist"
)

func TestChatParsesToolCallObjectArgs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"stream":false`) {
			t.Errorf("request must set stream:false, got %s", body)
		}
		// Ollama: arguments is an OBJECT, tool_calls has no id
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"list_messages","arguments":{"limit":5}}}]},"done":true}`))
	}))
	defer srv.Close()

	p := New("llama3.2", srv.URL)
	resp, err := p.Chat(context.Background(), []aiassist.Message{{Role: "user", Content: "hi"}}, aiassist.ReadOnlyTools())
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "list_messages" {
		t.Fatalf("tool calls: %+v", resp.ToolCalls)
	}
	if v, ok := resp.ToolCalls[0].Args["limit"].(float64); !ok || v != 5 {
		t.Fatalf("arguments must parse as object, got %+v", resp.ToolCalls[0].Args)
	}
	if resp.Stop != "tool_use" {
		t.Fatalf("stop: %q", resp.Stop)
	}
	_ = json.Marshal // keep import if unused elsewhere
}

func TestChatFinalText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"Hola final"},"done":true}`))
	}))
	defer srv.Close()
	p := New("llama3.2", srv.URL)
	resp, err := p.Chat(context.Background(), []aiassist.Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil || resp.Text != "Hola final" || resp.Stop == "tool_use" {
		t.Fatalf("resp: %+v err=%v", resp, err)
	}
}

func TestChatSerializesAssistantToolCalls(t *testing.T) {
	// audit NUEVO-1: an assistant turn carrying ToolCalls must be sent back to Ollama with tool_calls populated;
	// otherwise the multi-turn agent loop ships an empty assistant message and the tool_result loses its trigger.
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"done"}}`))
	}))
	defer srv.Close()
	p := New("llama3.2", srv.URL)
	_, err := p.Chat(context.Background(), []aiassist.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []aiassist.ToolCall{{Name: "list_messages", Args: map[string]any{"limit": 3}}}},
		{Role: "tool", ToolName: "list_messages", Content: "[]"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotBody, `"tool_calls"`) || !strings.Contains(gotBody, `"list_messages"`) {
		t.Fatalf("assistant tool_calls not serialized into request body: %s", gotBody)
	}
}
