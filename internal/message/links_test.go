package message

import (
	"strconv"
	"strings"
	"testing"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown"
)

func TestClassifyHref(t *testing.T) {
	cases := map[string]bool{
		"http://example.com/a":  true,
		"https://example.com/b": true,
		"HTTP://EXAMPLE.COM":     true, // case-insensitive
		"HtTpS://example.org":    true,
		"mailto:x@example.com":   false,
		"//cdn.tracker.test/px":  false, // protocol-relative = tracking vector
		"/relative/path":         false,
		"#anchor":                false,
		"":                       false,
		"ftp://example.com":      false,
		"tel:+15551234":          false,
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
