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
