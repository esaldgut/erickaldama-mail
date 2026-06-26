package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"erickaldama-mail/internal/mailbox"
	"erickaldama-mail/internal/message"
)

// viewState tracks which panel is active.
type viewState int

const (
	viewList     viewState = iota
	viewReader             // reading a single message
	viewComposer           // composing / confirming send
)

// composer field indices — keep in sync with newComposer.
const (
	cTo      = 0
	cCc      = 1
	cBcc     = 2
	cSubject = 3
)

// composer holds the multi-field compose state.
type composer struct {
	inputs []textinput.Model // [To, Cc, Bcc, Subject] — Bcc VISIBLE (UX standard; BCC travels only in SES envelope)
	active int               // index of focused input
	body   string            // edited via $EDITOR (editorCmd flow)
}

// newComposer creates a fresh composer with To focused.
func newComposer() composer {
	labels := []string{"To", "Cc", "Bcc", "Subject"}
	ins := make([]textinput.Model, len(labels))
	for i, l := range labels {
		ti := textinput.New()
		ti.Prompt = l + ": "
		ins[i] = ti
	}
	ins[cTo].Focus()
	return composer{inputs: ins, active: cTo}
}

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
	case errMsg:
		m.statusMsg = "error: " + msg.err.Error()
		return m, nil
	case bodyLoadedMsg:
		m.body = msg.body
		m.view = viewReader
		m.scrollOffset = 0
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
type bodyLoadedMsg struct{ body string }

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
		// Live reader: load asynchronously.
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
			body, err := message.RenderRich(parsed, m.termWidth)
			if err != nil {
				body = message.RenderPlain(parsed)
			}
			return bodyLoadedMsg{body: body}
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
		}
	}
	switch key.Type {
	case tea.KeyEsc:
		m.view = viewList
	}
	return m, nil
}

// handleComposerKey handles composer input: Tab/Shift-Tab (field nav), Ctrl-E, Ctrl-S, confirm y/n.
func (m model) handleComposerKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// If waiting for confirmation (Ctrl-S already pressed):
	if m.confirming {
		if key.Type == tea.KeyRunes && len(key.Runes) == 1 {
			switch key.Runes[0] {
			case 'y':
				// Attempt Send if sender is available. Use message.Build so the BCC never leaks
				// into the raw MIME (audit BCC-1). Return a dedicated sentMsg (NOT bodyLoadedMsg) so
				// the Update handler clears confirming/compose — otherwise a later 'y' would re-send
				// (audit H-2).
				if m.sender != nil {
					c := m.compose
					from := m.from
					s := m.sender
					return m, func() tea.Msg {
						raw, dests, err := message.Build(message.BuildOpts{
							From:    from,
							To:      c.inputs[cTo].Value(),
							Cc:      c.inputs[cCc].Value(),
							Bcc:     c.inputs[cBcc].Value(),
							Subject: c.inputs[cSubject].Value(),
							Body:    c.body,
						})
						if err != nil {
							return errMsg{err}
						}
						id, err := s.Send(context.Background(), raw, dests)
						if err != nil {
							return errMsg{err}
						}
						return sentMsg{messageID: id}
					}
				}
				// No live sender in tests — just mark sent.
				m.sent = true
				m.confirming = false
				m.view = viewList
				m.statusMsg = "sent (no live sender configured)"
				return m, nil
			case 'n':
				m.confirming = false
				return m, nil
			}
		}
		return m, nil
	}

	// Normal composer keypresses:
	switch key.Type {
	case tea.KeyCtrlS:
		// Security control #1: Ctrl-S sets confirming=true, does NOT send.
		m.confirming = true
		return m, nil
	case tea.KeyCtrlE:
		// Open $EDITOR via tea.ExecProcess so Bubble Tea releases the terminal during edit (audit H-3).
		cmd, tmpPath, err := editorCmd(m.compose.body)
		if err != nil {
			m.statusMsg = "editor error: " + err.Error()
			return m, nil
		}
		return m, tea.ExecProcess(cmd, func(runErr error) tea.Msg {
			defer os.Remove(tmpPath)
			if runErr != nil {
				return errMsg{runErr}
			}
			edited, rErr := os.ReadFile(tmpPath)
			if rErr != nil {
				return errMsg{rErr}
			}
			return editorDoneMsg{body: string(edited)}
		})
	case tea.KeyTab:
		// Advance to next field (wraps around).
		old := m.compose.active
		next := (old + 1) % len(m.compose.inputs)
		m.compose.inputs[old].Blur()
		m.compose.inputs[next].Focus()
		m.compose.active = next
		return m, nil
	case tea.KeyShiftTab:
		// Retreat to previous field (wraps around).
		old := m.compose.active
		prev := (old - 1 + len(m.compose.inputs)) % len(m.compose.inputs)
		m.compose.inputs[old].Blur()
		m.compose.inputs[prev].Focus()
		m.compose.active = prev
		return m, nil
	case tea.KeyEsc:
		m.view = viewList
		return m, nil
	}

	// Forward key to active textinput.
	if len(m.compose.inputs) > 0 {
		var cmd tea.Cmd
		m.compose.inputs[m.compose.active], cmd = m.compose.inputs[m.compose.active].Update(key)
		return m, cmd
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
	sb.WriteString("\n")
	if m.statusMsg != "" {
		sb.WriteString(statusStyle.Render(m.statusMsg) + "\n")
	}
	sb.WriteString(normalStyle.Render("J/K scroll  Esc back  r compose  s summarize"))
	return sb.String()
}

