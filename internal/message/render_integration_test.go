package message

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestSAN5InlineImageFixture is the SAN-5 integration test. It reads the synthetic
// multipart/related fixture (no real mail, no NDA material) and verifies:
//
//	(a) Parse produces exactly 1 InlineImage with ContentID "logo@test" and non-empty Data
//	    (the base64 PNG bytes decoded by enmime).
//	(b) SanitizeHTML with allowRemote=false blocks the remote tracking pixel
//	    (no src with a network scheme survives) while preserving the cid: reference.
func TestSAN5InlineImageFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/inline-image.eml")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// --- (a) Parse: InlineImages ---
	p, err := Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(p.InlineImages) != 1 {
		t.Fatalf("SAN-5(a): want 1 inline image, got %d: %+v", len(p.InlineImages), p.InlineImages)
	}
	img := p.InlineImages[0]
	if img.ContentID != "logo@test" {
		t.Errorf("SAN-5(a): ContentID = %q, want %q", img.ContentID, "logo@test")
	}
	if img.ContentType != "image/png" {
		t.Errorf("SAN-5(a): ContentType = %q, want image/png", img.ContentType)
	}
	if len(img.Data) == 0 {
		t.Error("SAN-5(a): Data is empty — base64 not decoded by enmime")
	}

	// --- (b) SanitizeHTML: remote blocked, cid: preserved ---
	if p.TextHTML == "" {
		t.Fatal("SAN-5(b): TextHTML is empty — fixture not parsed correctly")
	}
	result, err := SanitizeHTML(p.TextHTML, false)
	if err != nil {
		t.Fatalf("SAN-5(b): SanitizeHTML: %v", err)
	}

	// P-3 robust assertion: no src pointing to a network scheme (://), including protocol-relative
	reScheme := regexp.MustCompile(`src=["'][^"']*://`)
	reProtoRel := regexp.MustCompile(`src=["']\s*//`)
	if reScheme.MatchString(result.HTML) {
		t.Errorf("SAN-5(b): remote URL with scheme survived sanitize: %s", result.HTML)
	}
	if reProtoRel.MatchString(result.HTML) {
		t.Errorf("SAN-5(b): protocol-relative URL survived sanitize: %s", result.HTML)
	}

	// At least 1 remote was reported as blocked
	if len(result.BlockedRemotes) == 0 {
		t.Errorf("SAN-5(b): expected at least 1 blocked remote, got 0 (HTML: %s)", result.HTML)
	}

	// cid: reference must survive in the sanitized output (bluemonday allows cid: scheme)
	if !strings.Contains(result.HTML, "cid:logo@test") {
		t.Errorf("SAN-5(b): cid:logo@test lost after sanitize: %s", result.HTML)
	}
}
