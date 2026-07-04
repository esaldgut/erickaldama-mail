package message

import (
	"strconv"
	"strings"
	"testing"
	"time"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown"
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
	// Attacker plants raw PUA in the link text — must be stripped before we inject our own (LNK-SEC-3).
	md, _ := convertWithRule(t, "<p><a href=\"https://example.com/a\">safetext</a></p>")
	// The only sentinels present must be OUR wrapping ones; the forged inner triple is gone,
	// so total U+E001 separators == 1 (ours), not 2.
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
