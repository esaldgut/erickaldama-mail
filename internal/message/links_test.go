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
