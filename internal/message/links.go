package message

import (
	"strconv"
	"strings"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/PuerkitoBio/goquery"
	"github.com/charmbracelet/x/ansi"
)

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

// stripSentinels removes any PUA sentinel code points from s (LNK-SEC-3). Called on link text before
// we wrap it in our own sentinels, so a hostile email cannot plant a fake sentinel that the Fase B
// materializer would misread into an injected hyperlink.
func stripSentinels(s string) string {
	return strings.NewReplacer(sentinelOpen, "", sentinelSep, "", sentinelClose, "").Replace(s)
}

// newLinkRule returns a htmltomd Rule over <a>. AddRules appends and selecToMD iterates rules in
// reverse (markdown.go:185), so this custom rule runs before the native <a> rule and, with skip=false,
// annuls it. We read the href straight from the DOM (selec.Attr) — never re-parsing, never decoding.
func newLinkRule(acc *linkAccumulator) htmltomd.Rule {
	return htmltomd.Rule{
		Filter: []string{"a"},
		AdvancedReplacement: func(content string, selec *goquery.Selection, options *htmltomd.Options) (htmltomd.AdvancedResult, bool) {
			href, _ := selec.Attr("href")   // percent-encoded as bluemonday left it (LNK-SEC-2: never decode)
			text := stripSentinels(content) // LNK-SEC-3: neutralize forged sentinels in the text

			if !classifyHref(href) {
				// LNK-1 / LNK-5: not clickable → emit plain text, no sentinel, no footer entry.
				return htmltomd.AdvancedResult{Markdown: text}, false
			}
			n, ok := acc.add(href)
			if !ok {
				// LNK-6: past the cap → plain text (no sentinel), the footer notes the overflow count.
				return htmltomd.AdvancedResult{Markdown: text}, false
			}
			// Emit the body sentinel: U+E000 {n} U+E001 {text (n)} U+E002. The visible marker " (n)"
			// goes INSIDE the sentinel text so glamour counts it in the word-wrap (AUDIT FIX H2/LNK-4);
			// Fase B only wraps this in OSC 8, never appends width post-glamour.
			// Footer left empty on purpose (LNK-2): htmltomd's Footer accumulates without dedup; the
			// footer is built from acc in Fase B.
			marked := text + " (" + strconv.Itoa(n) + ")"
			md := sentinelOpen + strconv.Itoa(n) + sentinelSep + marked + sentinelClose
			return htmltomd.AdvancedResult{Markdown: md}, false
		},
	}
}

// hasControlBytes reports whether s contains any control byte unsafe inside an OSC 8 URI:
// C0 (< 0x20, incl. ESC 0x1b, BEL 0x07), DEL 0x7f, and C1 (0x80-0x9f, incl. CSI 0x9b, ST 0x9c).
// ansi.SetHyperlink does NOT escape the URI (hyperlink.go), so this guard is mandatory
// defense-in-depth (LNK-SEC-1). Note: the audit verified bluemonday already strips C0/DEL (drops the
// whole <a>) and percent-encodes C1, so in practice this never fires — but a future bluemonday bump
// could change that, and the guard is cheap. Iterates over runes so a legitimate multibyte rune whose
// continuation bytes are >0x7f is never misflagged (we check the decoded rune, not raw bytes).
func hasControlBytes(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return true
		}
	}
	return false
}

// materializeSentinels scans rendered ANSI for U+E000 {n} U+E001 {text} U+E002 tokens and wraps the
// text in an OSC 8 hyperlink. Two verified design points (proved by running the pipeline before F4):
//   - The visible marker " (n)" is INSIDE {text} (emitted in Fase A), and {text} is left BARE so
//     glamour word-wraps it to width. This keeps body lines within width (AUDIT FIX H2, verified:
//     maxWidth=28 at width 30). The OPEN{n}SEP prefix is the only opaque part; the wrappable text
//     lives after SEP.
//   - The text is wrapped in ONE OSC 8 span AS-IS, including any "\n  " that glamour inserted for
//     word-wrap. A multi-line OSC 8 hyperlink is valid and standard; do NOT collapse the internal
//     newlines — doing so re-joins glamour's wrapped lines into one 105-col line and reintroduces the
//     LNK-4 overflow (verified: collapsing pushed maxWidth back to 105). This is why the earlier
//     "collapseInternalWrap" idea (audit H3) is REJECTED — it was actively wrong.
//
// Guards (all degrade to plain text, never panic):
//   - LNK-SEC-4: index n must exist in acc; otherwise emit text (no hyperlink).
//   - LNK-SEC-1: URL must not contain control bytes; otherwise emit text without OSC 8.
func materializeSentinels(rendered string, acc *linkAccumulator) string {
	var b strings.Builder
	links := acc.links()
	rest := rendered
	for {
		start := strings.Index(rest, sentinelOpen)
		if start < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:start])
		rest = rest[start+len(sentinelOpen):]

		sep := strings.Index(rest, sentinelSep)
		end := strings.Index(rest, sentinelClose)
		if sep < 0 || end < 0 || sep > end {
			// Malformed token (forged partial sentinel): drop the open marker, keep scanning the rest.
			continue
		}
		nStr := rest[:sep]
		text := rest[sep+len(sentinelSep) : end]
		rest = rest[end+len(sentinelClose):]

		n, err := strconv.Atoi(nStr)
		if err != nil || n < 1 || n > len(links) {
			b.WriteString(text) // LNK-SEC-4: forged/OOB index → plain text (already carries " (n)")
			continue
		}
		url := links[n-1]
		if hasControlBytes(url) {
			b.WriteString(text) // LNK-SEC-1: degrade, no OSC 8 (text already carries " (n)")
			continue
		}
		// Wrap the bare (glamour-wrapped) text AS-IS; a multi-line OSC 8 span is valid. See the
		// function doc: collapsing the internal newlines would reintroduce LNK-4 overflow.
		b.WriteString(ansi.SetHyperlink(url) + text + ansi.ResetHyperlink())
	}
	return b.String()
}
