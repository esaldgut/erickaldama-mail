package main

import (
	"context"
	"strings"
	"time"

	bkey "github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"erickaldama-mail/internal/cache"
	"erickaldama-mail/internal/mailbox"
	"erickaldama-mail/internal/message"
	render "erickaldama-mail/internal/render"
)

// mailSender is the minimal interface the TUI needs to send mail. *mailbox.Sender satisfies it.
type mailSender interface {
	Send(ctx context.Context, raw []byte, destinations []string) (string, error)
}

type focusState int

const (
	focusList focusState = iota
	focusReader
)

type mode int

const (
	modeBrowse mode = iota
	modeComposer
)

type model struct {
	mode  mode
	focus focusState

	list     list.Model
	viewport viewport.Model
	spinner  spinner.Model

	inflight        int    // async ops in flight; spinner shows while > 0 (D6)
	loadGen         int    // generation of the in-flight body load; stale bodyLoadedMsg discarded (A-4)
	loadDebounceGen int    // generation of the latest debounce tick (B-1/C-debounce)
	loadPendingKey  string // the S3Key the latest debounce tick is waiting to load
	vpReady         bool

	from    string // sending identity (cfg.DefaultFrom) — used for reply-all self-strip, NOT for cache keys
	mailbox string // mailbox being browsed (cfg.Mailboxes[0]) — the cache write-path key; applyFilter MUST read with this, not from
	reader  *mailbox.Reader
	sender  mailSender
	cache   *cache.Cache // v0.5: filter (/) queries FTS5 via this cache; nil-safe (degrades to native filter)

	compose    composer
	confirming bool
	sent       bool

	statusMsg     string
	termWidth     int
	termHeight    int
	showImages    bool
	loadRemote    bool
	currentParsed *message.Parsed
	rawHTML       string
	sanitizedBody string // last sanitized RenderRich output — base for imagesRenderedMsg (SAN invariant)
	imageBlobs    []string
}

// Lipgloss styles used across model.go and composer.go
var (
	headerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	statusStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	confirmStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	normalStyle  = lipgloss.NewStyle()
)

// EFI-1: remove spinner.Tick from Init; the tick is started on the first inflight 0→1 transition
// (in loadDebounceMsg and 'i' handlers). Running it always wasted CPU and conflicted with A-3.
func (m model) Init() tea.Cmd { return textinput.Blink }

// ── Message types ─────────────────────────────────────────────────────────────

// errMsg carries an async error back into the model.
type errMsg struct{ err error }

// bodyLoadedMsg is sent when the reader finishes loading a message body.
type bodyLoadedMsg struct {
	parsed *message.Parsed
	gen    int
}

// loadDebounceMsg fires after the debounce interval; it loads only if its gen still matches
// the model's loadDebounceGen (i.e. the user stopped moving). Prevents N+1 S3 reads (C-debounce/B-1).
type loadDebounceMsg struct {
	key string
	gen int
}

// imagesRenderedMsg is sent when async image rendering via render.RenderImage completes.
type imagesRenderedMsg struct {
	blobs []string
	gen   int
}

// sentMsg signals a successful live SES send.
type sentMsg struct{ messageID string }

// editorDoneMsg carries the edited draft back after the editor exits.
type editorDoneMsg struct{ body string }

// replyReadyMsg is sent when the async reply-all fetch finishes.
type replyReadyMsg struct{ c composer }

// ── selectedKey ───────────────────────────────────────────────────────────────

func selectedKey(m model) string {
	it, ok := m.list.SelectedItem().(messageItem)
	if !ok {
		return ""
	}
	return it.S3Key()
}

