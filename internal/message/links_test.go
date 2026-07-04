package message

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestClassifyHref(t *testing.T) {
	cases := map[string]bool{
		"http://example.com/a":  true,
		"https://example.com/b": true,
		"HTTP://EXAMPLE.COM":    true, // case-insensitive
		"HtTpS://example.org":   true,
		"mailto:x@example.com":  false,
		"//cdn.tracker.test/px": false, // protocol-relative = tracking vector
		"/relative/path":        false,
		"#anchor":               false,
		"":                      false,
		"ftp://example.com":     false,
		"tel:+15551234":         false,
	}
	for href, want := range cases {
		if got := classifyHref(href); got != want {
			t.Errorf("classifyHref(%q) = %v, want %v", href, got, want)
		}
	}
}

func TestLinkAccumulatorDedup(t *testing.T) {
	a := &linkAccumulator{}
	n1, ok1 := a.add("https://example.com/a")
	n2, ok2 := a.add("https://example.com/b")
	n3, ok3 := a.add("https://example.com/a") // repeat → same n as n1
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("adds within cap should be ok: %v %v %v", ok1, ok2, ok3)
	}
	if n1 != 1 || n2 != 2 {
		t.Errorf("first two links should be 1,2; got %d,%d", n1, n2)
	}
	if n3 != n1 {
		t.Errorf("repeated href should reuse n=%d; got %d", n1, n3)
	}
	links := a.links()
	if len(links) != 2 {
		t.Fatalf("dedup: want 2 unique links, got %d: %v", len(links), links)
	}
	if links[0] != "https://example.com/a" || links[1] != "https://example.com/b" {
		t.Errorf("links out of insertion order: %v", links)
	}
}

// convertWithRule runs htmltomd with our custom link rule and returns the body markdown + accumulator.
func convertWithRule(t *testing.T, html string) (string, *linkAccumulator) {
	t.Helper()
	acc := &linkAccumulator{}
	conv := htmltomd.NewConverter("", true, nil)
	conv.AddRules(newLinkRule(acc))
	md, err := conv.ConvertString(html)
	if err != nil {
		t.Fatalf("ConvertString: %v", err)
	}
	return md, acc
}

func TestStripSentinels(t *testing.T) {
	in := "abcd"
	if got := stripSentinels(in); got != "abcd" {
		t.Errorf("stripSentinels = %q, want %q", got, "abcd")
	}
}

func TestLinkRuleEmitsSentinelNoInlineURL(t *testing.T) {
	md, acc := convertWithRule(t, `<p>Ver <a href="https://example.com/very/long/tracking/path">la oferta</a> ya</p>`)
	if strings.Contains(md, "https://example.com") {
		t.Errorf("URL leaked into body markdown: %q", md)
	}
	if !strings.Contains(md, sentinelOpen) || !strings.Contains(md, sentinelClose) {
		t.Errorf("no sentinel in body: %q", md)
	}
	if !strings.Contains(md, "la oferta") {
		t.Errorf("link text lost: %q", md)
	}
	// AUDIT FIX H2: the visible marker "(1)" is emitted INSIDE the sentinel text (pre-glamour).
	if !strings.Contains(md, "la oferta (1)") {
		t.Errorf("marker (1) not inside sentinel text: %q", md)
	}
	if len(acc.links()) != 1 || acc.links()[0] != "https://example.com/very/long/tracking/path" {
		t.Errorf("accumulator wrong: %v", acc.links())
	}
}

func TestLinkRuleNonClickableSchemesPlain(t *testing.T) {
	// mailto + protocol-relative → plain text, NO sentinel, NO accumulator entry (LNK-1, LNK-5)
	md, acc := convertWithRule(t, `<p><a href="mailto:x@example.com">mail me</a> and <a href="//cdn.tracker.test/px">px</a></p>`)
	if strings.Contains(md, sentinelOpen) {
		t.Errorf("non-clickable scheme produced a sentinel: %q", md)
	}
	if len(acc.links()) != 0 {
		t.Errorf("non-clickable schemes entered accumulator: %v", acc.links())
	}
	if !strings.Contains(md, "mail me") || !strings.Contains(md, "px") {
		t.Errorf("link text lost for non-clickable: %q", md)
	}
}

