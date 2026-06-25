package message

import (
	"strings"
	"testing"
)

func TestRenderPlainUsesText(t *testing.T) {
	p := &Parsed{TextPlain: "hola plano", TextHTML: "<b>x</b>"}
	if RenderPlain(p) != "hola plano" {
		t.Fatalf("plain render: %q", RenderPlain(p))
	}
}

func TestRenderRichConvertsHTML(t *testing.T) {
	p := &Parsed{TextHTML: "<p>Hola <b>mundo</b></p>"}
	out, err := RenderRich(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "<p>") || strings.Contains(out, "<b>") {
		t.Fatalf("rich render still has raw HTML tags: %q", out)
	}
	if out == "" {
		t.Fatal("rich render empty")
	}
}
