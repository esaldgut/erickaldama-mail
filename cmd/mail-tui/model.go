package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

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

// model is the Bubble Tea Elm model for the TUI.
type model struct {
	view       viewState
	headers    []mailbox.Header
	selected   int
	lastKey    rune   // for double-g detection (each 'g' is a separate KeyMsg)
	body       string // reader content (RenderRich output)
	confirming bool   // composer: Ctrl-S → true; 'y' sends, 'n' cancels
	sent       bool   // set true only after a real Send
	reader     *mailbox.Reader
	sender     *mailbox.Sender
	// scroll offset for reader view
	scrollOffset int
	// compose draft body
	composeDraft string
	// status message shown at the bottom
	statusMsg string
}

// Init satisfies tea.Model. No I/O at startup — the TUI loads mail lazily on Enter.
func (m model) Init() tea.Cmd {
	return nil
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
		m.composeDraft = ""
		m.view = viewList
		m.statusMsg = "reply sent: " + msg.messageID
		return m, nil
	case editorDoneMsg:
		// Editor finished: update the compose draft and stay in the composer (do NOT open the reader).
		m.composeDraft = msg.body
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
			m.composeDraft = replyDraft(m.headers, m.selected) // pre-populate To:/Subject: from the selected header (audit H-4)
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
			body, err := message.RenderRich(parsed)
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
			m.composeDraft = replyDraft(m.headers, m.selected) // pre-populate To:/Subject: (audit H-4)
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

// handleComposerKey handles composer input: Ctrl-E, Ctrl-S, confirm y/n.
func (m model) handleComposerKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// If waiting for confirmation (Ctrl-S already pressed):
	if m.confirming {
		if key.Type == tea.KeyRunes && len(key.Runes) == 1 {
			switch key.Runes[0] {
			case 'y':
				// Attempt Send if sender is available. Return a dedicated sentMsg (NOT bodyLoadedMsg) so the
				// Update handler clears confirming/draft — otherwise a later 'y' would re-send (audit H-2).
				if m.sender != nil {
					raw := []byte(m.composeDraft)
					s := m.sender
					return m, func() tea.Msg {
						id, err := s.Send(context.Background(), raw)
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
		cmd, tmpPath, err := editorCmd(m.composeDraft)
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
	sb.WriteString(m.composeDraft)
	sb.WriteString("\n\n")
	if m.confirming {
		sb.WriteString(confirmStyle.Render("Send this message? [y/n]") + "\n")
	} else {
		if m.statusMsg != "" {
			sb.WriteString(statusStyle.Render(m.statusMsg) + "\n")
		}
		sb.WriteString(normalStyle.Render("Ctrl-E edit  Ctrl-S confirm send  Esc back"))
	}
	return sb.String()
}

// ── openEditor ───────────────────────────────────────────────────────────────

// replyDraft pre-populates a composer draft with To:/Subject: derived from the selected header, so the user
// sees who they are replying to (audit H-4). Pure + bounds-checked. The From line is intentionally absent:
// the verified sender identity is enforced by SES (ses:FromAddress), not by editable draft text.
func replyDraft(headers []mailbox.Header, selected int) string {
	if selected < 0 || selected >= len(headers) {
		return ""
	}
	h := headers[selected]
	subject := h.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	return fmt.Sprintf("To: %s\nSubject: %s\n\n", h.From, subject)
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