func TestLinkRuleStripsForgedSentinel(t *testing.T) {
	// Attacker plants a raw PUA sentinel in the link text; stripSentinels(content) must remove it
	// before the rule injects its own, so exactly ONE sentinelSep remains (the rule's own).
	forged := "<p><a href=\"https://example.com/a\">safe" + sentinelSep + "text</a></p>"
	md, _ := convertWithRule(t, forged)
	if strings.Count(md, sentinelSep) != 1 {
		t.Errorf("forged sentinel not stripped: %d separators in %q", strings.Count(md, sentinelSep), md)
	}
}

func TestLinkRuleDedupSameHref(t *testing.T) {
	_, acc := convertWithRule(t, `<p><a href="https://example.com/x">one</a> <a href="https://example.com/x">two</a></p>`)
	if len(acc.links()) != 1 {
		t.Errorf("dedup failed: %v", acc.links())
	}
}

func TestLinkAccumulatorCap(t *testing.T) {
	a := &linkAccumulator{}
	for i := 0; i < maxLinks; i++ {
		// distinct hrefs so dedup never collapses them
		if _, ok := a.add("https://example.com/" + strconv.Itoa(i)); !ok {
			t.Fatalf("add %d within cap should be ok", i)
		}
	}
	// the 501st distinct href must be rejected (ok=false)
	if _, ok := a.add("https://example.com/over-the-cap"); ok {
		t.Errorf("add beyond cap should return ok=false")
	}
	if len(a.links()) != maxLinks {
		t.Errorf("cap: want exactly %d links, got %d", maxLinks, len(a.links()))
	}
}

func TestAccumulatorOverflowCount(t *testing.T) {
	a := &linkAccumulator{}
	for i := 0; i < maxLinks+3; i++ {
		a.add("https://example.com/u" + strconv.Itoa(i)) // 503 distinct hrefs
	}
	if a.overflow() != 3 {
		t.Errorf("overflow count = %d, want 3", a.overflow())
	}
}

func TestRenderRichCapNote(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("<p>")
	for i := 0; i < maxLinks+5; i++ {
		sb.WriteString(`<a href="https://example.com/p` + strconv.Itoa(i) + `">L</a> `)
	}
	sb.WriteString("</p>")
	p := &Parsed{TextHTML: sb.String()}
	out, err := RenderRich(p, 80)
	if err != nil {
		t.Fatalf("RenderRich: %v", err)
	}
	if !strings.Contains(out, "(+5 enlaces más)") {
		t.Errorf("cap overflow note missing from footer")
	}
	// exactly maxLinks footer entries (numbered [1]..[500])
	if !strings.Contains(out, "[500] ") {
		t.Errorf("expected entry [500] present")
	}
	if strings.Contains(out, "[501] ") {
		t.Errorf("entry [501] must not exist (capped)")
	}
}

func TestHasControlBytes(t *testing.T) {
	if hasControlBytes("https://example.com/ok") {
		t.Error("clean URL flagged as control bytes")
	}
	// C0, DEL, and C1 (0x9b CSI, 0x9c ST) must all be caught. A legit multibyte rune must NOT be.
	for _, bad := range []string{"a\x07b", "a\x1bb", "a\x00b", "a\x7fb", "a\nb", "a\u009bb", "a\u009cb"} {
		if !hasControlBytes(bad) {
			t.Errorf("control byte not detected in %q", bad)
		}
	}
	if hasControlBytes("café résumé ñ 日本語") { // legit multibyte runes, no control chars
		t.Error("multibyte runes misflagged as control bytes")
	}
}

func TestMaterializeSentinelHappyPath(t *testing.T) {
	acc := &linkAccumulator{}
	acc.add("https://example.com/a") // n=1
	// AUDIT FIX H2: the marker "(1)" is INSIDE the sentinel text (as Fase A emits it).
	body := "Ver " + sentinelOpen + "1" + sentinelSep + "la oferta (1)" + sentinelClose + " ahora"
	out := materializeSentinels(body, acc)
	if strings.Contains(out, sentinelOpen) || strings.Contains(out, sentinelClose) {
		t.Errorf("sentinel survived materialization: %q", out)
	}
	// The materializer wraps text (incl. its "(1)") in OSC 8; it appends NO extra width.
	want := ansi.SetHyperlink("https://example.com/a") + "la oferta (1)" + ansi.ResetHyperlink()
	if !strings.Contains(out, want) {
		t.Errorf("OSC 8 sequence missing.\n got: %q\nwant substr: %q", out, want)
	}
}

