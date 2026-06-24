// Package redact deterministically masks secret-shaped tokens and third-party emails before any mail body
// crosses the network to an LLM backend (defense in depth; spec §7). Pure, golden-corpus tested.
package redact

import "regexp"

var (
	// reSecret matches common token patterns: AWS AKIA keys (16 upper/digit chars after prefix),
	// GitHub PATs (classic ghp_ + 36, fine-grained github_pat_ + 20.. — real PATs are ~82 chars), and
	// Slack tokens (xox*-). Intentionally over-eager: masking is safe, leaking is not. The minimum lengths
	// match the real token shapes so the regex does not fire on short benign prose (e.g. "github_pat_x").
	reSecret = regexp.MustCompile(`\b(AKIA[0-9A-Z]{16}|ghp_[0-9A-Za-z]{36}|github_pat_[0-9A-Za-z_]{20,}|xox[baprs]-[0-9A-Za-z-]+)\b`)
	reEmail  = regexp.MustCompile(`\b[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}\b`)
)

// Redact replaces secret-shaped tokens and emails with placeholders. The control is the deterministic regex;
// it is intentionally over-eager (masking is safe, leaking is not).
func Redact(s string) string {
	s = reSecret.ReplaceAllString(s, "<REDACTED-SECRET>")
	s = reEmail.ReplaceAllString(s, "<REDACTED-EMAIL>")
	return s
}
