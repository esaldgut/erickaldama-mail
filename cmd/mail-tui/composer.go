package main

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"erickaldama-mail/internal/message"
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

// viewComposer renders the composer view.
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
