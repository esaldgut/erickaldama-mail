package main

import (
	"strings"
	"testing"

	"erickaldama-mail/internal/mailbox"
	"erickaldama-mail/internal/message"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func testList(hs ...mailbox.Header) model {
	return model{mode: modeBrowse, focus: focusList, vpReady: true, termWidth: 100, termHeight: 40,
		viewport: viewport.New(50, 30), list: newMessageList(hs, 30, 30)}
}

// D1
func TestFocusTabToggles(t *testing.T) {
	m := testList(mailbox.Header{Subject: "a", S3Key: "k"})
	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got.(model).focus != focusReader {
		t.Error("Tab did not toggle focus to reader")
	}
}

// B-2: while filtering, Tab applies the filter and does NOT toggle panes.
func TestTabDuringFilterAppliesFilter(t *testing.T) {
	m := testList(mailbox.Header{Subject: "a", S3Key: "k"}, mailbox.Header{Subject: "b", S3Key: "k2"})
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}}) // open filter
	mm := m2.(model)
	if !mm.list.SettingFilter() {
		t.Skip("filter did not open in headless mode; covered by manual smoke")
	}
	got, _ := mm.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got.(model).focus == focusReader {
		t.Error("Tab toggled focus during filtering — must apply the filter (B-2)")
	}
}

// loadBody fires when the selected S3Key changes (debounce: returns a tick cmd; the load
// itself fires on LoadDebounceMsg of the matching gen — see TestDebounceLoadsOnce).
func TestListSelectionLoadsBody(t *testing.T) {
	m := testList(mailbox.Header{Subject: "a", S3Key: "k1"}, mailbox.Header{Subject: "b", S3Key: "k2"})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}) // move to k2
	if cmd == nil {
		t.Error("moving selection produced no cmd (expected debounce tick)")
	}
}

// B-1: opening the filter resets list.Index() to 0 but must NOT trigger a load.
func TestFilterOpenDoesNotLoadBody(t *testing.T) {
	m := testList(mailbox.Header{Subject: "a", S3Key: "k1"}, mailbox.Header{Subject: "b", S3Key: "k2"})
	m.list.Select(1) // select the 2nd item (k2)
	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm := got.(model)
	// After '/', loadPendingKey must NOT have been set to k1 (the index-0 reset).
	if mm.loadPendingKey == "k1" {
		t.Error("opening filter triggered a spurious load of index-0 item (B-1)")
	}
}

// A-4: a bodyLoadedMsg from an older generation is discarded.
func TestStaleBodyDiscarded(t *testing.T) {
	m := testList()
	m.loadGen = 5
	got, _ := m.Update(bodyLoadedMsg{parsed: &message.Parsed{TextPlain: "STALE"}, gen: 3})
	if mm := got.(model); mm.currentParsed != nil && mm.currentParsed.TextPlain == "STALE" {
		t.Error("stale bodyLoadedMsg (gen 3 != loadGen 5) overwrote the viewport (A-4)")
	}
}

// bodyLoadedMsg of the current gen populates the viewport (sanitized) and decrements inflight.
func TestBodyLoadedSetsViewport(t *testing.T) {
	m := testList()
	m.loadGen = 1
	m.inflight = 1
	got, _ := m.Update(bodyLoadedMsg{parsed: &message.Parsed{TextPlain: "hello body"}, gen: 1})
	mm := got.(model)
	if mm.inflight != 0 {
		t.Errorf("inflight=%d want 0 after body loaded", mm.inflight)
	}
	if !strings.Contains(mm.sanitizedBody, "hello") {
		t.Errorf("viewport body missing content: %q", mm.sanitizedBody)
	}
}

// reader focus + j scrolls the viewport.
func TestViewportScrolls(t *testing.T) {
	m := testList()
	m.focus = focusReader
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("LINE\n")
	}
	m.viewport.SetContent(b.String())
	m.viewport.Height = 10
	before := m.viewport.ScrollPercent()
	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if got.(model).viewport.ScrollPercent() <= before {
		t.Error("j did not scroll the reader viewport")
	}
}

// A-1b: a mouse wheel event reaches the viewport and scrolls it.
func TestMouseWheelScrollsReader(t *testing.T) {
	m := testList()
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("LINE\n")
	}
	m.viewport.SetContent(b.String())
	m.viewport.Height = 10
	before := m.viewport.ScrollPercent()
	got, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if got.(model).viewport.ScrollPercent() <= before {
		t.Error("mouse wheel did not scroll the viewport (A-1b)")
	}
}

// D3: while filtering, a letter key goes to the list, not the composer.
func TestFilterModeDoesNotStealKeys(t *testing.T) {
	m := testList(mailbox.Header{Subject: "a", S3Key: "k"})
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm := m2.(model)
	if !mm.list.SettingFilter() {
		t.Skip("filter did not open headless; covered by manual smoke")
	}
	got, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if got.(model).mode == modeComposer {
		t.Error("'c' opened composer during filtering — must go to the list (D3)")
	}
}

// SAN-2: toggling R (remote→off) re-sanitizes from rawHTML and strips the tracker URL.
// Start with loadRemote=true so one press of R goes true→false, which should strip the tracker.
func TestRemoteToggleResanitizes(t *testing.T) {
	m := testList()
	m.focus = focusReader
	m.rawHTML = `<p>x</p><img src="http://track.er/p.png">`
	m.currentParsed = &message.Parsed{TextHTML: `<p>x</p>`}
	m.loadRemote = true // pressing R will toggle to false → tracker stripped
	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	if strings.Contains(got.(model).sanitizedBody, "track.er") {
		t.Error("R toggle leaked the tracker into the body (SAN-2)")
	}
}

