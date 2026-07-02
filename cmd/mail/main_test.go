package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"erickaldama-mail/internal/mailbox"
)

func TestRenderListJSON(t *testing.T) {
	hs := []mailbox.Header{{MessageID: "abc", Subject: "Hola", From: "a@x"}}
	var buf bytes.Buffer
	if err := renderList(&buf, hs, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"messageId"`) || !strings.Contains(buf.String(), "abc") {
		t.Fatalf("json output: %s", buf.String())
	}
}

func TestRenderListTable(t *testing.T) {
	hs := []mailbox.Header{{Subject: "Hola", From: "a@x", Date: "Mon"}}
	var buf bytes.Buffer
	if err := renderList(&buf, hs, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Hola") {
		t.Fatalf("table output: %s", buf.String())
	}
}

func TestRenderListShowsDateTimeAndS3Key(t *testing.T) {
	hs := []mailbox.Header{
		{S3Key: "inbound/aaa-000000", From: "alice@example.com", Subject: "Hello", Date: "Wed, 25 Jun 2026 14:32:00 +0000"},
	}
	var buf bytes.Buffer
	if err := renderList(&buf, hs, false); err != nil {
		t.Fatalf("renderList: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "inbound/aaa-000000") {
		t.Errorf("output missing s3Key:\n%s", out)
	}
	if !strings.Contains(out, "2026-06-25") { // date part (time is TZ-local, only assert the date)
		t.Errorf("output missing formatted date:\n%s", out)
	}
}

func TestTmuxPopupArgs(t *testing.T) {
	args := tmuxPopupArgs("mail-client-read", "inbox")
	// Must be display-popup -E launching mail-tui with the forwarded flags.
	if args[0] != "display-popup" {
		t.Fatalf("expected display-popup first, got %q", args[0])
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-E", "mail-tui", "--read-profile mail-client-read", "--mailbox inbox"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in args: %v", want, args)
		}
	}
	// Each value is its OWN argv element (no shell concatenation → no injection): the profile
	// and mailbox appear as standalone slice entries, not merged into one shell string.
	if !slices.Contains(args, "mail-client-read") || !slices.Contains(args, "inbox") {
		t.Fatalf("profile/mailbox not passed as standalone argv elements: %v", args)
	}
}
