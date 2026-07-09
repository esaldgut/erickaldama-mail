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
	out, err := RenderRich(p, 80)
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

func TestRenderRichRespectsWidth(t *testing.T) {
	p := &Parsed{TextHTML: "<p>" + strings.Repeat("word ", 40) + "</p>"}
	narrow, err := RenderRich(p, 40)
	if err != nil {
		t.Fatalf("RenderRich: %v", err)
	}
	// con width 40, alguna línea del output no debe exceder ~40 cols visibles (heurística: hay más de 1 línea)
	if strings.Count(narrow, "\n") < 2 {
		t.Errorf("expected wrapping at width 40, got few lines")
	}
}

func TestRenderRichWidthZeroFallback(t *testing.T) {
	p := &Parsed{TextHTML: "<p>hello</p>"}
	out, err := RenderRich(p, 0) // width 0 → fallback 80, NO output roto
	if err != nil {
		t.Fatalf("RenderRich width 0: %v", err)
	}
	if out == "" {
		t.Error("width 0 produced empty output — fallback to 80 failed")
	}
}