func TestMaterializeForgedIndexDegradesToPlain(t *testing.T) {
	// Sentinel with an index that is not in the accumulator (forged / OOB) → plain text, no panic,
	// no hyperlink (LNK-SEC-4).
	acc := &linkAccumulator{} // empty: index 9 does not exist
	body := sentinelOpen + "9" + sentinelSep + "evil (9)" + sentinelClose
	out := materializeSentinels(body, acc)
	if strings.Contains(out, "\x1b]8;") {
		t.Errorf("forged index produced a hyperlink: %q", out)
	}
	if !strings.Contains(out, "evil") {
		t.Errorf("text lost on forged index: %q", out)
	}
}

func TestMaterializeControlByteURLDegrades(t *testing.T) {
	// A URL that somehow carries a control byte must NOT reach SetHyperlink (LNK-SEC-1).
	acc := &linkAccumulator{}
	acc.add("https://example.com/\x07evil") // n=1, control byte in URL
	body := sentinelOpen + "1" + sentinelSep + "text (1)" + sentinelClose
	out := materializeSentinels(body, acc)
	if strings.Contains(out, "\x1b]8;;https://example.com/\x07") {
		t.Errorf("control-byte URL reached OSC 8: %q", out)
	}
	if !strings.Contains(out, "text (1)") {
		t.Errorf("expected degraded 'text (1)', got: %q", out)
	}
}

// The link text stays wrapped by glamour and the OSC 8 span may cross lines. Verify the OSC 8 wraps
// bare wrapped text as-is (no newline collapsing), so a long link text does not overflow width.
func TestMaterializeMultiLineOSC8(t *testing.T) {
	acc := &linkAccumulator{}
	acc.add("https://example.com/a") // n=1
	// text as glamour would leave it: wrapped with an internal newline + margin, marker inside.
	body := sentinelOpen + "1" + sentinelSep + "oferta especial\n  para ti (1)" + sentinelClose
	out := materializeSentinels(body, acc)
	// The internal newline survives (multi-line hyperlink), NOT collapsed to a space.
	if !strings.Contains(out, "oferta especial\n  para ti (1)") {
		t.Errorf("materializer must preserve glamour's internal wrap verbatim: %q", out)
	}
	// OSC 8 open before the text, reset after.
	if !strings.HasPrefix(strings.TrimPrefix(out, ""), ansi.SetHyperlink("https://example.com/a")[:6]) &&
		!strings.Contains(out, ansi.SetHyperlink("https://example.com/a")) {
		t.Errorf("OSC 8 open missing: %q", out)
	}
}

