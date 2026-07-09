package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"erickaldama-mail/internal/cache"
	"erickaldama-mail/internal/mailbox"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

// Description is the second line: sender + date+time (v0.5: FormatDate, shared with the CLI).
func (it messageItem) Description() string {
	return it.h.From + " · " + cache.FormatDate(it.h.Date)
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

// itemDelegate renders each message as three lines: subject (title), sender·datetime (description),
// and the S3 key in faint style. DefaultDelegate can't style the third line independently, so this
// is a minimal custom list.ItemDelegate (audit C-2).
type itemDelegate struct{}

func newItemDelegate() itemDelegate { return itemDelegate{} }

func (d itemDelegate) Height() int                             { return 3 }
func (d itemDelegate) Spacing() int                            { return 1 }
func (d itemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	descStyle     = lipgloss.NewStyle().Faint(true)
	s3KeyStyle    = lipgloss.NewStyle().Faint(true)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
)

func (d itemDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(messageItem)
	if !ok {
		return
	}
	title := it.Title()
	if index == m.Index() {
		title = selectedStyle.Render(title)
	} else {
		title = titleStyle.Render(title)
	}
	fmt.Fprintf(w, "%s\n%s\n%s", title, descStyle.Render(it.Description()), s3KeyStyle.Render(it.h.S3Key))
}

// newMessageList builds the list.Model with the 3-line itemDelegate.
func newMessageList(headers []mailbox.Header, width, height int) list.Model {
	items := make([]list.Item, len(headers))
	for i, h := range headers {
		items[i] = messageItem{h: h}
	}
	l := list.New(items, newItemDelegate(), width, height)
	l.Title = "Inbox"
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	return l
}
