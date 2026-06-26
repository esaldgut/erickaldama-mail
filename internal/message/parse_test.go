package message

import (
	"os"
	"strings"
	"testing"
)

func parseFixture(t *testing.T, name string) *Parsed {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParsePlain(t *testing.T) {
	p := parseFixture(t, "plain.eml")
	if p.Subject != "Hola plano" || p.From != "alice@example.com" {
		t.Fatalf("headers: %+v", p)
	}
	if p.TextPlain == "" || !strings.Contains(p.TextPlain, "café") {
		t.Fatalf("plain text not decoded: %q", p.TextPlain)
	}
	if p.MessageID != "<plain-001@example.com>" {
		t.Fatalf("message-id: %q", p.MessageID)
	}
}

func TestParseHTMLWithReferences(t *testing.T) {
	p := parseFixture(t, "html.eml")
	if p.TextHTML == "" || !strings.Contains(p.TextHTML, "<b>mundo</b>") {
		t.Fatalf("html missing: %q", p.TextHTML)
	}
	if p.References != "<thread-root@example.com>" {
		t.Fatalf("references: %q", p.References)
	}
}

func TestParseAttachment(t *testing.T) {
	p := parseFixture(t, "multipart-attach.eml")
	if len(p.Attachments) != 1 {
		t.Fatalf("attachments: %+v", p.Attachments)
	}
	a := p.Attachments[0]
	if a.FileName != "doc.pdf" || a.ContentType != "application/pdf" || a.Size != 9 {
		t.Fatalf("attachment fields: %+v (want doc.pdf/application/pdf/9 bytes)", a)
	}
}

func TestParseCapturesCcAndTo(t *testing.T) {
	raw := "From: a@x.com\r\nTo: to@x.com\r\nCc: cc1@x.com, cc2@x.com\r\nSubject: hi\r\n\r\nbody\r\n"
	p, err := Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if p.To != "to@x.com" {
		t.Fatalf("To = %q", p.To)
	}
	if !strings.Contains(p.Cc, "cc1@x.com") || !strings.Contains(p.Cc, "cc2@x.com") {
		t.Fatalf("Cc = %q", p.Cc)
	}
}

func TestParseExtractsInlineImages(t *testing.T) {
	// raw MIME con multipart/related: 1 text/html + 1 image/png inline (cid:logo@x)
	raw := "MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/related; boundary=b1\r\n\r\n" +
		"--b1\r\n" +
		"Content-Type: text/html\r\n\r\n" +
		"<html><body><img src=\"cid:logo@x\"></body></html>\r\n" +
		"--b1\r\n" +
		"Content-Type: image/png\r\n" +
		"Content-ID: <logo@x>\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		"iVBORw0KGgo=\r\n" + // PNG header bytes (base64)
		"--b1--\r\n"
	p, err := Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(p.InlineImages) != 1 {
		t.Fatalf("want 1 inline image, got %d", len(p.InlineImages))
	}
	img := p.InlineImages[0]
	if img.ContentID != "logo@x" { // sin <>
		t.Errorf("ContentID = %q, want logo@x", img.ContentID)
	}
	if img.ContentType != "image/png" {
		t.Errorf("ContentType = %q, want image/png", img.ContentType)
	}
	if len(img.Data) == 0 {
		t.Error("Data empty — base64 not decoded")
	}
}
