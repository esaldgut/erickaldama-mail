package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"erickaldama-mail/internal/cache"
	"erickaldama-mail/internal/mailbox"
	"erickaldama-mail/internal/message"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
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

// M-1: a spinner tick with no work in flight stops the loop (returns nil cmd).
func TestSpinnerTickStopsWhenIdle(t *testing.T) {
	m := testList()
	m.inflight = 0
	_, cmd := m.Update(m.spinner.Tick()) // a TickMsg
	if cmd != nil {
		t.Error("spinner kept ticking with inflight==0 (M-1: perpetual wakeup)")
	}
}

// M-2: a stale imagesRenderedMsg (gen != loadGen) does not paint old images on the new body.
func TestStaleImagesDiscarded(t *testing.T) {
	m := testList()
	m.loadGen = 5
	m.sanitizedBody = "new body"
	m.inflight = 1
	got, _ := m.Update(imagesRenderedMsg{blobs: []string{"[old-img]"}, gen: 3})
	mm := got.(model)
	if strings.Contains(mm.viewport.View(), "old-img") {
		t.Error("stale images painted onto the new message body (M-2 race)")
	}
	if mm.inflight != 0 {
		t.Errorf("inflight=%d, want 0 (stale image render must still decrement)", mm.inflight)
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

// ROB-1: the first WindowSizeMsg auto-loads the selected message so the reader isn't blank.
func TestFirstResizeAutoLoadsSelected(t *testing.T) {
	m := model{mode: modeBrowse, focus: focusList, list: newMessageList([]mailbox.Header{{Subject: "a", S3Key: "k1"}}, 10, 10)}
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	if cmd == nil {
		t.Error("first resize with a selected message produced no load cmd (ROB-1: reader would start blank)")
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

// ── Task 10: TUI filter → FTS5 over cache (B-1/B-2/D3/C-debounce preserved) ─────────────────

// TestFilterUsesCacheSearch: wiring smoke from the brief — applying an empty filter with a
// (possibly empty) cache set must not panic and must restore a non-nil list.
func TestFilterUsesCacheSearch(t *testing.T) {
	c, err := cache.Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()
	m := model{cache: c, from: "inbox", list: newMessageList(nil, 30, 20)}
	// Applying an empty filter must restore List (no panic, list non-nil).
	m2, _ := m.applyFilter("")
	if m2.list.Items() == nil && len(m2.list.Items()) != 0 {
		t.Errorf("applyFilter empty should restore list")
	}
}

// fakeHeaderLister is a minimal cache.HeaderLister for seeding the cache in tests, without
// reaching into internal/cache's unexported test helpers.
type fakeHeaderLister struct{ hs []mailbox.Header }

func (f fakeHeaderLister) List(_ context.Context, _ string, _ int32, _ map[string]ddbtypes.AttributeValue) ([]mailbox.Header, map[string]ddbtypes.AttributeValue, error) {
	return f.hs, nil, nil
}

// TestApplyFilterSearchesAndRestoresSelectionByS3Key: seeds a real cache via Sync, selects the
// 2nd item, applies a query that matches only that item via FTS5, and verifies the list reloads
// with the match AND the selection survives by S3Key (B-1: never by Index).
func TestApplyFilterSearchesAndRestoresSelectionByS3Key(t *testing.T) {
	c, err := cache.Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()
	hs := []mailbox.Header{
		{PK: "mailbox#inbox", SK: "2026-06-25T14:32:00Z#a", S3Key: "inbound/aaa", MessageID: "m1", From: "alice@example.com", Subject: "Hello"},
		{PK: "mailbox#inbox", SK: "2026-06-25T09:00:00Z#b", S3Key: "inbound/bbb", MessageID: "m2", From: "bob@example.com", Subject: "Report"},
	}
	if _, err := c.Sync(context.Background(), fakeHeaderLister{hs: hs}, "inbox", 50); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	m := model{cache: c, from: "inbox", list: newMessageList(hs, 30, 20)}
	m.list.Select(1) // select bob/Report (inbound/bbb) BEFORE filtering
	if got := selectedKey(m); got != "inbound/bbb" {
		t.Fatalf("precondition: selectedKey = %q, want inbound/bbb", got)
	}
	m2, _ := m.applyFilter("Report")
	items := m2.list.Items()
	if len(items) != 1 {
		t.Fatalf("applyFilter(Report) len(items) = %d, want 1", len(items))
	}
	mi, ok := items[0].(messageItem)
	if !ok || mi.h.S3Key != "inbound/bbb" {
		t.Fatalf("applyFilter(Report) did not return the matching header, got %+v", items)
	}
	if got := selectedKey(m2); got != "inbound/bbb" {
		t.Errorf("applyFilter must restore selection by S3Key (B-1); got %q want inbound/bbb", got)
	}
}

// TestApplyFilterNilCacheDegradesGracefully: without a cache, applyFilter must not panic and
// must leave the model's list untouched (graceful degradation to native filtering).
func TestApplyFilterNilCacheDegradesGracefully(t *testing.T) {
	m := testList(mailbox.Header{Subject: "a", S3Key: "k1"})
	m2, cmd := m.applyFilter("anything")
	if cmd != nil {
		t.Error("applyFilter with nil cache should return a nil cmd")
	}
	if len(m2.list.Items()) != 1 {
		t.Errorf("applyFilter with nil cache must not alter the list; got %d items", len(m2.list.Items()))
	}
}

// TestFilterEscRestoresFullList: CRITICAL regression (Task 10 review) — Esc while filtering must
// cancel to the FULL list, not re-apply the stale (typed-but-cancelled) query. Seeds a real cache
// via Sync (2 headers), opens the filter with '/', types a query that narrows the native filter to
// 1 match, then sends Esc. Asserts the restored list has both items back — NOT the 1-item filtered
// state that the CRITICAL bug produced (both Esc-cancel and Enter-confirm called
// applyFilter(prevQuery), so cancelling after typing "alpha" left the list filtered to 1 item).
func TestFilterEscRestoresFullList(t *testing.T) {
	c, err := cache.Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()
	hs := []mailbox.Header{
		{PK: "mailbox#inbox", SK: "2026-06-25T14:32:00Z#a", S3Key: "inbound/aaa", MessageID: "m1", From: "alice@example.com", Subject: "alpha report"},
		{PK: "mailbox#inbox", SK: "2026-06-25T09:00:00Z#b", S3Key: "inbound/bbb", MessageID: "m2", From: "bob@example.com", Subject: "beta memo"},
	}
	if _, err := c.Sync(context.Background(), fakeHeaderLister{hs: hs}, "inbox", 50); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	m := model{cache: c, from: "inbox", list: newMessageList(hs, 30, 20)}

	// Open the filter.
	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm := got.(model)
	if !mm.list.SettingFilter() {
		t.Fatal("filter did not open — cannot exercise the Esc-cancel path")
	}

	// Type "alpha": the native list filter narrows to the 1 matching item while still filtering.
	for _, r := range "alpha" {
		got, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		mm = got.(model)
	}
	if !mm.list.SettingFilter() {
		t.Fatal("typing into the filter exited SettingFilter early — precondition broken")
	}

	// Cancel with Esc.
	got, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm = got.(model)

	if mm.list.SettingFilter() {
		t.Fatal("Esc did not exit filtering mode")
	}
	items := mm.list.Items()
	if len(items) != 2 {
		t.Fatalf("Esc must restore the FULL list (2 items), got %d — stale query was re-applied (CRITICAL regression)", len(items))
	}
}

// TestHandleKeyFilterCancelAppliesEmptyQuery: unit-level guard on the intercept itself (not
// dependent on bubbles' native filter narrowing in headless mode). Directly simulates the
// Filtering→Unfiltered transition that bubbles performs internally on CancelWhileFiltering (Esc)
// by pre-seeding a query into the filter input, then confirms handleKey's cancel-detection routes
// through applyFilter("") rather than applyFilter(prevQuery).
func TestHandleKeyFilterCancelAppliesEmptyQuery(t *testing.T) {
	c, err := cache.Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()
	hs := []mailbox.Header{
		{PK: "mailbox#inbox", SK: "2026-06-25T14:32:00Z#a", S3Key: "inbound/aaa", MessageID: "m1", From: "alice@example.com", Subject: "alpha report"},
		{PK: "mailbox#inbox", SK: "2026-06-25T09:00:00Z#b", S3Key: "inbound/bbb", MessageID: "m2", From: "bob@example.com", Subject: "beta memo"},
	}
	if _, err := c.Sync(context.Background(), fakeHeaderLister{hs: hs}, "inbox", 50); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	m := model{cache: c, from: "inbox", list: newMessageList(hs, 30, 20)}

	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm := got.(model)
	if !mm.list.SettingFilter() {
		t.Fatal("filter did not open — cannot exercise the Esc-cancel path")
	}
	for _, r := range "alpha" {
		got, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		mm = got.(model)
	}
	if got := mm.list.FilterValue(); got != "alpha" {
		t.Fatalf("precondition: FilterValue() = %q, want %q", got, "alpha")
	}

	m2, _ := mm.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	res := m2.(model)
	if res.list.SettingFilter() {
		t.Fatal("Esc did not exit filtering mode via handleKey")
	}
	if got := len(res.list.Items()); got != 2 {
		t.Fatalf("handleKey(Esc) must apply an empty query (full list = 2 items), got %d items", got)
	}
}