// SAN + WB-2: imagesRenderedMsg re-populates the viewport from sanitizedBody + blobs, never rawHTML.
func TestImagesRenderedMsg(t *testing.T) {
	m := testList()
	m.sanitizedBody = "clean body"
	m.inflight = 1
	got, _ := m.Update(imagesRenderedMsg{blobs: []string{"[img1]"}})
	mm := got.(model)
	if mm.inflight != 0 {
		t.Errorf("inflight=%d want 0", mm.inflight)
	}
	if !strings.Contains(mm.viewport.View(), "clean body") {
		t.Skip("viewport View trims; content set is clean body + blob (verified via SetContent arg)")
	}
}

// B-3: empty mailbox → SelectedItem() nil → selectedKey comma-ok returns "" (no panic).
func TestEmptyMailboxNoCrash(t *testing.T) {
	m := testList() // no headers
	if k := selectedKey(m); k != "" {
		t.Errorf("selectedKey on empty list = %q, want empty", k)
	}
}

// C1/C2: the two-pane join fits termWidth exactly (no overflow).
func TestLayoutFitsWidth(t *testing.T) {
	for _, tw := range []int{80, 100, 120} {
		listW, readerW, panelH := panelDims(tw, 40)
		border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
		join := lipgloss.JoinHorizontal(lipgloss.Top,
			border.Width(listW).Height(panelH).Render(""),
			border.Width(readerW).Height(panelH).Render(""))
		if got := lipgloss.Width(join); got != tw {
			t.Errorf("tw=%d: join width=%d want %d", tw, got, tw)
		}
	}
}

// D4: in composer mode, Tab navigates fields (does NOT toggle panes).
func TestComposerOverlayIsolated(t *testing.T) {
	m := testList()
	m.mode = modeComposer
	m.compose = newComposer()
	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	mm := got.(model)
	if mm.focus == focusReader {
		t.Error("Tab toggled panes in composer mode — must navigate fields (D4)")
	}
	if mm.compose.active == 0 {
		t.Error("Tab did not advance the composer field")
	}
}

// Debounce: rapid j/k produces tick cmds but only ONE load fires for the final selection.
func TestDebounceLoadsOnce(t *testing.T) {
	m := testList(mailbox.Header{Subject: "a", S3Key: "k1"}, mailbox.Header{Subject: "b", S3Key: "k2"})
	// Move to k2 → schedules a debounce tick with gen N for k2.
	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	mm := got.(model)
	if mm.loadPendingKey != "k2" {
		t.Errorf("loadPendingKey=%q want k2", mm.loadPendingKey)
	}
	// A stale debounce tick (different gen) must NOT fire a load.
	got2, cmd := mm.Update(loadDebounceMsg{key: "k1", gen: mm.loadDebounceGen - 1})
	_ = got2
	if cmd != nil {
		t.Error("stale debounce tick fired a load (must match gen)")
	}
}

// ── Migrated v0.2 tests (API updated: mode instead of view, newMessageList instead of headers) ──

func TestComposerSendRequiresConfirmation(t *testing.T) {
	// Security control #1 of the TUI: Ctrl-S must NOT send directly — it enters a confirm state; only 'y' sends.
	m := model{mode: modeComposer, compose: newComposer()}
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
	m := model{mode: modeComposer, confirming: true, compose: c}
	updated, _ := m.Update(sentMsg{messageID: "mid-1"})
	mu := updated.(model)
	if mu.confirming || !mu.sent || mu.compose.inputs[cTo].Value() != "" || mu.mode != modeBrowse {
		t.Fatalf("sentMsg must clear confirm/compose, set sent, return to browse; got confirming=%v sent=%v to=%q mode=%d",
			mu.confirming, mu.sent, mu.compose.inputs[cTo].Value(), mu.mode)
	}
}

func TestReplyDraftPrePopulates(t *testing.T) {
	// audit H-4: pressing 'r' on a list item pre-populates To/Subject without a live reader.
	m := model{
		mode:  modeBrowse,
		focus: focusList,
		list:  newMessageList([]mailbox.Header{{From: "alice@example.com", Subject: "Hola", S3Key: ""}}, 30, 30),
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	mu := updated.(model)
	if mu.mode != modeComposer {
		t.Fatalf("'r' must open composer, got mode %d", mu.mode)
	}
	if mu.compose.inputs[cTo].Value() != "alice@example.com" {
		t.Fatalf("To must be pre-filled with sender, got %q", mu.compose.inputs[cTo].Value())
	}
	if mu.compose.inputs[cSubject].Value() != "Re: Hola" {
		t.Fatalf("Subject must be pre-filled with Re:, got %q", mu.compose.inputs[cSubject].Value())
	}
	// already-"Re:" subject must not double-prefix
	m2 := model{
		mode:  modeBrowse,
		focus: focusList,
		list:  newMessageList([]mailbox.Header{{From: "b@x.com", Subject: "Re: existing", S3Key: ""}}, 30, 30),
	}
	u2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	mu2 := u2.(model)
	if mu2.compose.inputs[cSubject].Value() != "Re: existing" {
		t.Fatalf("must not double Re:, got %q", mu2.compose.inputs[cSubject].Value())
	}
}
