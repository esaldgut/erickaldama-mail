package cache

import (
	"testing"
	"time"
)

func TestFormatDate(t *testing.T) {
	// RFC1123Z (typical email Date header)
	got := FormatDate("Wed, 25 Jun 2026 14:32:00 +0000")
	// Expected is local time — compute it the same way to avoid TZ-flaky tests.
	parsed, _ := time.Parse(time.RFC1123Z, "Wed, 25 Jun 2026 14:32:00 +0000")
	want := parsed.Local().Format("2006-01-02 15:04")
	if got != want {
		t.Errorf("FormatDate = %q, want %q", got, want)
	}
}

func TestFormatDateFallsBackToRaw(t *testing.T) {
	if got := FormatDate("not a date"); got != "not a date" {
		t.Errorf("FormatDate(unparseable) = %q, want raw echo", got)
	}
}