func (m model) viewComposer() string {
	var sb strings.Builder
	sb.WriteString(headerStyle.Render("Composer") + "\n\n")
	// Render the 4 textinput fields.
	for _, ti := range m.compose.inputs {
		sb.WriteString(ti.View() + "\n")
	}
	// Preview body.
	if m.compose.body != "" {
		sb.WriteString("\n" + m.compose.body + "\n")
	}
	sb.WriteString("\n")
	if m.confirming {
		sb.WriteString(confirmStyle.Render("Send this message? [y/n]") + "\n")
	} else {
		if m.statusMsg != "" {
			sb.WriteString(statusStyle.Render(m.statusMsg) + "\n")
		}
		sb.WriteString(normalStyle.Render("Tab/Shift-Tab switch field  Ctrl-E edit body  Ctrl-S confirm send  Esc back"))
	}
	return sb.String()
}

// ── helpers ───────────────────────────────────────────────────────────────────

// replyAllCc returns the original To+Cc addresses minus self, comma-joined. Matches the CLI cmd/mail.
func replyAllCc(parsedTo, parsedCc, self string) string {
	seen := map[string]bool{}
	if self != "" {
		seen[strings.ToLower(self)] = true
	}
	var out []string
	for _, a := range append(message.SplitAddrs(parsedTo), message.SplitAddrs(parsedCc)...) {
		la := strings.ToLower(a)
		if !seen[la] {
			seen[la] = true
			out = append(out, a)
		}
	}
	return strings.Join(out, ",")
}

// editorCmd builds the $EDITOR *exec.Cmd over a temp file seeded with content, plus the temp path. It uses
// an argv-slice (no shell → no injection) and a random temp name (os.CreateTemp, NOT derived from untrusted
// data). The caller runs it via tea.ExecProcess so Bubble Tea releases/restores the terminal (audit H-3:
// running the editor inside a plain tea.Cmd corrupts the AltScreen).
func editorCmd(content string) (*exec.Cmd, string, error) {
	f, err := os.CreateTemp("", "mail-tui-*.txt")
	if err != nil {
		return nil, "", err
	}
	_, _ = f.WriteString(content)
	f.Close()
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nvim"
	}
	fields := strings.Fields(editor) // e.g. "code -w" → ["code","-w"]
	args := append(fields[1:], f.Name())
	cmd := exec.Command(fields[0], args...) // no sh -c; tea.ExecProcess wires stdio + terminal handoff
	return cmd, f.Name(), nil
}