// applyFilter rebuilds the list from the cache: Search(query) when non-empty, List otherwise.
// Preserves the current selection by S3Key (B-1) when the selected message survives the filter.
// nil-safe: without a cache, this is a no-op — leaves native filtering as-is (graceful degradation).
func (m model) applyFilter(query string) (model, tea.Cmd) {
	if m.cache == nil {
		return m, nil
	}
	var hs []mailbox.Header
	var err error
	if strings.TrimSpace(query) == "" {
		hs, err = m.cache.List(m.mailbox, 200)
	} else {
		hs, err = m.cache.Search(m.mailbox, query, 200)
	}
	if err != nil {
		m.statusMsg = "search error: " + err.Error()
		return m, nil
	}
	prevKey := selectedKey(m)
	m.list = newMessageList(hs, m.list.Width(), m.list.Height())
	// Restore selection by S3Key if still present (B-1: never by Index).
	for i, it := range m.list.Items() {
		if mi, ok := it.(messageItem); ok && mi.h.S3Key == prevKey {
			m.list.Select(i)
			break
		}
	}
	return m, nil
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleResize(msg)
	case tea.MouseMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg) // A-1b
		return m, cmd
	case spinner.TickMsg:
		if m.inflight == 0 {
			return m, nil // no work in flight → let the tick loop die (M-1: no perpetual wakeup)
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg) // A-3: only here
		return m, cmd
	case loadDebounceMsg:
		// C-debounce/B-1: fire the S3 load ONLY if this tick is still the latest (user stopped moving).
		if msg.gen != m.loadDebounceGen || msg.key == "" || msg.key != m.loadPendingKey {
			return m, nil
		}
		m.loadGen++
		gen := m.loadGen
		startSpinner := m.inflight == 0
		m.inflight++
		r := m.reader
		s3Key := msg.key
		load := func() tea.Msg {
			if r == nil {
				return bodyLoadedMsg{parsed: &message.Parsed{TextPlain: "(no body — not connected)"}, gen: gen}
			}
			rc, err := r.Open(context.Background(), s3Key)
			if err != nil {
				return errMsg{err}
			}
			defer rc.Close()
			parsed, err := message.Parse(rc)
			if err != nil {
				return errMsg{err}
			}
			return bodyLoadedMsg{parsed: parsed, gen: gen}
		}
		if startSpinner {
			return m, tea.Batch(load, m.spinner.Tick)
		}
		return m, load
	case bodyLoadedMsg:
		if msg.gen != m.loadGen {
			return m, nil // A-4
		}
		return m.applyBody(msg.parsed)
	case imagesRenderedMsg:
		if m.inflight > 0 {
			m.inflight--
		}
		if msg.gen != m.loadGen {
			return m, nil // M-2: stale render (user navigated away) — discard, don't paint old images on the new body
		}
		m.imageBlobs = msg.blobs
		m.viewport.SetContent(m.sanitizedBody + "\n" + strings.Join(msg.blobs, "\n")) // SAN: base is sanitizedBody
		return m, nil
	case sentMsg:
		m.sent = true
		m.confirming = false
		m.compose = newComposer()
		m.mode = modeBrowse
		if m.inflight > 0 {
			m.inflight--
		}
		m.statusMsg = "reply sent: " + msg.messageID
		return m, nil
	case replyReadyMsg:
		m.compose = msg.c
		m.mode = modeComposer
		return m, nil
	case editorDoneMsg:
		m.compose.body = msg.body
		return m, nil
	case errMsg:
		if m.inflight > 0 {
			m.inflight--
		}
		m.statusMsg = "error: " + msg.err.Error()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeComposer {
		return m.handleComposerKey(key)
	}
	if m.list.SettingFilter() { // D3: all keys to the list; Tab here applies the filter (B-2)
		prevQuery := m.list.FilterValue()                               // == m.list.FilterInput.Value(); capture BEFORE delegating
		cancel := bkey.Matches(key, m.list.KeyMap.CancelWhileFiltering) // Esc: cancel, don't apply the stale query
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(key) // bubbles may transition Filtering→(FilterApplied|Unfiltered) HERE (B-2: Tab/Enter confirm; Esc cancels)
		if !m.list.SettingFilter() {     // transition Filtering→(Applied|Unfiltered): user confirmed or cancelled
			q := prevQuery
			if cancel {
				q = "" // Esc restores the full list, NOT the typed-but-cancelled query
			}
			m2, c2 := m.applyFilter(q) // reload via cache.Search (or List if empty)
			return m2, tea.Batch(cmd, c2)
		}
		return m, cmd // still typing the filter query
	}
	if key.Type == tea.KeyTab { // B-2: only toggles when NOT filtering
		if m.focus == focusList {
			m.focus = focusReader
		} else {
			m.focus = focusList
		}
		return m, nil
	}
	if key.Type == tea.KeyRunes && len(key.Runes) == 1 {
		switch key.Runes[0] {
		case 'q':
			return m, tea.Quit
		case 'r':
			return m.startReply()
		case 's':
			m.statusMsg = "summarize: not connected (run with --backend)"
			return m, nil
		case 'a':
			m.statusMsg = "agent: not connected (run with --backend)"
			return m, nil
		}
		// reader-only keys i/R handled in the focusReader branch below.
	}
	if m.focus == focusReader {
		return m.handleReaderRune(key) // i/R then viewport scroll
	}
	// focusList: detect selection change by S3Key (NOT Index — poisoned by '/', B-1) and DEBOUNCE.
	prev := selectedKey(m)
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(key)
	next := selectedKey(m)
	if next != prev && next != "" && !m.list.SettingFilter() {
		m.loadDebounceGen++
		m.loadPendingKey = next
		gen := m.loadDebounceGen
		pendingKey := next
		// 150ms debounce: only the tick whose gen still matches will fire startLoad (C-debounce).
		tick := tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg {
			return loadDebounceMsg{key: pendingKey, gen: gen}
		})
		return m, tea.Batch(cmd, tick)
	}
	return m, cmd
}

