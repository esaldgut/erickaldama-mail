package message

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/jhillyerd/enmime/v2"
)

const Domain = "erickaldama.com"

// NewMessageID builds an RFC 5322 msg-id <unixnano.randhex@erickaldama.com>. No stdlib/Builder helper exists.
func NewMessageID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), hex.EncodeToString(b), Domain)
}

// ReplyHeaders derives threading headers per RFC 5322 §3.6.4 from the PARSED original (References lives in
// the S3 MIME headers, not in DynamoDB). References = parent's References + parent's Message-ID.
func ReplyHeaders(orig *Parsed) (inReplyTo, references, subject string) {
	if orig == nil {
		return "", "", ""
	}
	inReplyTo = orig.MessageID
	if orig.References != "" {
		references = orig.References + " " + orig.MessageID
	} else {
		references = orig.MessageID
	}
	subject = orig.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	return inReplyTo, references, subject
}

// FileAttach holds the path to a file to attach.
type FileAttach struct{ Path string }

// BuildOpts holds all parameters for constructing an outbound MIME message.
type BuildOpts struct {
	From, To, Subject, Body string
	Cc, Bcc                 string // comma-separated, plain addresses (no "Name <addr>")
	InReplyTo, References   string
	MessageID               string
	Attachments             []FileAttach
}

// SplitAddrs splits a comma-separated address list, trimming spaces and dropping empties. Exported because
// the CLI reply-all reuses it (message.SplitAddrs).
func SplitAddrs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if a := strings.TrimSpace(p); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// Build assembles outbound MIME via enmime. Returns the raw bytes AND the envelope destinations
// (To+Cc+Bcc) for SES SendRawEmail. The BCC is deliberately NOT passed to the enmime builder —
// calling .BCC() WOULD write a "Bcc:" header into the raw (enmime does NOT silence it). The BCC
// travels ONLY in the SES Destinations envelope (see Sender.Send) → privacy invariant.
func Build(opt BuildOpts) (raw []byte, destinations []string, err error) {
	if opt.MessageID == "" {
		opt.MessageID = NewMessageID()
	}
	b := enmime.Builder().
		From("", opt.From).
		To("", opt.To).
		Subject(opt.Subject).
		Text([]byte(opt.Body)).
		Header("Message-ID", opt.MessageID)
	cc := SplitAddrs(opt.Cc)
	for _, addr := range cc {
		b = b.CC("", addr)
	}
	if opt.InReplyTo != "" {
		b = b.Header("In-Reply-To", opt.InReplyTo)
	}
	if opt.References != "" {
		b = b.Header("References", opt.References)
	}
	for _, a := range opt.Attachments {
		b = b.AddFileAttachment(a.Path)
	}
	part, err := b.Build()
	if err != nil {
		return nil, nil, err
	}
	var sb strings.Builder
	if err := part.Encode(&sb); err != nil {
		return nil, nil, err
	}
	// Envelope destinations: To + Cc + Bcc. The Bcc is here ONLY (never in the header above).
	destinations = append([]string{opt.To}, cc...)
	destinations = append(destinations, SplitAddrs(opt.Bcc)...)
	return []byte(sb.String()), destinations, nil
}
