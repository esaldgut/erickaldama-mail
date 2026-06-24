package message

import (
	"bytes"
	"strings"
	"testing"
)

func TestReplyHeaders(t *testing.T) {
	orig := &Parsed{Subject: "Hola", MessageID: "<html-001@example.com>", References: "<thread-root@example.com>"}
	irt, refs, subj := ReplyHeaders(orig)
	if irt != "<html-001@example.com>" {
		t.Fatalf("in-reply-to: %q", irt)
	}
	if refs != "<thread-root@example.com> <html-001@example.com>" {
		t.Fatalf("references chain: %q", refs)
	}
	if subj != "Re: Hola" {
		t.Fatalf("subject: %q", subj)
	}
}

func TestReplyHeadersAlreadyRe(t *testing.T) {
	for _, subjIn := range []string{"Re: Hola", "RE: Hola", "re: Hola"} {
		orig := &Parsed{Subject: subjIn, MessageID: "<m@x>"}
		_, _, subj := ReplyHeaders(orig)
		if subj != subjIn { // already-replied subject (any case) must not gain a second "Re:"
			t.Fatalf("must not double Re: for %q, got %q", subjIn, subj)
		}
	}
}

func TestReplyHeadersNilSafe(t *testing.T) {
	irt, refs, subj := ReplyHeaders(nil) // public API must not panic on nil (audit F-1)
	if irt != "" || refs != "" || subj != "" {
		t.Fatalf("nil orig should yield empty headers, got %q %q %q", irt, refs, subj)
	}
}

func TestNewMessageIDFormat(t *testing.T) {
	id := NewMessageID()
	if !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, "@erickaldama.com>") {
		t.Fatalf("message-id format: %q", id)
	}
}

func TestBuildRoundTrip(t *testing.T) {
	raw, err := Build(BuildOpts{
		From: "erick@erickaldama.com", To: "bob@example.com", Subject: "Re: Hola",
		Body: "cuerpo de prueba", InReplyTo: "<html-001@example.com>",
		References: "<thread-root@example.com> <html-001@example.com>", MessageID: "<own-1@erickaldama.com>",
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("built MIME not parseable: %v", err)
	}
	if p.Subject != "Re: Hola" || p.MessageID != "<own-1@erickaldama.com>" {
		t.Fatalf("headers lost in round-trip: %+v", p)
	}
	if p.References != "<thread-root@example.com> <html-001@example.com>" {
		t.Fatalf("references chain lost: %q", p.References)
	}
	if p.InReplyTo != "<html-001@example.com>" { // all 3 threading headers must survive the round-trip (F-2)
		t.Fatalf("in-reply-to lost: %q", p.InReplyTo)
	}
	if !strings.Contains(p.TextPlain, "cuerpo de prueba") {
		t.Fatalf("body lost: %q", p.TextPlain)
	}
}
