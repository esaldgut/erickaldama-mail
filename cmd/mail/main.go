// Package main is the mail CLI. Subcommands: ls, read, send, reply, ai.
// All AWS wiring goes through internal/wire (single wiring point, DRY).
// Errors and warnings always go to STDERR so --json output stays clean.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"erickaldama-mail/internal/aiassist"
	"erickaldama-mail/internal/cache"
	"erickaldama-mail/internal/config"
	"erickaldama-mail/internal/mailbox"
	"erickaldama-mail/internal/message"
	"erickaldama-mail/internal/redact"
	"erickaldama-mail/internal/wire"
)

// renderList is pure and testeable. JSON for piping; table for humans.
// Errors and warnings must NOT be written here — callers print to stderr.
func renderList(w io.Writer, hs []mailbox.Header, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(w).Encode(hs)
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for _, h := range hs {
		// date+time, sender, subject, s3Key — s3Key last so the user can copy it (tmux copy-mode)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", cache.FormatDate(h.Date), h.From, h.Subject, h.S3Key)
	}
	return tw.Flush()
}

// tmuxPopupArgs builds the argv for `tmux display-popup` that launches the TUI. Pure + testeable:
// an argv slice (never a shell string), so the profile/mailbox values cannot inject shell. The TUI
// inherits the read profile and mailbox the user passed to `mail`.
func tmuxPopupArgs(readProfile, mailboxName string) []string {
	return []string{
		"display-popup", "-E", "-w", "90%", "-h", "90%",
		"mail-tui", "--read-profile", readProfile, "--mailbox", mailboxName,
	}
}

// openEditor edits content in $EDITOR via argv-slice (no shell → no injection). Tmpfile name is random
// (os.CreateTemp), NOT derived from untrusted mail subject/from.
func openEditor(ctx context.Context, content string) (string, error) {
	f, err := os.CreateTemp("", "mail-*.txt")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	_, _ = f.WriteString(content)
	f.Close()
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nvim"
	}
	fields := strings.Fields(editor) // e.g. "code -w" → ["code","-w"]
	args := append(fields[1:], f.Name())
	cmd := exec.CommandContext(ctx, fields[0], args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	edited, err := os.ReadFile(f.Name())
	return string(edited), err
}

