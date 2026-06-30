package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"erickaldama-mail/internal/mailbox"
	"erickaldama-mail/internal/message"
	"erickaldama-mail/internal/render"
)

// viewState tracks which panel is active.
type viewState int

const (
	viewList     viewState = iota
	viewReader             // reading a single message
	viewComposer           // composing / confirming send
)

// mailSender is the minimal interface the TUI needs to send mail. *mailbox.Sender satisfies it.
type mailSender interface {
	Send(ctx context.Context, raw []byte, destinations []string) (string, error)
}

// model is the Bubble Tea Elm model for the TUI.
type model struct {
	view       viewState
	headers    []mailbox.Header
	selected   int
	lastKey    rune   // for double-g detection (each 'g' is a separate KeyMsg)
	body       string // reader content (RenderRich output)
	confirming bool   // composer: Ctrl-S → true; 'y' sends, 'n' cancels
	sent       bool   // set true only after a real Send
	from       string // sender identity (from config.DefaultFrom); enforced by SES ses:FromAddress
	reader     *mailbox.Reader
	sender     mailSender
	// scroll offset for reader view
	scrollOffset int
	// compose state: multi-field textinput composer
	compose composer
	// status message shown at the bottom
	statusMsg string
	// termWidth holds the current terminal width for dynamic word-wrap in RenderRich (T5 wires WindowSizeMsg).
	termWidth int
	// showImages is true when the user has pressed 'i' to render inline images.
	showImages bool
	// loadRemote controls whether remote images are allowed through SanitizeHTML (toggled by 'R').
	loadRemote bool
	// currentParsed is the last fully-parsed message (used for re-render on resize and image render).
	currentParsed *message.Parsed
	// rawHTML stores the original unsanitized HTML from the last message load (for idempotent 'R' toggle).
	rawHTML string
	// imageBlobs holds ANSI strings returned by render.RenderImage, one per InlineImage.
	imageBlobs []string
}

// Init satisfies tea.Model. Start the textinput blink timer.
func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink)
}

// Update handles all keypresses for all views. Returns (tea.Model, tea.Cmd).
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.termWidth = msg.Width
		// P-7: guard by currentParsed, not a rawMD string.
		if m.view == viewReader && m.currentParsed != nil {
			body, _ := message.RenderRich(m.currentParsed, m.termWidth)
			m.body = body
		}
		return m, nil
	case errMsg:
		m.statusMsg = "error: " + msg.err.Error()
		return m, nil
	case bodyLoadedMsg:
		// P-5 (D4): sanitize the HTML FIRST before rendering. Store raw HTML for idempotent 'R' toggle.
		m.rawHTML = msg.parsed.TextHTML
		m.loadRemote = false
		san, err := message.SanitizeHTML(m.rawHTML, false)
		if err != nil {
			// H-5: do not silently use empty/unsanitized HTML; fall back to plain text.
			m.statusMsg = "error sanitizando el correo: " + err.Error()
			clean := *msg.parsed
			clean.TextHTML = ""
			m.currentParsed = &clean
			m.body = "" // WB-3: no dejar el body del correo anterior si falla la sanitización
			if msg.parsed.TextPlain != "" {
				body, _ := message.RenderRich(&clean, m.termWidth)
				m.body = body
			}
			m.imageBlobs = nil
			m.showImages = false
			m.view = viewReader
			m.scrollOffset = 0
			return m, nil
		}
		clean := *msg.parsed
		clean.TextHTML = san.HTML
		m.currentParsed = &clean
		body, _ := message.RenderRich(&clean, m.termWidth)
		m.body = body
		m.imageBlobs = nil
		m.showImages = false
		m.view = viewReader
		m.scrollOffset = 0
		return m, nil
	case imagesRenderedMsg:
		m.imageBlobs = msg.blobs
		return m, nil
	case sentMsg:
		// A successful live send: clear the confirm state so a later 'y' cannot fire a second SES send
		// without a fresh Ctrl-S confirmation (audit H-2). Return to the list with a status line.
		m.sent = true
		m.confirming = false
		m.compose = newComposer() // reset to blank composer (inputs always initialized)
		m.view = viewList
		m.statusMsg = "reply sent: " + msg.messageID
		return m, nil
	case editorDoneMsg:
		// Editor finished: update the compose body and stay in the composer (do NOT open the reader).
		m.compose.body = msg.body
		return m, nil
	case replyReadyMsg:
		// Async reply-all fetch complete: switch to composer with pre-filled fields.
		m.compose = msg.c
		m.view = viewComposer
		return m, nil
	}
	return m, nil
}

