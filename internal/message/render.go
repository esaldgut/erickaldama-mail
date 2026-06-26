package message

import (
	htmltomd "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/charmbracelet/glamour"
)

// RenderPlain returns the plain-text body for the CLI (enmime already down-converts HTML→text into TextPlain).
func RenderPlain(p *Parsed) string {
	if p.TextPlain != "" {
		return p.TextPlain
	}
	return p.TextHTML // worst case: raw; CLI is for piping, TUI uses RenderRich
}

// RenderRich converts the HTML body to markdown then renders it to ANSI for the TUI/CLI --rich, wrapped
// to width columns. width<=0 falls back to 80 (glamour's WithWordWrap(0) produces broken output).
// P-1 CRÍTICO: DEBE convertir HTML→markdown con htmltomd ANTES de glamour. Si se pasa HTML crudo a glamour,
// goldmark (Unsafe=false default) lo borra → texto sin formato, SIN error. El test pasaría igual.
func RenderRich(p *Parsed, width int) (string, error) {
	if width <= 0 {
		width = 80
	}
	src := p.TextHTML
	if src == "" {
		return p.TextPlain, nil
	}
	md, err := htmltomd.NewConverter("", true, nil).ConvertString(src) // ← conversión HTML→markdown OBLIGATORIA
	if err != nil {
		return "", err
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", err
	}
	return r.Render(md)
}
