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
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"erickaldama-mail/internal/aiassist"
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
		fmt.Fprintf(tw, "%s\t%s\t%s\n", h.Date, h.From, h.Subject)
	}
	return tw.Flush()
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
			r, err := wire.Reader(ctx, readProfile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return err
			}
			hs, _, err := r.List(ctx, mailboxName, int32(count), nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error listing messages: %v\n", err)
				return err
			}
			// --count flag with no --json: just print the count (for tmux status, spec §5.3)
			if cmd.Flags().Changed("count") && !jsonFlag {
				fmt.Println(len(hs))
				return nil
			}
			return renderList(os.Stdout, hs, jsonFlag)
		},
	}

	// ── read ─────────────────────────────────────────────────────────────
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
			fmt.Print(message.RenderPlain(parsed))
			return nil
		},
	}

	// ── send ─────────────────────────────────────────────────────────────
	var sendFrom, sendTo, sendSubject, sendBody string
	sendCmd := &cobra.Command{
		Use:   "send",
		Short: "Send a new message",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			raw, err := message.Build(message.BuildOpts{
				From:    sendFrom,
				To:      sendTo,
				Subject: sendSubject,
				Body:    sendBody,
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
			msgID, err := s.Send(ctx, raw)
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
	_ = sendCmd.MarkFlagRequired("from")
	_ = sendCmd.MarkFlagRequired("to")
	_ = sendCmd.MarkFlagRequired("subject")

	// ── reply ─────────────────────────────────────────────────────────────
	replyCmd := &cobra.Command{
		Use:   "reply <s3Key>",
		Short: "Reply to a message (opens $EDITOR)",
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
			inReplyTo, references, subject := message.ReplyHeaders(parsed)
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
			raw, err := message.Build(message.BuildOpts{
				From:       replyFrom,
				To:         parsed.From,
				Subject:    subject,
				Body:       edited,
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
			msgID, err := s.Send(ctx, raw)
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

	root.AddCommand(lsCmd, readCmd, sendCmd, replyCmd, aiCmd)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