// errMsg carries an async error back into the model.
type errMsg struct{ err error }

// bodyLoadedMsg is sent when the reader finishes loading a message body.
// It carries the full *message.Parsed so the Update handler can sanitize (P-5) and store currentParsed.
type bodyLoadedMsg struct{ parsed *message.Parsed }

// imagesRenderedMsg is sent when async image rendering via render.RenderImage completes (P-6).
type imagesRenderedMsg struct{ blobs []string }

// sentMsg signals a successful live SES send (distinct from bodyLoadedMsg so the confirm state is cleared).
type sentMsg struct{ messageID string }

// editorDoneMsg carries the edited draft back after the editor (run via tea.ExecProcess) exits.
type editorDoneMsg struct{ body string }

// handleKey dispatches keystrokes per active view.
func (m model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.view {
	case viewList:
		return m.handleListKey(key)
	case viewReader:
		return m.handleReaderKey(key)
	case viewComposer:
		return m.handleComposerKey(key)
	}
	return m, nil
}

// handleListKey handles Vim-motions in the list view.
func (m model) handleListKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Helper: any key other than 'g' resets lastKey.
	resetLastKey := func() {
		m.lastKey = 0
	}

	if key.Type == tea.KeyRunes && len(key.Runes) == 1 {
		ch := key.Runes[0]
		switch ch {
		case 'j':
			resetLastKey()
			if m.selected < len(m.headers)-1 {
				m.selected++
			}
			return m, nil
		case 'k':
			resetLastKey()
			if m.selected > 0 {
				m.selected--
			}
			return m, nil
		case 'g':
			if m.lastKey == 'g' {
				// double-g → go to top
				m.selected = 0
				m.lastKey = 0
			} else {
				m.lastKey = 'g'
			}
			return m, nil
		case 'G':
			resetLastKey()
			if len(m.headers) > 0 {
				m.selected = len(m.headers) - 1
			}
			return m, nil
		case 'r':
			resetLastKey()
			c := newComposer()
			// Pre-fill from header (bounds-checked). Deep reply-all requires the parsed message (Step 4).
			if m.selected >= 0 && m.selected < len(m.headers) {
				h := m.headers[m.selected]
				subject := h.Subject
				if !strings.HasPrefix(strings.ToLower(subject), "re:") {
					subject = "Re: " + subject
				}
				c.inputs[cTo].SetValue(h.From)
				c.inputs[cSubject].SetValue(subject)
			}
			// If a live reader is available, open the message and parse it for full reply-all (Cc).
			if m.reader != nil && m.selected >= 0 && m.selected < len(m.headers) && m.headers[m.selected].S3Key != "" {
				s3Key := m.headers[m.selected].S3Key
				r := m.reader
				self := m.from
				orig := c // capture pre-filled compose
				return m, func() tea.Msg {
					rc, err := r.Open(context.Background(), s3Key)
					if err != nil {
						return replyReadyMsg{c: orig} // fallback: use header-only pre-fill
					}
					defer rc.Close()
					parsed, err := message.Parse(rc)
					if err != nil {
						return replyReadyMsg{c: orig}
					}
					orig.inputs[cTo].SetValue(parsed.From)
					orig.inputs[cCc].SetValue(replyAllCc(parsed.To, parsed.Cc, self))
					inReplyTo, _, subject := message.ReplyHeaders(parsed)
					orig.inputs[cSubject].SetValue(subject)
					_ = inReplyTo // threading headers stored in body/metadata in future
					return replyReadyMsg{c: orig}
				}
			}
			m.compose = c
			m.view = viewComposer
			return m, nil
		case 's':
			resetLastKey()
			m.statusMsg = "summarize: not connected (run with --backend)"
			return m, nil
		case 'a':
			resetLastKey()
			m.statusMsg = "agent: not connected (run with --backend)"
			return m, nil
		case 'q':
			resetLastKey()
			return m, tea.Quit
		}
		resetLastKey()
		return m, nil
	}

	switch key.Type {
	case tea.KeyDown:
		m.lastKey = 0
		if m.selected < len(m.headers)-1 {
			m.selected++
		}
	case tea.KeyUp:
		m.lastKey = 0
		if m.selected > 0 {
			m.selected--
		}
	case tea.KeyEnter:
		m.lastKey = 0
		if len(m.headers) == 0 {
			return m, nil
		}
		h := m.headers[m.selected]
		if m.reader == nil || h.S3Key == "" {
			// No live reader or no S3 key: switch view immediately with empty body.
			m.view = viewReader
			m.body = fmt.Sprintf("Subject: %s\nFrom: %s\nDate: %s\n\n(no body — not connected)", h.Subject, h.From, h.Date)
			m.scrollOffset = 0
			return m, nil
		}
		// Live reader: load asynchronously. bodyLoadedMsg carries the full *Parsed so Update can
		// sanitize (P-5) and store currentParsed for resize/image keys.
		s3Key := h.S3Key
		r := m.reader
		return m, func() tea.Msg {
			rc, err := r.Open(context.Background(), s3Key)
			if err != nil {
				return errMsg{err}
			}
			defer rc.Close()
			parsed, err := message.Parse(rc)
			if err != nil {
				return errMsg{err}
			}
			return bodyLoadedMsg{parsed: parsed}
		}
	case tea.KeyEsc:
		m.lastKey = 0
		// Nothing to escape from in list — ignore.
	}
	return m, nil
}