// replyAllCc returns the original To+Cc addresses minus self, comma-joined. Uses message.SplitAddrs (exported).
func replyAllCc(parsedTo, parsedCc, self string) string {
	seen := map[string]bool{}
	if self != "" { // REPLY-1: with self="" do NOT seed seen[""] (would filter nothing → own addr enters Cc)
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

func main() {
	var (
		readProfile string
		sendProfile string
		mailboxName string
		backend     string
		agentModel  string
		jsonFlag    bool
		count       int
		replyFrom   string
	)

	root := &cobra.Command{
		Use:   "mail",
		Short: "Mail client — ls/read/send/reply/ai",
	}
	root.PersistentFlags().StringVar(&readProfile, "read-profile", "mail-client-read", "AWS SSO profile for reading mail")
	root.PersistentFlags().StringVar(&sendProfile, "send-profile", "mail-sender", "AWS SSO profile for sending mail")
	root.PersistentFlags().StringVar(&mailboxName, "mailbox", "inbox", "Mailbox name (DynamoDB PK prefix)")
	root.PersistentFlags().StringVar(&backend, "backend", "ollama", "AI backend: ollama|claude")
	root.PersistentFlags().StringVar(&agentModel, "agent-model", "qwen3:32b", "Ollama model for agent/summarize/draft")
	root.PersistentFlags().BoolVar(&jsonFlag, "json", false, "Output as JSON (machine-readable)")
	root.PersistentFlags().IntVar(&count, "count", 20, "Number of messages to list")

	// ── ls ───────────────────────────────────────────────────────────────
	lsCmd := &cobra.Command{
		Use:   "ls",
		Short: "List messages in the mailbox",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, hasCfg, _ := config.Load()
			var mailboxes []string
			if cmd.Flags().Changed("mailbox") { // cmd.Flags() includes PersistentFlags inherited from root
				mailboxes = []string{mailboxName} // explicit override
			} else if hasCfg && len(cfg.Mailboxes) > 0 {
				mailboxes = cfg.Mailboxes // all mailboxes from config
			} else {
				fmt.Fprintln(os.Stderr, "no hay config; crea ~/.config/erickaldama-mail/config.toml con tus mailboxes, o usa --mailbox <dirección>")
				return fmt.Errorf("no mailbox specified and no config") // exit≠0
			}
			// Apply read-profile fallback from config before wiring (GAP-1 / spec §3.5)
			if !cmd.Root().PersistentFlags().Changed("read-profile") && hasCfg && cfg.ReadProfile != "" {
				readProfile = cfg.ReadProfile
			}
			r, err := wire.Reader(ctx, readProfile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return err
			}
			var all []mailbox.Header
			// Try the cache: open, sync each mailbox, read from SQLite. On ANY cache failure,
			// fall back transparently to the live Reader (the cache is a discardable optimization).
			cachePath, perr := cache.DefaultPath()
			var ca *cache.Cache
			var cerr error
			if perr == nil {
				ca, cerr = cache.Open(cachePath)
			}
			if perr == nil && cerr == nil {
				defer ca.Close()
				for _, mb := range mailboxes {
					// Populate with a FIXED cap (not count) so search sees the full history (M-2).
					if _, serr := ca.Sync(ctx, r, mb, cache.SyncPageLimit); serr != nil {
						fmt.Fprintf(os.Stderr, "warning: cache sync %s failed (%v); using live\n", mb, serr)
					}
					hs, lerr := ca.List(mb, count) // display limit = count
					if lerr != nil {
						// m-1: cache List failed → fall back to live for THIS mailbox, don't drop it.
						fmt.Fprintf(os.Stderr, "warning: cache list %s failed (%v); using live\n", mb, lerr)
						if live, _, le := r.List(ctx, mb, int32(count), nil); le == nil {
							all = append(all, live...)
						}
						continue
					}
					all = append(all, hs...)
				}
			} else {
				// Cache unavailable → live path (original behaviour).
				for _, mb := range mailboxes {
					hs, _, lerr := r.List(ctx, mb, int32(count), nil)
					if lerr != nil {
						fmt.Fprintf(os.Stderr, "error listing %s: %v\n", mb, lerr)
						continue
					}
					all = append(all, hs...)
				}
			}
			// sort by SK descending (ISO8601, NOT by Date which is RFC1123Z and sorts incorrectly)
			slices.SortFunc(all, func(a, b mailbox.Header) int { return strings.Compare(b.SK, a.SK) })
			if len(all) > count {
				all = all[:count] // truncate merged result to limit
			}
			// PRESERVE (tmux status uses `mail ls --count N` without --json to get the number)
			if cmd.Flags().Changed("count") && !jsonFlag {
				fmt.Println(len(all))
				return nil
			}
			return renderList(os.Stdout, all, jsonFlag)
		},
	}

	// ── read ─────────────────────────────────────────────────────────────
	var rich, loadRemote bool
	readCmd := &cobra.Command{
		Use:   "read <s3Key>",
		Short: "Read one message by its S3 key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			r, err := wire.Reader(ctx, readProfile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return err
			}
			body, err := r.Open(ctx, args[0])
			if err != nil {
				fmt.Fprintf(os.Stderr, "error opening message: %v\n", err)
				return err
			}
			defer body.Close()
			parsed, err := message.Parse(body)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error parsing message: %v\n", err)
				return err
			}
			if !rich {
				// Default: pipe-friendly plain text — DO NOT change this behaviour.
				fmt.Print(message.RenderPlain(parsed))
				return nil
			}
			// --rich: get terminal width → sanitize HTML → render to ANSI.
			width, _, err := term.GetSize(int(os.Stdout.Fd()))
			if err != nil || width <= 0 {
				width = 80 // degrade gracefully for non-TTY / pipes
			}
			san, err := message.SanitizeHTML(parsed.TextHTML, loadRemote)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error sanitizing HTML: %v\n", err)
				return err
			}
			clean := *parsed
			clean.TextHTML = san.HTML
			out, err := message.RenderRich(&clean, width)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error rendering rich: %v\n", err)
				return err
			}
			fmt.Print(out)
			return nil
		},
	}
	readCmd.Flags().BoolVar(&rich, "rich", false, "Render HTML body as rich ANSI text (sanitized; terminal-width aware)")
	readCmd.Flags().BoolVar(&loadRemote, "load-remote", false, "Allow remote images when --rich (default: blocked, placeholder shown)")

	// ── send ─────────────────────────────────────────────────────────────
	var sendFrom, sendTo, sendSubject, sendBody, sendCc, sendBcc string
	sendCmd := &cobra.Command{
		Use:   "send",
		Short: "Send a new message",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			// Profile and from fallbacks from config (GAP-1 / spec §3.5)
			cfg, hasCfg, _ := config.Load()
			if !cmd.Flags().Changed("from") && hasCfg && cfg.DefaultFrom != "" {
				sendFrom = cfg.DefaultFrom
			}
			if !cmd.Flags().Changed("send-profile") && hasCfg && cfg.SendProfile != "" {
				sendProfile = cfg.SendProfile
			}
			raw, dests, err := message.Build(message.BuildOpts{
				From:    sendFrom,
				To:      sendTo,
				Subject: sendSubject,
				Body:    sendBody,
				Cc:      sendCc,
				Bcc:     sendBcc,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "error building message: %v\n", err)
				return err
			}
			s, err := wire.Sender(ctx, sendProfile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return err
			}
			msgID, err := s.Send(ctx, raw, dests)
			if err != nil {
				if errors.Is(err, mailbox.ErrSandboxRecipient) {
					// Actionable error to stderr (not stdout — keeps --json clean)
					fmt.Fprintf(os.Stderr, "send rejected (SES sandbox): verify the recipient address or use success@simulator.amazonses.com\ndetails: %v\n", err)
					return err
				}
				fmt.Fprintf(os.Stderr, "send error: %v\n", err)
				return err
			}
			fmt.Fprintf(os.Stderr, "sent: %s\n", msgID)
			return nil
		},
	}
	sendCmd.Flags().StringVar(&sendFrom, "from", "", "From address")
	sendCmd.Flags().StringVar(&sendTo, "to", "", "To address")
	sendCmd.Flags().StringVar(&sendSubject, "subject", "", "Subject")
	sendCmd.Flags().StringVar(&sendBody, "body", "", "Body text")
	sendCmd.Flags().StringVar(&sendCc, "cc", "", "Cc addresses (comma-separated, plain addresses)")
	sendCmd.Flags().StringVar(&sendBcc, "bcc", "", "Bcc addresses (comma-separated, plain addresses)")
	_ = sendCmd.MarkFlagRequired("to")
	_ = sendCmd.MarkFlagRequired("subject")

	// ── reply ─────────────────────────────────────────────────────────────
	var replyCc, replyBcc string
	replyCmd := &cobra.Command{
		Use:   "reply <s3Key>",
		Short: "Reply to a message (opens $EDITOR)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			// Profile and from fallbacks from config (GAP-1 / spec §3.5)
			cfg, hasCfg, _ := config.Load()
			if !cmd.Flags().Changed("from") && hasCfg && cfg.DefaultFrom != "" {
				replyFrom = cfg.DefaultFrom
			}
			if !cmd.Flags().Changed("read-profile") && hasCfg && cfg.ReadProfile != "" {
				readProfile = cfg.ReadProfile
			}
			if !cmd.Flags().Changed("send-profile") && hasCfg && cfg.SendProfile != "" {
				sendProfile = cfg.SendProfile
			}
			r, err := wire.Reader(ctx, readProfile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return err
			}
			body, err := r.Open(ctx, args[0])
			if err != nil {
				fmt.Fprintf(os.Stderr, "error opening message: %v\n", err)
				return err
			}
			defer body.Close()
			parsed, err := message.Parse(body)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error parsing message: %v\n", err)
				return err
			}
			inReplyTo, references, subject := message.ReplyHeaders(parsed)
			// reply-all: pre-fill --cc with To+Cc of original minus self, unless user passed --cc explicitly
			if !cmd.Flags().Changed("cc") {
				replyCc = replyAllCc(parsed.To, parsed.Cc, replyFrom)
			}
			// Pre-fill editor with quoted original
			initial := fmt.Sprintf("To: %s\nSubject: %s\n\n\n-- Original --\n%s", parsed.From, subject, message.RenderPlain(parsed))
			edited, err := openEditor(ctx, initial)
			if err != nil {
				fmt.Fprintf(os.Stderr, "editor error: %v\n", err)
				return err
			}
			// Confirm before send
			fmt.Fprintf(os.Stderr, "\nSend reply? [y/N] ")
			scanner := bufio.NewScanner(os.Stdin)
			if !scanner.Scan() || strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
				fmt.Fprintln(os.Stderr, "aborted")
				return nil
			}
			// From = the user's own verified identity (--from); To = the original sender. Using parsed.From
			// for From would impersonate the original sender and be rejected by the mail-send policy
			// (Condition ses:FromAddress=<verified identity>). (audit H-1)
			if replyFrom == "" {
				fmt.Fprintln(os.Stderr, "error: --from is required (your verified sender address, e.g. erick@erickaldama.com)")
				return fmt.Errorf("reply: --from is required")
			}
			raw, dests, err := message.Build(message.BuildOpts{
				From:       replyFrom,
				To:         parsed.From,
				Subject:    subject,
				Body:       edited,
				Cc:         replyCc,
				Bcc:        replyBcc,
				InReplyTo:  inReplyTo,
				References: references,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "error building reply: %v\n", err)
				return err
			}
			s, err := wire.Sender(ctx, sendProfile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return err
			}
			msgID, err := s.Send(ctx, raw, dests)
			if err != nil {
				if errors.Is(err, mailbox.ErrSandboxRecipient) {
					fmt.Fprintf(os.Stderr, "send rejected (SES sandbox): verify the recipient address or use success@simulator.amazonses.com\ndetails: %v\n", err)
					return err
				}
				fmt.Fprintf(os.Stderr, "send error: %v\n", err)
				return err
			}
			fmt.Fprintf(os.Stderr, "reply sent: %s\n", msgID)
			return nil
		},
	}
	replyCmd.Flags().StringVar(&replyFrom, "from", "", "Your verified sender address (e.g. erick@erickaldama.com); required")
	replyCmd.Flags().StringVar(&replyCc, "cc", "", "Cc addresses (comma-separated, plain addresses)")
	replyCmd.Flags().StringVar(&replyBcc, "bcc", "", "Bcc addresses (comma-separated, plain addresses)")

	// ── ai ───────────────────────────────────────────────────────────────
	aiCmd := &cobra.Command{
		Use:   "ai <summarize|draft|agent> <s3Key|goal>",
		Short: "AI-powered subcommands (summarize, draft, agent)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			subCmd := args[0]
			target := args[1]

			// An invalid --backend is a user typo → hard error (only failure wire.Provider can return).
			// A live failure (Ollama daemon down) surfaces later in the Summarize/Draft/RunAgent call and is
			// degraded there to stderr without aborting — ls/read/send/reply (separate subcommands) are never
			// affected, so the AI never blocks mail (spec §6). (audit H-2)
			var p aiassist.LLMProvider
			p, err := wire.Provider(backend, agentModel)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: invalid --backend %q (use ollama|claude): %v\n", backend, err)
				return err
			}

			switch subCmd {
			case "summarize":
				r, err := wire.Reader(ctx, readProfile)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					return err
				}
				body, err := r.Open(ctx, target)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error opening message: %v\n", err)
					return err
				}
				defer body.Close()
				parsed, err := message.Parse(body)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error parsing message: %v\n", err)
					return err
				}
				mailBody := message.RenderPlain(parsed)
				// Always redact before sending to any LLM backend (defense in depth; spec §7)
				mailBody = redact.Redact(mailBody)
				summary, err := aiassist.Summarize(ctx, p, mailBody)
				if err != nil {
					fmt.Fprintf(os.Stderr, "backend AI no disponible (¿ollama serve?); lectura/envío siguen\nerror: %v\n", err)
					return nil // degrade: the user is informed; the AI failing must not abort with a nonzero exit (spec §6)
				}
				fmt.Println(summary)

			case "draft":
				instruction := target
				thread := ""
				if len(args) >= 3 {
					thread = args[2]
				}
				// Redact any thread content before sending to LLM
				thread = redact.Redact(thread)
				draft, err := aiassist.Draft(ctx, p, thread, instruction)
				if err != nil {
					fmt.Fprintf(os.Stderr, "backend AI no disponible (¿ollama serve?); lectura/envío siguen\nerror: %v\n", err)
					return nil // degrade: the user is informed; the AI failing must not abort with a nonzero exit (spec §6)
				}
				fmt.Println(draft)

			case "agent":
				r, err := wire.Reader(ctx, readProfile)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					return err
				}
				goal := target
				// Redact goal before sending to LLM (goal may contain sensitive context)
				goal = redact.Redact(goal)
				result, err := aiassist.RunAgent(ctx, p, r, mailboxName, goal, 10)
				if err != nil {
					fmt.Fprintf(os.Stderr, "backend AI no disponible (¿ollama serve?); lectura/envío siguen\nerror: %v\n", err)
					return nil // degrade: the user is informed; the AI failing must not abort with a nonzero exit (spec §6)
				}
				fmt.Println(result)

			default:
				return fmt.Errorf("ai subcomando desconocido %q (usa summarize|draft|agent)", subCmd)
			}
			return nil
		},
	}

	// ── search ───────────────────────────────────────────────────────────
	searchCmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Full-text search over cached headers (sender + subject)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, hasCfg, _ := config.Load()
			var mailboxes []string
			if cmd.Flags().Changed("mailbox") {
				mailboxes = []string{mailboxName}
			} else if hasCfg && len(cfg.Mailboxes) > 0 {
				mailboxes = cfg.Mailboxes
			} else {
				return fmt.Errorf("no mailbox specified and no config")
			}
			if !cmd.Root().PersistentFlags().Changed("read-profile") && hasCfg && cfg.ReadProfile != "" {
				readProfile = cfg.ReadProfile
			}
			query := strings.Join(args, " ")

			cachePath, err := cache.DefaultPath()
			if err != nil {
				return fmt.Errorf("cache path: %w", err)
			}
			ca, err := cache.Open(cachePath)
			if err != nil {
				return fmt.Errorf("cache open: %w", err)
			}
			defer ca.Close()
			// Refresh the cache first so search reflects recent mail (best-effort). Populate with the
			// FIXED cap (not count) — search must see the full history, not just --count rows (M-2).
			if r, rerr := wire.Reader(ctx, readProfile); rerr == nil {
				for _, mb := range mailboxes {
					if _, serr := ca.Sync(ctx, r, mb, cache.SyncPageLimit); serr != nil {
						fmt.Fprintf(os.Stderr, "warning: cache sync %s failed (%v)\n", mb, serr)
					}
				}
			}
			var all []mailbox.Header
			for _, mb := range mailboxes {
				hs, serr := ca.Search(mb, query, count)
				if serr != nil {
					fmt.Fprintf(os.Stderr, "error searching %s: %v\n", mb, serr)
					continue
				}
				all = append(all, hs...)
			}
			return renderList(os.Stdout, all, jsonFlag)
		},
	}

	// ── tmux ─────────────────────────────────────────────────────────────
	// Glue for the tmux integration documented in the SP-4 spec §5.3. Two subcommands:
	//   popup  → open the full-screen TUI in a tmux display-popup (floating overlay)
	//   status → print the unread/message count for status-right (no --count ambiguity)
	tmuxCmd := &cobra.Command{
		Use:   "tmux <popup|status>",
		Short: "tmux integration glue (popup overlay, status count)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "popup":
				// Must run inside tmux (display-popup needs a server + client).
				if os.Getenv("TMUX") == "" {
					return fmt.Errorf("mail tmux popup must run inside a tmux session (TMUX env not set)")
				}
				// argv-slice, no shell string → no injection. Forward read/mailbox flags to mail-tui.
				tc := exec.CommandContext(context.Background(), "tmux", tmuxPopupArgs(readProfile, mailboxName)...)
				tc.Stdin, tc.Stdout, tc.Stderr = os.Stdin, os.Stdout, os.Stderr
				if err := tc.Run(); err != nil {
					return fmt.Errorf("tmux display-popup failed: %w", err)
				}
				return nil

			case "status":
				// Count for status-right. Read-only. AI/send never touched.
				// Multi-mailbox: same logic as ls — config.Mailboxes wins unless --mailbox was
				// passed explicitly (H-4, deferred from Task 5).
				ctx := context.Background()
				cfg, hasCfg, _ := config.Load()
				var statusMailboxes []string
				if cmd.Root().PersistentFlags().Changed("mailbox") {
					statusMailboxes = []string{mailboxName}
				} else if hasCfg && len(cfg.Mailboxes) > 0 {
					statusMailboxes = cfg.Mailboxes
				} else {
					statusMailboxes = []string{mailboxName} // fallback: the flag default ("inbox")
				}
				if !cmd.Root().PersistentFlags().Changed("read-profile") && hasCfg && cfg.ReadProfile != "" {
					readProfile = cfg.ReadProfile
				}
				r, err := wire.Reader(ctx, readProfile)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					return err
				}
				var total int
				for _, mb := range statusMailboxes {
					hs, _, lerr := r.List(ctx, mb, int32(count), nil)
					if lerr != nil {
						fmt.Fprintf(os.Stderr, "warning: listing %s: %v\n", mb, lerr)
						continue
					}
					total += len(hs)
				}
				fmt.Printf("📬 %d\n", total)
				return nil

			default:
				return fmt.Errorf("tmux subcomando desconocido %q (usa popup|status)", args[0])
			}
		},
	}

	root.AddCommand(lsCmd, readCmd, sendCmd, replyCmd, aiCmd, tmuxCmd, searchCmd)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
