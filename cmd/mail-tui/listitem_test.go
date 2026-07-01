package main

import (
	"testing"

	"erickaldama-mail/internal/mailbox"
	"github.com/charmbracelet/bubbles/list"
)

func TestMessageItemDefaultItem(t *testing.T) {
	var _ list.DefaultItem = messageItem{} // compile-time: satisfies the interface

	it := messageItem{h: mailbox.Header{From: "alice@example.com", Subject: "Hola", Date: "Mon, 23 Jun 2026 10:00:00 +0000", S3Key: "inbound/abc"}}
	if it.Title() != "Hola" {
		t.Errorf("Title=%q want Hola", it.Title())
	}
	if it.FilterValue() != "alice@example.com Hola" {
		t.Errorf("FilterValue=%q", it.FilterValue())
	}
	if it.S3Key() != "inbound/abc" {
		t.Errorf("S3Key=%q", it.S3Key())
	}
}

func TestMessageItemEmptySubjectFallback(t *testing.T) {
	it := messageItem{h: mailbox.Header{From: "a@x", Subject: ""}} // omitempty → empty from DynamoDB
	if it.Title() != "(sin asunto)" {
		t.Errorf("empty Subject Title=%q want (sin asunto)", it.Title())
	}
}

func TestNewMessageListPopulates(t *testing.T) {
	hs := []mailbox.Header{{From: "a@x", Subject: "One", S3Key: "k1"}, {From: "b@y", Subject: "Two", S3Key: "k2"}}
	l := newMessageList(hs, 30, 20)
	if len(l.Items()) != 2 {
		t.Errorf("list has %d items, want 2", len(l.Items()))
	}
}

func TestShortDateRobust(t *testing.T) {
	cases := map[string]string{
		"Mon, 23 Jun 2026 10:00:00 +0000": "23 Jun 2026", // RFC1123Z
		"23 Jun 2026 10:00:00 +0000":      "23 Jun 2026", // RFC822-ish sin día de semana
		"2026-06-23T10:00:00Z":            "23 Jun 2026", // ISO8601 (algunos ingest)
	}
	for in, want := range cases {
		if got := shortDate(in); got != want {
			t.Errorf("shortDate(%q)=%q want %q", in, got, want)
		}
	}
	// Unparseable → return the raw string (no garbage slice).
	if got := shortDate("garbage"); got != "garbage" {
		t.Errorf("shortDate(garbage)=%q want raw passthrough", got)
	}
}