func (m model) handleReaderRune(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Type == tea.KeyRunes && len(key.Runes) == 1 {
		switch key.Runes[0] {
		case 'i':
			if m.currentParsed == nil || len(m.currentParsed.InlineImages) == 0 {
				m.statusMsg = "no hay imágenes inline en este correo"
				return m, nil
			}
			m.showImages = true
			imgs := m.currentParsed.InlineImages
			_, readerW, _ := panelDims(m.termWidth, m.termHeight)
			w, h := readerW, readerW/2
			startSpinner := m.inflight == 0
			m.inflight++
			gen := m.loadGen // M-2: tag the render with the current message's generation
			renderImgs := func() tea.Msg {
				var blobs []string
				for _, im := range imgs {
					blobs = append(blobs, render.RenderImage(context.Background(), im.Data, w, h))
				}
				return imagesRenderedMsg{blobs: blobs, gen: gen}
			}
			if startSpinner {
				return m, tea.Batch(renderImgs, m.spinner.Tick)
			}
			return m, renderImgs
		case 'R':
			m.loadRemote = !m.loadRemote
			if m.currentParsed != nil && m.rawHTML != "" {
				_, readerW, _ := panelDims(m.termWidth, m.termHeight)
				san, err := message.SanitizeHTML(m.rawHTML, m.loadRemote)
				if err != nil {
					m.statusMsg = "error sanitizando el correo: " + err.Error()
					return m, nil
				}
				clean := *m.currentParsed
				clean.TextHTML = san.HTML
				m.currentParsed = &clean
				// BPR-1: surface RenderRich errors as status message + fallback to TextPlain.
				body, rerr := message.RenderRich(&clean, readerW)
				if rerr != nil {
					m.statusMsg = "error al renderizar: " + rerr.Error()
					body = clean.TextPlain
				}
				m.sanitizedBody = body
				m.viewport.SetContent(body) // SAN: re-sanitized, never raw
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(key) // scroll j/k/pgup/etc.
	return m, cmd
}

func (m model) applyBody(parsed *message.Parsed) (tea.Model, tea.Cmd) {
	if m.inflight > 0 {
		m.inflight--
	}
	m.rawHTML = parsed.TextHTML
	m.loadRemote = false
	_, readerW, _ := panelDims(m.termWidth, m.termHeight)
	san, err := message.SanitizeHTML(m.rawHTML, false)
	if err != nil {
		m.statusMsg = "error sanitizando el correo: " + err.Error()
		clean := *parsed
		clean.TextHTML = ""
		m.currentParsed = &clean
		body := ""
		if parsed.TextPlain != "" {
			body, _ = message.RenderRich(&clean, readerW)
		}
		m.sanitizedBody = body
		m.viewport.SetContent(body)
		m.viewport.GotoTop()
		m.imageBlobs = nil
		m.showImages = false
		m.focus = focusReader
		return m, nil
	}
	clean := *parsed
	clean.TextHTML = san.HTML
	m.currentParsed = &clean
	// BPR-1: surface RenderRich errors as status message + fallback to TextPlain.
	body, rerr := message.RenderRich(&clean, readerW)
	if rerr != nil {
		m.statusMsg = "error al renderizar: " + rerr.Error()
		body = clean.TextPlain
	}
	m.sanitizedBody = body
	m.viewport.SetContent(body) // SAN: POST-RenderRich sanitized, never rawHTML
	m.viewport.GotoTop()
	m.imageBlobs = nil
	m.showImages = false
	m.focus = focusReader
	return m, nil
}

func (m model) startReply() (tea.Model, tea.Cmd) {
	it, ok := m.list.SelectedItem().(messageItem)
	if !ok {
		return m, nil
	}
	c := newComposer()
	subject := it.h.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	c.inputs[cTo].SetValue(it.h.From)
	c.inputs[cSubject].SetValue(subject)
	if m.reader != nil && it.h.S3Key != "" {
		s3Key := it.h.S3Key
		r := m.reader
		self := m.from
		orig := c
		return m, func() tea.Msg {
			rc, err := r.Open(context.Background(), s3Key)
			if err != nil {
				return replyReadyMsg{c: orig}
			}
			defer rc.Close()
			parsed, err := message.Parse(rc)
			if err != nil {
				return replyReadyMsg{c: orig}
			}
			orig.inputs[cTo].SetValue(parsed.From)
			orig.inputs[cCc].SetValue(replyAllCc(parsed.To, parsed.Cc, self))
			_, _, subj := message.ReplyHeaders(parsed)
			orig.inputs[cSubject].SetValue(subj)
			return replyReadyMsg{c: orig}
		}
	}
	m.compose = c
	m.mode = modeComposer
	return m, nil
}

// ── handleResize ──────────────────────────────────────────────────────────────

func (m model) handleResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	firstResize := !m.vpReady
	m.termWidth = msg.Width
	m.termHeight = msg.Height
	m.vpReady = true
	listW, readerW, panelH := panelDims(msg.Width, msg.Height)
	m.list.SetSize(listW, panelH)
	m.viewport.Width = readerW
	m.viewport.Height = panelH
	if m.currentParsed != nil {
		// BPR-1: surface RenderRich errors + fallback to TextPlain.
		body, rerr := message.RenderRich(m.currentParsed, readerW)
		if rerr != nil {
			m.statusMsg = "error al renderizar: " + rerr.Error()
			body = m.currentParsed.TextPlain
		}
		m.sanitizedBody = body
		// DEU-2: re-attach image blobs on resize so showImages mode doesn't go blank.
		// SAN: body is the sanitized RenderRich output; blobs are cid: local references, never rawHTML.
		content := body
		if m.showImages && len(m.imageBlobs) > 0 {
			content = body + "\n" + strings.Join(m.imageBlobs, "\n")
		}
		m.viewport.SetContent(content)
	}
	// ROB-1: on the first resize, auto-load the currently-selected message so the reader
	// pane isn't blank at startup. Uses the same debounce path (no special-casing the load).
	if firstResize && m.currentParsed == nil {
		if key := selectedKey(m); key != "" {
			m.loadDebounceGen++
			m.loadPendingKey = key
			gen := m.loadDebounceGen
			k := key
			return m, tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg {
				return loadDebounceMsg{key: k, gen: gen}
			})
		}
	}
	return m, nil
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m model) View() string {
	switch m.mode {
	case modeComposer:
		return m.viewComposer()
	default:
		if !m.vpReady {
			return "cargando…"
		}
		return renderTwoPane(m)
	}
}
