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
