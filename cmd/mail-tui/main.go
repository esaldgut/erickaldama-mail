// Package main is the mail-tui binary: a full-screen Bubble Tea TUI for the mail client.
// Vim-motions: list (j/k/gg/G/Enter/r/s/a/q), reader (J/K/Esc/r/s), composer (Ctrl-E/Ctrl-S/y/n).
// AWS wiring: identical profile flags as cmd/mail, via internal/wire.
// Run with --help for usage.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"erickaldama-mail/internal/cache"
	"erickaldama-mail/internal/config"
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

	// Load optional config and apply fallbacks (same pattern as cmd/mail/main.go).
	// Flags explicitly passed on the command line take precedence over config values.
	from := ""
	cfg, hasCfg, _ := config.Load()
	if hasCfg {
		from = cfg.DefaultFrom
		if cfg.ReadProfile != "" && readProfile == "mail-client-read" {
			readProfile = cfg.ReadProfile
		}
		if cfg.SendProfile != "" && sendProfile == "mail-sender" {
			sendProfile = cfg.SendProfile
		}
		if mailboxName == "inbox" && len(cfg.Mailboxes) > 0 {
			mailboxName = cfg.Mailboxes[0]
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

	// v0.5: open the cache and sync it (best-effort, non-fatal) so the '/' filter can query FTS5
	// instead of the native in-memory fuzzy filter. A cache/sync failure degrades gracefully —
	// ca stays nil, applyFilter no-ops, and browsing falls back to the live reader below.
	var ca *cache.Cache
	if cachePath, perr := cache.DefaultPath(); perr == nil {
		if opened, cerr := cache.Open(cachePath); cerr == nil {
			ca = opened
			defer ca.Close()
			if r != nil {
				if _, serr := ca.Sync(ctx, r, mailboxName, cache.SyncPageLimit); serr != nil { // fixed cap, not 50 (M-2)
					fmt.Fprintf(os.Stderr, "warning: cache sync failed (%v)\n", serr)
				}
			}
		}
	}

	// Pre-load headers for the initial list view (first page, 50 messages): prefer the cache,
	// fall back to a live List when the cache is unavailable or empty.
	var headers []mailbox.Header
	if ca != nil {
		if hs, lerr := ca.List(mailboxName, 50); lerr == nil {
			headers = hs
		}
	}
	if headers == nil && r != nil {
		hs, _, lerr := r.List(ctx, mailboxName, 50, nil)
		if lerr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not load headers (%v)\n", lerr)
		} else {
			headers = hs
		}
	}

	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	m := model{
		mode:    modeBrowse,
		focus:   focusList,
		list:    newMessageList(headers, 30, 20),
		spinner: sp,
		from:    from,
		mailbox: mailboxName, // same key used to Sync/pre-load the cache above — applyFilter reads under this
		reader:  r,
		sender:  s,
		cache:   ca,
		compose: newComposer(),
	}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
