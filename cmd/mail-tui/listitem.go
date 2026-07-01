package main

import (
	"strings"
	"time"

	"erickaldama-mail/internal/mailbox"
	"github.com/charmbracelet/bubbles/list"
)

// messageItem adapts a mailbox.Header to bubbles/list. It wraps the WHOLE Header (not
// just the display fields) so reply/load paths can recover S3Key after headers[] is gone (audit B-7).
type messageItem struct{ h mailbox.Header }

// Title is the list's first line. Falls back when Subject is empty (omitempty in DynamoDB, audit B-6).
func (it messageItem) Title() string {
	if strings.TrimSpace(it.h.Subject) == "" {
		return "(sin asunto)"
	}
	return it.h.Subject
}

// Description is the second line: sender + short date.
func (it messageItem) Description() string {
	return it.h.From + " · " + shortDate(it.h.Date)
}

// FilterValue is what the fuzzy filter ('/') searches: sender OR subject.
func (it messageItem) FilterValue() string { return it.h.From + " " + it.h.Subject }

// S3Key is the stable identity used as the loadBody trigger (audit B-1) and reply source (B-7).
func (it messageItem) S3Key() string { return it.h.S3Key }

// shortDate parses common email Date header formats and returns "DD Mon YYYY".
// The Date comes from env.GetHeader("Date") unnormalized, so try several layouts;
// fall back to the raw string if none parse (audit A-shortDate — no fixed-index slice).
func shortDate(d string) string {
	d = strings.TrimSpace(d)
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, "2 Jan 2006 15:04:05 -0700", time.RFC3339} {
		if t, err := time.Parse(layout, d); err == nil {
			return t.Format("2 Jan 2006")
		}
	}
	return d
}

// newMessageList builds the list.Model with the DefaultDelegate (verified in Fase 0),
// title/status bar hidden so it embeds cleanly in the narrow left pane.
func newMessageList(headers []mailbox.Header, width, height int) list.Model {
	items := make([]list.Item, len(headers))
	for i, h := range headers {
		items[i] = messageItem{h: h}
	}
	l := list.New(items, list.NewDefaultDelegate(), width, height)
	l.Title = "Inbox"
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	return l
}