// replyReadyMsg is sent when the async reply-all fetch finishes.
type replyReadyMsg struct{ c composer }

// handleReaderKey handles Vim-motions in the reader view.
func (m model) handleReaderKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Type == tea.KeyRunes && len(key.Runes) == 1 {
		switch key.Runes[0] {
		case 'J':
			m.scrollOffset++
			return m, nil
		case 'K':
			if m.scrollOffset > 0 {
				m.scrollOffset--
			}
			return m, nil
		case 'r':
			c := newComposer()
			// Pre-fill from current header (bounds-checked). Deep reply-all via reader open.
			if m.selected >= 0 && m.selected < len(m.headers) {
				h := m.headers[m.selected]
				subject := h.Subject
				if !strings.HasPrefix(strings.ToLower(subject), "re:") {
					subject = "Re: " + subject
				}
				c.inputs[cTo].SetValue(h.From)
				c.inputs[cSubject].SetValue(subject)
			}
			if m.reader != nil && m.selected >= 0 && m.selected < len(m.headers) && m.headers[m.selected].S3Key != "" {
				s3Key := m.headers[m.selected].S3Key
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
					_, _, subject := message.ReplyHeaders(parsed)
					orig.inputs[cSubject].SetValue(subject)
					return replyReadyMsg{c: orig}
				}
			}
			m.compose = c
			m.view = viewComposer
			return m, nil
		case 's':
			m.statusMsg = "summarize: not connected (run with --backend)"
			return m, nil
		case 'i':
			// P-6: image render is ASYNC via tea.Cmd — never block the event loop (chafa can take up to 5s).
			// H-1: guard FIRST — only set showImages=true when there are images to render, so a stale
			// imagesRenderedMsg from a prior message cannot surface on a message with no images.
			if m.currentParsed == nil || len(m.currentParsed.InlineImages) == 0 {
				m.statusMsg = "no hay imágenes inline en este correo"
				return m, nil
			}
			m.showImages = true
			imgs := m.currentParsed.InlineImages
			// H-2: guard against termWidth==0 (WindowSizeMsg not yet received) so chafa never gets 0x0.
			tw := m.termWidth
			if tw == 0 {
				tw = 80
			}
			w, h := tw, tw/2
			return m, func() tea.Msg {
				var blobs []string
				for _, im := range imgs {
					blobs = append(blobs, render.RenderImage(context.Background(), im.Data, w, h))
				}
				return imagesRenderedMsg{blobs: blobs}
			}
		case 'R': // P-5: re-sanitize from ORIGINAL rawHTML (m.rawHTML) — idempotent N times.
			m.loadRemote = !m.loadRemote
			if m.currentParsed != nil && m.rawHTML != "" {
				san, err := message.SanitizeHTML(m.rawHTML, m.loadRemote)
				if err != nil {
					// H-5: sanitize failed; do not overwrite body with empty/unsafe HTML.
					m.statusMsg = "error sanitizando el correo: " + err.Error()
					if m.currentParsed.TextPlain != "" {
						clean := *m.currentParsed
						clean.TextHTML = ""
						m.currentParsed = &clean
						body, _ := message.RenderRich(&clean, m.termWidth)
						m.body = body
					}
					return m, nil
				}
				clean := *m.currentParsed
				clean.TextHTML = san.HTML
				m.currentParsed = &clean
				body, _ := message.RenderRich(&clean, m.termWidth)
				m.body = body
			}
			return m, nil
		}
	}
	switch key.Type {
	case tea.KeyEsc:
		m.view = viewList
	}
	return m, nil
}

