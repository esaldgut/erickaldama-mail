package message

import "strings"

// Sentinel code points (Unicode Private Use Area) that carry a link through glamour untouched.
// glamour/goldmark treat PUA as opaque text (verified: survives even with markdown-special or empty
// content). Materialized into OSC 8 in Fase B after glamour renders.
const (
	sentinelOpen  = "" // U+E000: start of a link token
	sentinelSep   = "" // U+E001: separates the numeric index from the link text
	sentinelClose = "" // U+E002: end of a link token
)

// maxLinks caps how many <a href> http/https links become hyperlinks (LNK-6). A hostile .eml with
// 100k <a> would freeze the synchronous render; beyond the cap links degrade to plain text.
const maxLinks = 500

// classifyHref reports whether href is a clickable http/https URL (LNK-1). Case-insensitive.
// mailto:, protocol-relative (//host), relative paths, #anchors and empty are NOT clickable:
// v0.6 exists to tame http/https tracking URLs; protocol-relative is itself a tracking vector.
func classifyHref(href string) bool {
	lh := strings.ToLower(strings.TrimSpace(href))
	return strings.HasPrefix(lh, "http://") || strings.HasPrefix(lh, "https://")
}

// linkAccumulator collects unique http/https hrefs in first-appearance order and assigns each a
// 1-based index n. It is the single source of truth for the footer (htmltomd's own Footer accumulates
// per-<a> without dedup — verified markdown.go:200-202 does +=, so we never use it).
type linkAccumulator struct {
	order []string       // hrefs in first-appearance order
	index map[string]int // href → 1-based n
}

// add registers href and returns its 1-based index. A repeated href reuses its index. Returns
// ok=false when the cap is reached for a NEW href (LNK-6); a repeat within an already-capped set
// still returns its existing index with ok=true.
func (a *linkAccumulator) add(href string) (n int, ok bool) {
	if a.index == nil {
		a.index = make(map[string]int)
	}
	if n, exists := a.index[href]; exists {
		return n, true
	}
	if len(a.order) >= maxLinks {
		return 0, false
	}
	a.order = append(a.order, href)
	n = len(a.order)
	a.index[href] = n
	return n, true
}

// links returns the unique hrefs in first-appearance order (footer source).
func (a *linkAccumulator) links() []string {
	return a.order
}
