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

// RenderRich converts the HTML body to markdown then renders it to ANSI for the TUI. glamour renders
// MARKDOWN, not HTML — the HTML→markdown step (html-to-markdown) is mandatory and lives here.
func RenderRich(p *Parsed) (string, error) {
	src := p.TextHTML
	if src == "" {
		return p.TextPlain, nil
	}
	md, err := htmltomd.NewConverter("", true, nil).ConvertString(src)
	if err != nil {
		return "", err
	}
	return glamour.Render(md, "auto") // "auto" respects the terminal's background (light/dark), not hardcoded "dark"
}
