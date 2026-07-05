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
// P-1 CRÍTICO: DEBE convertir HTML→markdown con htmltomd ANTES de glamour.
//
// v0.6 link render (3 phases, all downstream of bluemonday):
//
//	Fase A: custom <a> rule emits U+E000..U+E002 sentinels + fills a per-call link accumulator.
//	Fase C: pre-wrap unbreakable body tokens.
//	Fase B: after glamour, materialize sentinels → OSC 8, append the footer built outside glamour.
//
// AUDIT FIX SEC-CRIT (LNK-SEC-3): the sanitized HTML is stripped of any U+E000..U+E002 code points
// BEFORE htmltomd, so the only PUA sentinels in the pipeline are the ones our rule injects. Without
// this, a hostile email can plant a sentinel via an HTML entity (&#xE000;) in body text OUTSIDE any
// <a> — bluemonday/x-net-html decodes it to raw PUA and keeps it — and Fase B would materialize it
// into a spoofed OSC 8 hyperlink (visible text = fake URL, destination = a real link's target).
// Verified: a string-level strip on the raw HTML is a NO-OP against entity-encoded forgeries, because
// the entity is still literal ASCII text at that point — the decode happens inside the HTML parser.
// stripSentinelsFromHTML parses the DOM (forcing the decode) and strips every text node, which is the
// only point that closes the hole; the in-rule stripSentinels (links.go) is defense-in-depth on the
// link text itself.
func RenderRich(p *Parsed, width int) (string, error) {
	if width <= 0 {
		width = 80
	}
	src := p.TextHTML
	if src == "" {
		// AUDIT FIX (external review LNK-S1): a plain-text-only email returns TextPlain, which is
		// attacker-controlled (enmime env.Text). Callers write RenderRich's output straight to the
		// terminal and trust it to be sanitized, so this branch MUST neutralize terminal-injection too.
		// Use stripDangerousControls (the AGGRESSIVE strip), NOT sanitizeTerminalOutput: on this path
		// glamour never ran, so there is NO legitimate ANSI to preserve — every control byte here is
		// attacker-controlled. sanitizeTerminalOutput preserves well-formed OSC 8 / CSI (it cannot tell
		// a forged sequence from glamour's own), so it would let a forged OSC 8 or a raw \x1b[2J through.
		// stripDangerousControls keeps \n/\t/\r and printable Unicode, so legitimate plain text is intact.
		return stripDangerousControls(p.TextPlain), nil
	}
	// AUDIT FIX SEC-CRIT: neutralize any attacker-planted sentinel (incl. HTML-entity-encoded) BEFORE
	// the link rule runs. Must operate on the parsed DOM — see stripSentinelsFromHTML doc.
	src, err := stripSentinelsFromHTML(src)
	if err != nil {
		return "", err
	}

	// Fase A: convert with the custom link rule; acc is per-invocation (never shared across emails).
	acc := &linkAccumulator{}
	conv := htmltomd.NewConverter("", true, nil)
	conv.AddRules(newLinkRule(acc))
	md, err := conv.ConvertString(src) // ← conversión HTML→markdown OBLIGATORIA (P-1)
	if err != nil {
		return "", err
	}

	// Fase C: break residual unbreakable tokens in the body before glamour wraps.
	md = preWrapLongTokens(md, width)

	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", err
	}
	rendered, err := r.Render(md)
	if err != nil {
		return "", err
	}

	// Fase B: sentinels → OSC 8, then append the footer (built outside glamour).
	rendered = materializeSentinels(rendered, acc)
	rendered += buildFooter(acc, acc.overflow(), width)
	return sanitizeTerminalOutput(rendered), nil
}
