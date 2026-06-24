package main

import (
	"testing"

	"erickaldama-mail/internal/mailbox"
	tea "github.com/charmbracelet/bubbletea"
)

func newTestModel() model {
	return model{
		view:     viewList,
		headers:  []mailbox.Header{{Subject: "A"}, {Subject: "B"}, {Subject: "C"}},
		selected: 0,
	}
}

func TestJMovesDown(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if updated.(model).selected != 1 {
		t.Fatalf("j should move to 1, got %d", updated.(model).selected)
	}
}

func TestGGoesToTop(t *testing.T) {
	// Bubble Tea delivers each keypress as a SEPARATE KeyMsg — 'gg' is TWO 'g' events, not one Runes:{'g','g'}.
	// The model detects double-g via lastKey state, so the test must send two consecutive 'g' KeyMsg.
	m := newTestModel()
	m.selected = 2
	u1, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	u2, _ := u1.(model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if u2.(model).selected != 0 {
		t.Fatalf("gg should go to top, got %d", u2.(model).selected)
	}
}

func TestEnterOpensReader(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.(model).view != viewReader {
		t.Fatalf("Enter should open reader, got view %d", updated.(model).view)
	}
}

func TestComposerSendRequiresConfirmation(t *testing.T) {
	// Security control #1 of the TUI: Ctrl-S must NOT send directly — it enters a confirm state; only 'y' sends.
	m := model{view: viewComposer}
	afterCtrlS, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	mc := afterCtrlS.(model)
	if !mc.confirming {
		t.Fatal("Ctrl-S must enter confirming state, not send immediately")
	}
	if mc.sent {
		t.Fatal("Ctrl-S must NOT have sent the email")
	}
	afterN, _ := mc.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if afterN.(model).sent {
		t.Fatal("'n' must cancel, not send")
	}
}

func TestSentMsgClearsConfirmState(t *testing.T) {
	// audit H-2: a successful send (sentMsg) must clear confirming + draft so a later 'y' cannot re-send.
	m := model{view: viewComposer, confirming: true, composeDraft: "body"}
	updated, _ := m.Update(sentMsg{messageID: "mid-1"})
	mu := updated.(model)
	if mu.confirming || !mu.sent || mu.composeDraft != "" || mu.view != viewList {
		t.Fatalf("sentMsg must clear confirm/draft, set sent, return to list; got %+v", mu)
	}
}

func TestReplyDraftPrePopulates(t *testing.T) {
	// audit H-4: replying pre-populates To:/Subject: (Re:) from the selected header; bounds-checked.
	hs := []mailbox.Header{{From: "alice@example.com", Subject: "Hola"}}
	got := replyDraft(hs, 0)
	if got != "To: alice@example.com\nSubject: Re: Hola\n\n" {
		t.Fatalf("replyDraft: %q", got)
	}
	if replyDraft(hs, 5) != "" || replyDraft(nil, 0) != "" {
		t.Fatal("replyDraft must be bounds-safe (out-of-range / empty → \"\")")
	}
	// already-"Re:" subject must not double-prefix
	if replyDraft([]mailbox.Header{{From: "b@x", Subject: "Re: x"}}, 0) != "To: b@x\nSubject: Re: x\n\n" {
		t.Fatal("replyDraft must not double Re:")
	}
}
