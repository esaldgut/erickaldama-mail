package main

import (
	"context"
	"slices"
	"strings"
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
	m := model{view: viewComposer, compose: newComposer()}
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
	// audit H-2: a successful send (sentMsg) must clear confirming + compose so a later 'y' cannot re-send.
	c := newComposer()
	c.inputs[cTo].SetValue("alice@example.com")
	m := model{view: viewComposer, confirming: true, compose: c}
	updated, _ := m.Update(sentMsg{messageID: "mid-1"})
	mu := updated.(model)
	if mu.confirming || !mu.sent || mu.compose.inputs[cTo].Value() != "" || mu.view != viewList {
		t.Fatalf("sentMsg must clear confirm/compose, set sent, return to list; got confirming=%v sent=%v to=%q view=%d",
			mu.confirming, mu.sent, mu.compose.inputs[cTo].Value(), mu.view)
	}
}

func TestReplyDraftPrePopulates(t *testing.T) {
	// audit H-4: pressing 'r' on a list header pre-populates To/Subject without a live reader.
	m := model{
		view: viewList,
		headers: []mailbox.Header{
			{From: "alice@example.com", Subject: "Hola", S3Key: ""},
		},
		selected: 0,
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	mu := updated.(model)
	if mu.view != viewComposer {
		t.Fatalf("'r' must open composer, got view %d", mu.view)
	}
	if mu.compose.inputs[cTo].Value() != "alice@example.com" {
		t.Fatalf("To must be pre-filled with sender, got %q", mu.compose.inputs[cTo].Value())
	}
	if mu.compose.inputs[cSubject].Value() != "Re: Hola" {
		t.Fatalf("Subject must be pre-filled with Re:, got %q", mu.compose.inputs[cSubject].Value())
	}
	// already-"Re:" subject must not double-prefix
	m2 := model{
		view:     viewList,
		headers:  []mailbox.Header{{From: "b@x.com", Subject: "Re: existing", S3Key: ""}},
		selected: 0,
	}
	u2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	mu2 := u2.(model)
	if mu2.compose.inputs[cSubject].Value() != "Re: existing" {
		t.Fatalf("must not double Re:, got %q", mu2.compose.inputs[cSubject].Value())
	}
}

// fakeSender captures the raw bytes and destinations passed to Send (for invariant testing).
type fakeSender struct {
	gotRaw   []byte
	gotDests []string
}

func (f *fakeSender) Send(_ context.Context, raw []byte, dests []string) (string, error) {
	f.gotRaw = raw
	f.gotDests = dests
	return "mid-1", nil
}

func TestComposerBccNotInRaw(t *testing.T) {
	// BCC-1: the TUI send path uses message.Build, so the Bcc never leaks into the raw MIME.
	// The fakeSender captures what Build produces; we assert Bcc: header absent and secret@x.com absent from raw,
	// but present in the envelope destinations.
	fs := &fakeSender{}
	c := newComposer()
	c.inputs[cTo].SetValue("to@x.com")
	c.inputs[cBcc].SetValue("secret@x.com")
	c.body = "hi"
	m := model{
		view:       viewComposer,
		confirming: true,
		sender:     fs,
		from:       "me@example.com",
		compose:    c,
	}
	// 'y' fires the send tea.Cmd.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("'y' with a live sender must return a tea.Cmd")
	}
	// Execute the cmd to trigger the actual Send call.
	cmd()
	if strings.Contains(string(fs.gotRaw), "Bcc:") {
		t.Fatalf("BCC header leaked into TUI-sent raw:\n%s", fs.gotRaw)
	}
	if strings.Contains(string(fs.gotRaw), "secret@x.com") {
		t.Fatalf("BCC address leaked into TUI-sent raw:\n%s", fs.gotRaw)
	}
	if !slices.Contains(fs.gotDests, "secret@x.com") {
		t.Fatalf("BCC must be in the envelope destinations: %v", fs.gotDests)
	}
}

func TestComposerTabNavigation(t *testing.T) {
	// GAP-4: Tab advances the active field from To → Cc.
	m := model{view: viewComposer, compose: newComposer()}
	if m.compose.active != cTo {
		t.Fatalf("starts at To (%d), got %d", cTo, m.compose.active)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	mu := updated.(model)
	if mu.compose.active != cCc {
		t.Fatalf("Tab → Cc (%d), got %d", cCc, mu.compose.active)
	}
}

func TestModelCapturesWindowSize(t *testing.T) {
	m := model{}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if updated.(model).termWidth != 120 {
		t.Errorf("termWidth not captured from WindowSizeMsg")
	}
}

func TestReaderKeyIRendersImages(t *testing.T) {
	// con un Parsed que tiene InlineImages y view==viewReader, la tecla 'i' marca showImages=true
	// (test del estado, no del render real de chafa)
	m := model{view: viewReader, termWidth: 80}
	updated, _ := m.handleReaderKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if !updated.(model).showImages {
		t.Errorf("'i' should toggle showImages")
	}
}
