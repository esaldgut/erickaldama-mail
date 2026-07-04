package message

import (
	"strconv"
	"testing"
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