// ── View ─────────────────────────────────────────────────────────────────────

var (
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	normalStyle   = lipgloss.NewStyle()
	headerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	statusStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	confirmStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
)

// View renders the current state to a string (Bubble Tea v1.x API).
func (m model) View() string {
	switch m.view {
	case viewList:
		return m.viewList()
	case viewReader:
		return m.viewReader()
	case viewComposer:
		return m.viewComposer()
	}
	return ""
}

func (m model) viewList() string {
	var sb strings.Builder
	sb.WriteString(headerStyle.Render("Mail — Inbox") + "\n\n")
	for i, h := range m.headers {
		line := fmt.Sprintf("  %s  %-30s  %s", h.Date, h.From, h.Subject)
		if i == m.selected {
			sb.WriteString(selectedStyle.Render("> "+line) + "\n")
		} else {
			sb.WriteString(normalStyle.Render("  "+line) + "\n")
		}
	}
	sb.WriteString("\n")
	if m.statusMsg != "" {
		sb.WriteString(statusStyle.Render(m.statusMsg) + "\n")
	}
	sb.WriteString(normalStyle.Render("j/k move  gg top  G bottom  Enter read  r compose  s summarize  a agent  q quit"))
	return sb.String()
}

func (m model) viewReader() string {
	var sb strings.Builder
	sb.WriteString(headerStyle.Render("Reader") + "\n\n")
	// Scroll: split by lines and apply offset.
	lines := strings.Split(m.body, "\n")
	start := m.scrollOffset
	if start > len(lines) {
		start = len(lines)
	}
	for _, l := range lines[start:] {
		sb.WriteString(l + "\n")
	}
	if m.showImages && len(m.imageBlobs) > 0 {
		sb.WriteString(strings.Join(m.imageBlobs, "\n") + "\n")
	}
	sb.WriteString("\n")
	if m.statusMsg != "" {
		sb.WriteString(statusStyle.Render(m.statusMsg) + "\n")
	}
	sb.WriteString(normalStyle.Render("J/K scroll  Esc back  r compose  s summarize  i images  R remote-imgs"))
	return sb.String()
}
