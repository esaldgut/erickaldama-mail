// Package main is the mail-tui binary: a full-screen Bubble Tea TUI for the mail client.
// Vim-motions: list (j/k/gg/G/Enter/r/s/a/q), reader (J/K/Esc/r/s), composer (Ctrl-E/Ctrl-S/y/n).
// AWS wiring: identical profile flags as cmd/mail, via internal/wire.
// Run with --help for usage.
package main

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"erickaldama-mail/internal/mailbox"
	"erickaldama-mail/internal/wire"
)

func main() {
	// Flags (simple, no cobra — TUI does not need subcommands).
	readProfile := "mail-client-read"
	sendProfile := "mail-sender"
	mailboxName := "inbox"

	for i, arg := range os.Args[1:] {
		switch arg {
		case "--read-profile":
			if i+2 < len(os.Args) {
				readProfile = os.Args[i+2]
			}
		case "--send-profile":
			if i+2 < len(os.Args) {
				sendProfile = os.Args[i+2]
			}
		case "--mailbox":
			if i+2 < len(os.Args) {
				mailboxName = os.Args[i+2]
			}
		case "--help", "-h":
			fmt.Fprintf(os.Stderr, "mail-tui — full-screen TUI mail client\n")
			fmt.Fprintf(os.Stderr, "  --read-profile <profile>  AWS profile for reading (default: mail-client-read)\n")
			fmt.Fprintf(os.Stderr, "  --send-profile <profile>  AWS profile for sending (default: mail-sender)\n")
			fmt.Fprintf(os.Stderr, "  --mailbox <name>          Mailbox name (default: inbox)\n")
			os.Exit(0)
		}
	}

	ctx := context.Background()

	// Wire up reader and sender (errors are non-fatal at TUI startup — the user can still browse
	// any cached/pre-loaded headers; live operations will fail gracefully when attempted).
	r, err := wire.Reader(ctx, readProfile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: reader not available (%v)\n", err)
	}
	s, err := wire.Sender(ctx, sendProfile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: sender not available (%v)\n", err)
	}

	// Pre-load headers for the initial list view (first page, 50 messages).
	var headers []mailbox.Header
	if r != nil {
		hs, _, lerr := r.List(ctx, mailboxName, 50, nil)
		if lerr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not load headers (%v)\n", lerr)
		} else {
			headers = hs
		}
	}

	m := model{
		view:    viewList,
		headers: headers,
		reader:  r,
		sender:  s,
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
