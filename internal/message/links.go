package message

import (
	"bytes"
	"strconv"
	"strings"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/PuerkitoBio/goquery"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/net/html"
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
	order   []string       // hrefs in first-appearance order
	index   map[string]int // href → 1-based n
	dropped int            // distinct http/https hrefs rejected by the cap (LNK-6)
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
		a.dropped++
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

// overflow reports how many NEW http/https hrefs were rejected by the cap (LNK-6).
func (a *linkAccumulator) overflow() int {
	return a.dropped
}

// stripSentinels removes any PUA sentinel code points from s (LNK-SEC-3). Called on link text before
// we wrap it in our own sentinels, so a hostile email cannot plant a fake sentinel that the Fase B
// materializer would misread into an injected hyperlink.
func stripSentinels(s string) string {
	return strings.NewReplacer(sentinelOpen, "", sentinelSep, "", sentinelClose, "").Replace(s)
}

// stripSentinelsFromHTML removes any PUA sentinel code points from EVERY text node in rawHTML (AUDIT
// FIX SEC-CRIT, LNK-SEC-3 global). This MUST run on the parsed DOM, not on the raw HTML string: an
// attacker can plant a sentinel via an HTML entity (&#xE000;) which is still literal ASCII text at the
// string level — golang.org/x/net/html's tokenizer only decodes it to the raw PUA rune during parsing.
// A string-level stripSentinels(rawHTML) is therefore a no-op against entity-encoded forgeries; this
// function parses first (forcing the decode) and strips the decoded runes from every text node,
// INCLUDING inside <a> (harmless there — newLinkRule strips its own text again, LNK-SEC-3 local — and
// necessary everywhere else, since a forged sentinel in body text outside any <a> would otherwise
// reach htmltomd untouched by our link rule and be materialized in Fase B into a spoofed OSC 8
// hyperlink aliasing a real link's index). Runs BEFORE htmltomd conversion, so Fase A's own sentinel
// emission (which happens during conversion, after this strip) is never touched.
func stripSentinelsFromHTML(rawHTML string) (string, error) {
	root, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return "", err
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			n.Data = stripSentinels(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	var buf bytes.Buffer
	if err := html.Render(&buf, root); err != nil {
		return "", err
	}
	return buf.String(), nil
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
// Scan order (LNK-A3-E1): after finding sentinelOpen, find sentinelClose NEXT and advance rest
// UNCONDITIONALLY past the CLOSE (not just past the OPEN) before continuing the loop. This
// guarantees strictly linear progress through rendered even when the token is malformed (forged
// OPEN bytes with no SEP/CLOSE), keeping the scan O(n) instead of the O(n²) that resulted from
// only consuming the OPEN marker on the malformed path. sentinelSep is then searched only WITHIN
// the OPEN..CLOSE token, which is also the correct semantic (SEP belongs to this token, not
// wherever it next appears in the remaining text).
//
// Guards (all degrade to plain text, never panic):
//   - LNK-SEC-4 / LNK-A3-R1: index n must exist in acc AND be the canonical decimal form Fase A
//     emits (strconv.Itoa(n) round-trips to nStr); otherwise emit text (no hyperlink). Rejecting
//     non-canonical forms ("+1", "01", "007") prevents a forged sentinel from aliasing a real link
//     the user never marked.
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
		afterOpen := rest[start+len(sentinelOpen):]
		end := strings.Index(afterOpen, sentinelClose)
		if end < 0 {
			// No CLOSE ahead: no token can complete. Emit the rest literally and stop (O(n) total).
			b.WriteString(afterOpen)
			break
		}
		token := afterOpen[:end]
		rest = afterOpen[end+len(sentinelClose):] // advance PAST the CLOSE — guarantees linear progress

		sep := strings.Index(token, sentinelSep)
		if sep < 0 {
			// OPEN..CLOSE with no SEP → malformed; emit the token text literally, no hyperlink.
			b.WriteString(token)
			continue
		}
		nStr := token[:sep]
		text := token[sep+len(sentinelSep):]

		// Finding 2 (LNK-A3-R1): reject non-canonical index forms. Fase A emits only decimal via
		// strconv.Itoa; "+1"/"01"/"007" can ONLY be attacker-forged and would alias a real link the
		// user never marked. Require the parsed int to re-serialize to exactly nStr.
		n, err := strconv.Atoi(nStr)
		if err != nil || n < 1 || n > len(links) || strconv.Itoa(n) != nStr {
			b.WriteString(text) // LNK-SEC-4: forged/OOB/non-canonical index → plain text, no hyperlink
			continue
		}
		url := links[n-1]
		if hasControlBytes(url) {
			b.WriteString(text) // LNK-SEC-1: control-byte URL → plain text, no OSC 8
			continue
		}
		b.WriteString(ansi.SetHyperlink(url) + text + ansi.ResetHyperlink())
	}
	return b.String()
}

// footerHeader is the section title shown before the link list. Spanish, matching the existing UI.
const footerHeader = "─── Enlaces ───"

// chunkRunes breaks s into chunks of at most width runes each, without splitting a rune and without
// relying on spaces. Used for the footer URLs (LNK-2) so a 300-char URL never overflows the width —
// glamour would split it mid-token at 222 cols (verified); we split it deterministically instead.
// Named chunkRunes (not hardSplit) per AUDIT FIX H9 DRY: Task 5 reuses this identical body for the
// body pre-wrap, so it is defined once here under its final name.
func chunkRunes(s string, width int) []string {
	if width < 1 {
		width = 80
	}
	runes := []rune(s)
	if len(runes) <= width {
		return []string{s}
	}
	var chunks []string
	for len(runes) > width {
		chunks = append(chunks, string(runes[:width]))
		runes = runes[width:]
	}
	if len(runes) > 0 {
		chunks = append(chunks, string(runes))
	}
	return chunks
}

// buildFooter renders the link list block, OUTSIDE glamour (LNK-2). Returns "" when there are no
// links (no header, no block). Each unique href gets one "[n] URL" entry in first-appearance order,
// hard-split to width with a hanging indent for continuation lines. overflow>0 appends a note.
func buildFooter(acc *linkAccumulator, overflow int, width int) string {
	links := acc.links()
	if len(links) == 0 {
		return ""
	}
	if width < 1 {
		width = 80
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(footerHeader)
	b.WriteString("\n")
	for i, url := range links {
		prefix := "[" + strconv.Itoa(i+1) + "] "
		indent := strings.Repeat(" ", len([]rune(prefix)))
		// Reserve the prefix/indent width so no line exceeds width.
		avail := width - len([]rune(prefix))
		if avail < 1 {
			avail = 1
		}
		chunks := chunkRunes(url, avail)
		for j, c := range chunks {
			if j == 0 {
				b.WriteString(prefix + c + "\n")
			} else {
				b.WriteString(indent + c + "\n")
			}
		}
	}
	if overflow > 0 {
		b.WriteString("[…] (+" + strconv.Itoa(overflow) + " enlaces más)\n")
	}
	return b.String()
}

// preWrapLongTokens inserts a space break inside any whitespace-delimited token longer than width
// runes, so glamour never overflows the width on an unbreakable body token (Fase C). Operates LINE BY
// LINE (splitting on "\n" and rejoining with "\n") so the markdown structure — headings, blank lines
// between paragraphs, list markers — is preserved (AUDIT FIX H1). Sentinel tokens pass through
// untouched (a break inside a sentinel would corrupt it). Applies to the body only; the footer is
// hard-split separately in Fase B. Preserves all content (never truncates).
func preWrapLongTokens(md string, width int) string {
	if width < 1 {
		width = 80
	}
	lines := strings.Split(md, "\n")
	changed := false
	for li, line := range lines {
		// Only touch a line if it holds a non-sentinel token longer than width.
		if !lineHasLongToken(line, width) {
			continue
		}
		fields := strings.Split(line, " ") // preserve intra-line single spaces exactly
		for i, f := range fields {
			if f == "" || strings.Contains(f, sentinelOpen) {
				continue // empty (from double space) or sentinel: leave as-is
			}
			if len([]rune(f)) <= width {
				continue
			}
			fields[i] = strings.Join(chunkRunes(f, width), " ")
		}
		lines[li] = strings.Join(fields, " ")
		changed = true
	}
	if !changed {
		return md
	}
	return strings.Join(lines, "\n")
}

// lineHasLongToken reports whether line contains a space-delimited, non-sentinel token > width runes.
func lineHasLongToken(line string, width int) bool {
	for _, f := range strings.Split(line, " ") {
		if strings.Contains(f, sentinelOpen) {
			continue
		}
		if len([]rune(f)) > width {
			return true
		}
	}
	return false
}

// sanitizeTerminalOutput removes raw control bytes that email content could use for ANSI injection
// (cursor moves, line/screen erase, style spoofing) while preserving the terminal control sequences
// glamour legitimately emits: 7-bit CSI (SGR color, "\x1b[ ... final-byte") and OSC (hyperlinks,
// "\x1b] ... BEL|ST"). Also preserves printable runes (incl. Unicode), "\n", and "\t".
//
// Dropped: lone C0 control bytes (except \n/\t), DEL (0x7f), C1 controls (0x80-0x9f, incl. 8-bit CSI
// 0x9b / ST 0x9c), and any ESC that does not introduce a valid CSI/OSC sequence. This closes the
// ANSI-injection surface for the whole rendered body (plain text, degraded links, and OSC 8 spans),
// not just link text. Applied once as the final step of RenderRich, after materialization + footer.
//
// Note: invalid UTF-8 bytes (e.g. a raw 0x9b) are decoded to U+FFFD by []rune() before this scan, so
// they render as the harmless replacement character rather than reaching the terminal as a control.
func sanitizeTerminalOutput(s string) string {
	var b strings.Builder
	rs := []rune(s)
	i := 0
	for i < len(rs) {
		r := rs[i]
		if r == 0x1b && i+1 < len(rs) {
			next := rs[i+1]
			if next == '[' { // CSI: keep through the final byte 0x40-0x7e
				j := i + 2
				for j < len(rs) && !(rs[j] >= 0x40 && rs[j] <= 0x7e) {
					j++
				}
				if j < len(rs) { // complete, valid CSI sequence
					b.WriteString(string(rs[i : j+1]))
					i = j + 1
					continue
				}
				i++ // incomplete CSI: drop the ESC, continue
				continue
			}
			if next == ']' { // OSC: keep through BEL (0x07) or ST (ESC \)
				j := i + 2
				for j < len(rs) {
					if rs[j] == 0x07 {
						j++
						break
					}
					if rs[j] == 0x1b && j+1 < len(rs) && rs[j+1] == '\\' {
						j += 2
						break
					}
					j++
				}
				b.WriteString(string(rs[i:j]))
				i = j
				continue
			}
			i++ // ESC not introducing CSI/OSC: injection attempt, drop it
			continue
		}
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == 0x1b:
			// trailing ESC with nothing after → drop
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			// raw dangerous control byte → drop
		default:
			b.WriteRune(r) // printable (incl. Unicode)
		}
		i++
	}
	return b.String()
}
