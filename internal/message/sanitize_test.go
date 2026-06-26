package message

import (
	"regexp"
	"strings"
	"testing"
)

func TestSanitizeBlocksXSS(t *testing.T) { // SAN-1
	in := `<p>ok</p><script>alert(1)</script><a href="javascript:bad()">x</a>` +
		`<img onerror="evil()" src="cid:a@b"><iframe src="x"></iframe>` +
		`<style>body{}</style><p style="color:red">y</p><link rel="stylesheet" href="x">`
	r, err := SanitizeHTML(in, false)
	if err != nil {
		t.Fatalf("SanitizeHTML: %v", err)
	}
	// P-9: incluir <object>, <embed>, data: (UGCPolicy los bloquea — el test lo asegura ante regresiones)
	for _, bad := range []string{"<script", "javascript:", "onerror", "<iframe", "<style", "style=", "<link", "<object", "<embed", "data:"} {
		if strings.Contains(r.HTML, bad) {
			t.Errorf("SAN-1: %q survived: %s", bad, r.HTML)
		}
	}
}

func TestSanitizeBlocksRemoteImages(t *testing.T) { // SAN-2 (forma canónica)
	in := `<img src="http://track.er/p.png"><img src="cid:logo@x">`
	r, err := SanitizeHTML(in, false)
	if err != nil {
		t.Fatalf("SanitizeHTML: %v", err)
	}
	// P-3: assert robusto — NINGÚN src apunta a red (no solo buscar "http://" literal)
	if regexp.MustCompile(`src=["'][^"']*://`).MatchString(r.HTML) {
		t.Errorf("SAN-2: remote URL with scheme survived: %s", r.HTML)
	}
	if !strings.Contains(r.HTML, "cid:logo@x") {
		t.Errorf("SAN-2: cid: image lost: %s", r.HTML)
	}
	if len(r.BlockedRemotes) != 1 {
		t.Errorf("want 1 blocked remote, got %d", len(r.BlockedRemotes))
	}
}

func TestSanitizeBlocksRemoteImagesBypasses(t *testing.T) { // P-3: los bypasses que el test canónico no veía
	cases := []string{
		`<img src="HTTP://track.er/p.png">`,   // mayúsculas
		`<img src="HTTPS://track.er/p.png">`,  // mayúsculas
		`<img src="//track.er/p.png">`,        // protocol-relative
		`<img src="  http://track.er/p.png">`, // espacios al inicio
	}
	reScheme := regexp.MustCompile(`src=["'][^"']*://`)
	reRel := regexp.MustCompile(`src=["']\s*//`)
	for _, in := range cases {
		r, err := SanitizeHTML(in, false)
		if err != nil {
			t.Fatalf("SanitizeHTML(%q): %v", in, err)
		}
		if reScheme.MatchString(r.HTML) || reRel.MatchString(r.HTML) {
			t.Errorf("SAN-2 bypass: remote URL survived in %q → %s", in, r.HTML)
		}
	}
}

func TestSanitizeAllowRemote(t *testing.T) { // escape consciente
	in := `<img src="http://track.er/p.png">`
	r, err := SanitizeHTML(in, true)
	if err != nil {
		t.Fatalf("SanitizeHTML: %v", err)
	}
	if !strings.Contains(r.HTML, "http://track.er/p.png") {
		t.Errorf("allowRemote=true should keep remote img: %s", r.HTML)
	}
}