// LNK-A3-E1 regression: forged OPEN markers must NOT trigger O(n²) — 50k forged OPENs return fast.
func TestMaterializeForgedOpensLinear(t *testing.T) {
	acc := &linkAccumulator{}
	acc.add("https://example.com/a")
	body := strings.Repeat(sentinelOpen+"x ", 50000) // no SEP/CLOSE → all malformed
	done := make(chan string, 1)
	go func() { done <- materializeSentinels(body, acc) }()
	select {
	case out := <-done:
		// The forged OPENs are consumed; no hyperlink, no panic.
		if strings.Contains(out, "\x1b]8;") {
			t.Errorf("forged OPENs produced a hyperlink")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("materializeSentinels did not return within 2s on 50k forged OPENs (O(n²) regression)")
	}
}

// LNK-A3-R1: a non-canonical index in a forged sentinel must NOT alias a real link.
func TestMaterializeNonCanonicalIndexNoAlias(t *testing.T) {
	acc := &linkAccumulator{}
	acc.add("https://example.com/real") // n=1
	for _, forged := range []string{"+1", "01", "007"} {
		body := sentinelOpen + forged + sentinelSep + "text (" + forged + ")" + sentinelClose
		out := materializeSentinels(body, acc)
		if strings.Contains(out, "\x1b]8;") {
			t.Errorf("non-canonical index %q produced a hyperlink (forgery alias): %q", forged, out)
		}
		if !strings.Contains(out, "text") {
			t.Errorf("text lost for non-canonical index %q: %q", forged, out)
		}
	}
}

func TestChunkRunes(t *testing.T) {
	// 250-rune token, width 80 → 4 chunks (80+80+80+10), none exceeding 80 runes.
	s := strings.Repeat("a", 250)
	chunks := chunkRunes(s, 80)
	if len(chunks) != 4 {
		t.Fatalf("want 4 chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len([]rune(c)) > 80 {
			t.Errorf("chunk %d exceeds width: %d runes", i, len([]rune(c)))
		}
	}
	if strings.Join(chunks, "") != s {
		t.Error("chunkRunes lost data")
	}
}

func TestBuildFooterEmpty(t *testing.T) {
	acc := &linkAccumulator{}
	if got := buildFooter(acc, 0, 80); got != "" {
		t.Errorf("empty accumulator must yield no footer, got %q", got)
	}
}

func TestBuildFooterEntries(t *testing.T) {
	acc := &linkAccumulator{}
	acc.add("https://example.com/first")
	acc.add("https://example.org/second")
	out := buildFooter(acc, 0, 80)
	if !strings.Contains(out, "─── Enlaces ───") {
		t.Errorf("footer header missing: %q", out)
	}
	if !strings.Contains(out, "[1] https://example.com/first") {
		t.Errorf("entry 1 missing: %q", out)
	}
	if !strings.Contains(out, "[2] https://example.org/second") {
		t.Errorf("entry 2 missing: %q", out)
	}
	// footer carries no OSC 8 — it is the universal fallback (plain URLs, copyable)
	if strings.Contains(out, "\x1b]8;") {
		t.Errorf("footer must not contain OSC 8: %q", out)
	}
}

func TestBuildFooterOverflowNote(t *testing.T) {
	acc := &linkAccumulator{}
	acc.add("https://example.com/a")
	out := buildFooter(acc, 7, 80) // 7 links dropped past the cap
	if !strings.Contains(out, "(+7 enlaces más)") {
		t.Errorf("overflow note missing: %q", out)
	}
}

func TestBuildFooterHardSplitsLongURL(t *testing.T) {
	acc := &linkAccumulator{}
	long := "https://example.com/" + strings.Repeat("x", 300)
	acc.add(long)
	out := buildFooter(acc, 0, 80)
	for _, line := range strings.Split(out, "\n") {
		if ansi.StringWidth(line) > 80 {
			t.Errorf("footer line exceeds width 80: %d cols — %q", ansi.StringWidth(line), line)
		}
	}
}

func TestPreWrapBreaksLongToken(t *testing.T) {
	long := strings.Repeat("z", 200) // no spaces
	out := preWrapLongTokens("prefix "+long+" suffix", 80)
	// every whitespace-delimited token must now fit width
	for _, tok := range strings.Fields(out) {
		if len([]rune(tok)) > 80 {
			t.Errorf("token still exceeds width: %d runes", len([]rune(tok)))
		}
	}
	if strings.Count(out, "z") != 200 {
		t.Errorf("preWrap lost data: %d z's", strings.Count(out, "z"))
	}
}

// AUDIT FIX H1: multi-paragraph markdown structure MUST survive even when a long token is present.
func TestPreWrapPreservesStructure(t *testing.T) {
	md := "# Titulo\n\nPrimer parrafo.\n\nSegundo con " + strings.Repeat("z", 100) + " largo.\n\n- item uno\n- item dos\n"
	before := strings.Count(md, "\n")
	out := preWrapLongTokens(md, 80)
	after := strings.Count(out, "\n")
	if after < before {
		t.Fatalf("preWrap collapsed structure: newlines %d → %d\nout: %q", before, after, out)
	}
	// heading and list markers still on their own lines
	if !strings.Contains(out, "# Titulo\n") {
		t.Errorf("heading fused into another line: %q", out)
	}
	if !strings.Contains(out, "- item uno") {
		t.Errorf("list item lost: %q", out)
	}
	if strings.Count(out, "z") != 100 {
		t.Errorf("preWrap lost data: %d z's", strings.Count(out, "z"))
	}
}

func TestPreWrapPreservesSentinel(t *testing.T) {
	// A sentinel token must pass through untouched even if the text inside is long.
	in := sentinelOpen + "1" + sentinelSep + strings.Repeat("w", 200) + sentinelClose
	out := preWrapLongTokens(in, 80)
	if !strings.Contains(out, sentinelOpen+"1"+sentinelSep) {
		t.Errorf("sentinel corrupted by pre-wrap: %q", out)
	}
	if !strings.Contains(out, sentinelClose) {
		t.Errorf("sentinel close lost: %q", out)
	}
}

func TestPreWrapShortTokensUntouched(t *testing.T) {
	in := "the quick brown fox\n\nsecond line"
	if out := preWrapLongTokens(in, 80); out != in {
		t.Errorf("short tokens changed: %q → %q", in, out)
	}
}

func TestRenderRichE2ELinkNoInlineURLHasFooter(t *testing.T) {
	longURL := "https://example.com/CL0/" + strings.Repeat("track", 40) // ~200+ chars
	p := &Parsed{TextHTML: `<p>Mira <a href="` + longURL + `">la oferta especial</a> por tiempo limitado.</p>`}
	out, err := RenderRich(p, 80)
	if err != nil {
		t.Fatalf("RenderRich: %v", err)
	}
	// LNK-3: the URL is NOT inline in the body render (only in the OSC 8 escape + footer).
	// Body region = everything before the footer header.
	footerAt := strings.Index(out, footerHeader)
	if footerAt < 0 {
		t.Fatal("footer missing from render")
	}
	// LNK-4: no BODY line exceeds width 80 visible cols.
	body := out[:footerAt]
	for _, line := range strings.Split(body, "\n") {
		if ansi.StringWidth(line) > 80 {
			t.Errorf("body line exceeds width 80: %d cols — %q", ansi.StringWidth(line), line)
		}
	}
	// LNK-2: footer carries the full URL.
	if !strings.Contains(out, longURL[:40]) {
		t.Errorf("footer does not carry the URL")
	}
	// The clickable text is present with its marker.
	if !strings.Contains(out, "la oferta especial") {
		t.Error("link text lost")
	}
	if !strings.Contains(out, "(1)") {
		t.Error("link marker (1) missing")
	}
}

func TestRenderRichNoLinksNoFooter(t *testing.T) {
	p := &Parsed{TextHTML: "<p>Hola mundo sin enlaces</p>"}
	out, err := RenderRich(p, 80)
	if err != nil {
		t.Fatalf("RenderRich: %v", err)
	}
	if strings.Contains(out, footerHeader) {
		t.Errorf("footer emitted with no links: %q", out)
	}
}

// AUDIT FIX H2: at narrow reader-pane widths (30/45) a link whose text fills the line must NOT
// overflow width — the " (n)" marker is counted by glamour because it lives inside the sentinel text.
func TestRenderRichE2ENarrowWidthNoOverflow(t *testing.T) {
	longText := "oferta especial de la semana con envio totalmente gratis para ti"
	p := &Parsed{TextHTML: `<p>Texto previo <a href="https://example.com/track">` + longText + `</a> fin.</p>`}
	for _, w := range []int{30, 45} {
		out, err := RenderRich(p, w)
		if err != nil {
			t.Fatalf("RenderRich(w=%d): %v", w, err)
		}
		footerAt := strings.Index(out, footerHeader)
		body := out
		if footerAt >= 0 {
			body = out[:footerAt]
		}
		for _, line := range strings.Split(body, "\n") {
			if ansi.StringWidth(line) > w {
				t.Errorf("width=%d: body line exceeds width: %d cols — %q", w, ansi.StringWidth(line), line)
			}
		}
	}
}

// AUDIT FIX SEC-CRIT: a forged sentinel planted via HTML entity in body text OUTSIDE any <a>, with a
// VALID index (a real link exists), must NOT produce a spoofed OSC 8 hyperlink around the fake URL.
func TestRenderRichSentinelForgeViaEntityNoSpoof(t *testing.T) {
	// One legit link (index 1 exists) + a forged sentinel via entity pointing text at a fake URL.
	attack := `<p><a href="https://example.com/real">legit</a>. ` +
		`Verify at &#xE000;1&#xE001;https://your-bank.example/login&#xE002; now.</p>`
	p := &Parsed{TextHTML: attack}
	out, err := RenderRich(p, 80)
	if err != nil {
		t.Fatalf("RenderRich: %v", err)
	}
	// The fake bank URL must never become the visible text of an OSC 8 hyperlink.
	spoof := ansi.SetHyperlink("https://example.com/real") + "https://your-bank.example/login"
	if strings.Contains(out, spoof) {
		t.Fatal("SEC-CRIT: forged-entity sentinel produced a spoofed hyperlink")
	}
	// It also must not appear in the footer as a legit destination.
	if strings.Contains(out, "your-bank.example") && strings.Contains(out, footerHeader) &&
		strings.Contains(out[strings.Index(out, footerHeader):], "your-bank.example") {
		t.Fatal("SEC-CRIT: forged URL leaked into the footer")
	}
}

// AUDIT FIX H-4 (LNK-8 idempotency): materializing an already-materialized render injects no new
// OSC 8 (no sentinels remain to act on).
func TestRenderRichMaterializeIdempotent(t *testing.T) {
	p := &Parsed{TextHTML: `<p><a href="https://example.com/x">clic</a></p>`}
	out, err := RenderRich(p, 80)
	if err != nil {
		t.Fatalf("RenderRich: %v", err)
	}
	// A second materialize pass over the finished ANSI must be a no-op (no sentinels left).
	acc2 := &linkAccumulator{}
	again := materializeSentinels(out, acc2)
	if again != out {
		t.Errorf("materializeSentinels not idempotent on rendered output")
	}
}

func TestSanitizeTerminalOutputPreservesLegit(t *testing.T) {
	color := "\x1b[31mrojo\x1b[0m"
	if got := sanitizeTerminalOutput(color); got != color {
		t.Errorf("SGR color not preserved: %q", got)
	}
	osc8 := "\x1b]8;;https://example.com/x\x07click\x1b]8;;\x07"
	if got := sanitizeTerminalOutput(osc8); got != osc8 {
		t.Errorf("OSC 8 not preserved: %q", got)
	}
	uni := "café 日本語 ñ\ntab\there"
	if got := sanitizeTerminalOutput(uni); got != uni {
		t.Errorf("unicode/newline/tab not preserved: %q", got)
	}
}

func TestSanitizeTerminalOutputDropsInjection(t *testing.T) {
	// The dangerous input byte must be gone at the BYTE level. Note: an invalid-UTF-8 byte like
	// 0x9b becomes U+FFFD after []rune() inside the sanitizer, so it never survives as a raw byte —
	// but strings.ContainsAny is UTF-8-aware and would false-positive on the U+FFFD; use IndexByte.
	for _, c := range []struct {
		name    string
		in      string
		mustNot byte
	}{
		{"loose-ESC", "cl\x1bick", 0x1b},
		{"C1-CSI", "p\x9bq", 0x9b},
		{"C1-ST", "a\x9cb", 0x9c},
		{"DEL", "x\x7fy", 0x7f},
		{"BEL", "a\x07b", 0x07},
		{"trailing-ESC", "abc\x1b", 0x1b},
	} {
		out := sanitizeTerminalOutput(c.in)
		if strings.IndexByte(out, c.mustNot) >= 0 {
			t.Errorf("%s: injection byte 0x%02x survived: %q", c.name, c.mustNot, out)
		}
	}
}

// End-to-end: a raw ESC in <a> link text must NOT reach the terminal inside the OSC 8 span (S1).
func TestRenderRichNoAnsiInjectionInLinkText(t *testing.T) {
	san, _ := SanitizeHTML("<p><a href=\"https://example.com/\">cl\x1bick here</a></p>", false)
	out, err := RenderRich(&Parsed{TextHTML: san.HTML}, 80)
	if err != nil {
		t.Fatalf("RenderRich: %v", err)
	}
	// The only ESC bytes in the output must be part of SGR (\x1b[) or OSC (\x1b]) — never a lone ESC.
	rs := []rune(out)
	for i, r := range rs {
		if r == 0x1b {
			if i+1 >= len(rs) || (rs[i+1] != '[' && rs[i+1] != ']') {
				t.Errorf("lone ESC (injection) survived at rune %d: %q", i, out)
			}
		}
	}
}

// End-to-end: a raw ESC in PLAIN body text (no link) must also not reach the terminal (the class).
func TestRenderRichNoAnsiInjectionInPlainText(t *testing.T) {
	san, _ := SanitizeHTML("<p>plain te\x1bxt no link</p>", false)
	out, err := RenderRich(&Parsed{TextHTML: san.HTML}, 80)
	if err != nil {
		t.Fatalf("RenderRich: %v", err)
	}
	rs := []rune(out)
	for i, r := range rs {
		if r == 0x1b && (i+1 >= len(rs) || (rs[i+1] != '[' && rs[i+1] != ']')) {
			t.Errorf("lone ESC survived in plain-text render: %q", out)
		}
	}
}

// TestFixtureMarketingLinks exercises the full Parse→Sanitize→RenderRich path on a realistic email.
func TestFixtureMarketingLinks(t *testing.T) {
	raw, err := os.ReadFile("testdata/marketing-links.eml")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	p, err := Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	san, err := SanitizeHTML(p.TextHTML, false)
	if err != nil {
		t.Fatalf("SanitizeHTML: %v", err)
	}
	clean := &Parsed{TextHTML: san.HTML, TextPlain: p.TextPlain}
	out, err := RenderRich(clean, 80)
	if err != nil {
		t.Fatalf("RenderRich: %v", err)
	}

	// Clickable http/https footer entries in appearance order (dedup collapses the repeated one):
	//   [1] the tracking URL (appears twice, same href → one entry)
	//   [2] the img-wrapped link
	//   [3] the HTTP:// uppercase link (classifyHref is case-insensitive)
	if !strings.Contains(out, "[1] https://example.com/CL0/") {
		t.Errorf("footer entry [1] missing:\n%s", out)
	}
	// Full URL survives to the footer (chunkRunes splits for display but must not drop the tail).
	fullURL := "https://example.com/CL0/aHR0cHM6Ly9zaG9wL3RyYWNraW5nL3Zlcnlsb25ncGF0aC93aXRoL21hbnkvc2VnbWVudHM"
	// The footer hard-splits long URLs across lines; strip newlines+indent to check the URL is intact.
	footerFlat := strings.ReplaceAll(strings.ReplaceAll(out, "\n", ""), " ", "")
	if !strings.Contains(footerFlat, fullURL) {
		t.Errorf("full tracking URL not intact in footer (truncated?): want %q", fullURL)
	}
	// Dedup: the repeated href yields exactly ONE footer entry and ONE header.
	if strings.Count(out, footerHeader) != 1 {
		t.Errorf("expected exactly one footer header, got %d", strings.Count(out, footerHeader))
	}
	if !strings.Contains(out, "[2] https://example.org/promo/banner") {
		t.Errorf("footer entry [2] (img link) missing:\n%s", out)
	}
	// bluemonday lowercases the URL SCHEME (verified: HTTP://→http://) but preserves host case.
	if !strings.Contains(out, "[3] http://EXAMPLE.ORG/upper") {
		t.Errorf("footer entry [3] (scheme-lowercased HTTP) missing:\n%s", out)
	}
	// Exactly 3 clickable links → no [4].
	if strings.Contains(out, "[4] ") {
		t.Errorf("unexpected footer entry [4] — a non-clickable scheme leaked in:\n%s", out)
	}
	// AUDIT FIX H-3/H4: <a href><img></a> — the destination is in the footer ([2]); the clickable body
	// text is glamour's image render, which exposes the cid:. Documented, asserted behavior (not an
	// accident): the cid: appears as link text. If this is ever changed to use alt-text, update here.
	if !strings.Contains(out, "cid:banner@test") {
		t.Errorf("expected <a><img></a> body to render the image markdown (cid:) as link text:\n%s", out)
	}
	// mailto text present but NOT clickable / not in footer (LNK-1, LNK-5).
	if !strings.Contains(out, "soporte") {
		t.Errorf("mailto text lost")
	}
	// LNK-5: #anchor, relative and empty hrefs → plain text, never in footer.
	if !strings.Contains(out, "sección de ofertas") || !strings.Contains(out, "promociones") {
		t.Errorf("relative/#anchor link text lost (LNK-5):\n%s", out)
	}
	if strings.Contains(out, "[") && strings.Contains(out, "] #seccion-ofertas") {
		t.Errorf("#anchor wrongly entered footer (LNK-5):\n%s", out)
	}
	// protocol-relative tracker URL must NOT appear anywhere (not clickable, not in footer).
	if strings.Contains(out, "cdn.tracker.test") {
		t.Errorf("protocol-relative tracker leaked into render:\n%s", out)
	}
}

// --- LNK-SEC mutation tests: each FAILS if its guard is reverted (verified by mutation experiment). ---
// Covers: LNK-SEC-1 (control-byte), LNK-SEC-2 (no URL-decode), LNK-SEC-3 (forged-sentinel strip, both
// the in-rule local strip and the global entity strip), LNK-SEC-4 (index range), LNK-6 (cap).
// The C1 (0x9b/0x9c) guard, the non-canonical-index guard (+1/01), and sanitizeTerminalOutput are
// covered by TestHasControlBytes, TestMaterializeNonCanonicalIndexNoAlias, and
// TestSanitizeTerminalOutput* respectively (elsewhere in this file).

// LNK-SEC-1: a control byte in the URL must degrade to plain text, never reach OSC 8.
func TestMutationControlByteInURL(t *testing.T) {
	acc := &linkAccumulator{}
	acc.add("https://example.com/\x1bevil")
	body := sentinelOpen + "1" + sentinelSep + "click (1)" + sentinelClose
	out := materializeSentinels(body, acc)
	if strings.Contains(out, "\x1b]8;;https://example.com/\x1b") {
		t.Fatal("MUTATION: control byte reached OSC 8 (LNK-SEC-1 reverted)")
	}
}

// SEC-CRIT: a sentinel forged via HTML entity in body text OUTSIDE any <a>, with a VALID index,
// must NOT produce a spoofed hyperlink (the global strip before htmltomd neutralizes it).
func TestMutationSentinelForgeViaEntity(t *testing.T) {
	attack := `<p><a href="https://example.com/real">legit</a>. ` +
		`Go &#xE000;1&#xE001;https://your-bank.example/login&#xE002; now.</p>`
	out, _ := RenderRich(&Parsed{TextHTML: attack}, 80)
	if strings.Contains(out, ansi.SetHyperlink("https://example.com/real")+"https://your-bank.example/login") {
		t.Fatal("MUTATION: forged-entity sentinel spoofed a hyperlink (SEC-CRIT global strip reverted)")
	}
}

// LNK-SEC-2: the href must be used percent-encoded; a backtick stays %60 (bluemonday encodes it).
func TestMutationNoURLDecode(t *testing.T) {
	// bluemonday percent-encodes backtick to %60 in href; our pipeline must NOT decode it back.
	san, _ := SanitizeHTML("<a href=\"https://example.com/a`b\">t</a>", false)
	clean := &Parsed{TextHTML: san.HTML}
	out, _ := RenderRich(clean, 80)
	if strings.Contains(out, "example.com/a`b") {
		t.Fatal("MUTATION: href was URL-decoded, backtick reappeared (LNK-SEC-2 reverted)")
	}
}

// LNK-SEC-3: a forged sentinel in the attacker's link text must be stripped.
func TestMutationForgedSentinelStripped(t *testing.T) {
	// Plant a forged sentinel triple INSIDE the attacker-controlled link text. LNK-SEC-3's
	// stripSentinels(content) must remove it before the rule wraps the link, leaving exactly ONE
	// sentinelSep (the rule's own legitimate token). If the guard is reverted, the forged sep
	// survives and the count is 2 → this test fails. (Verified: reverting the guard makes this fail.)
	forged := "<p><a href=\"https://example.com/a\">x" + sentinelOpen + "9" + sentinelSep + "evil" + sentinelClose + "y</a></p>"
	md, _ := convertWithRule(t, forged)
	if strings.Count(md, sentinelSep) != 1 {
		t.Fatalf("MUTATION: forged sentinel not stripped (LNK-SEC-3 reverted): %q", md)
	}
}

// LNK-SEC-4: an out-of-range sentinel index must degrade to plain text, never panic/hyperlink.
func TestMutationForgedIndexNoHyperlink(t *testing.T) {
	acc := &linkAccumulator{} // empty
	out := materializeSentinels(sentinelOpen+"42"+sentinelSep+"z (42)"+sentinelClose, acc)
	if strings.Contains(out, "\x1b]8;") {
		t.Fatal("MUTATION: OOB index produced a hyperlink (LNK-SEC-4 reverted)")
	}
}

// LNK-6: the 501st distinct link is not a hyperlink; footer notes the overflow.
func TestMutationCapEnforced(t *testing.T) {
	a := &linkAccumulator{}
	for i := 0; i < maxLinks; i++ {
		a.add("https://example.com/c" + strconv.Itoa(i))
	}
	if _, ok := a.add("https://example.com/over"); ok {
		t.Fatal("MUTATION: cap not enforced, 501st link accepted (LNK-6 reverted)")
	}
}

// LNK-7: OSC 8 sequences must survive viewport.SetContent + a lipgloss border (the TUI reader path).
func TestLNK7ViewportPreservesOSC8(t *testing.T) {
	p := &Parsed{TextHTML: `<p><a href="https://example.com/keep">keep me clickable</a></p>`}
	rich, err := RenderRich(p, 60)
	if err != nil {
		t.Fatalf("RenderRich: %v", err)
	}
	if !strings.Contains(rich, "\x1b]8;;https://example.com/keep\x07") {
		t.Fatalf("OSC 8 missing from RenderRich output")
	}
	vp := viewport.New(60, 20)
	vp.SetContent(rich)
	framed := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Render(vp.View())
	if !strings.Contains(framed, "\x1b]8;;https://example.com/keep\x07") {
		t.Errorf("OSC 8 did not survive viewport+border:\n%q", framed)
	}
}
