// Package message parses inbound MIME and builds outbound MIME for the mail client. Pure (no AWS/network).
package message

import (
	"io"

	"github.com/jhillyerd/enmime/v2"
)

// Attachment holds metadata for a MIME attachment part.
type Attachment struct {
	FileName    string
	ContentType string
	Size        int
}

// Parsed holds the decoded fields extracted from a raw MIME message.
type Parsed struct {
	Subject     string
	From        string
	Date        string
	TextPlain   string
	TextHTML    string
	MessageID   string
	References  string
	Attachments []Attachment
}

// Parse reads a raw MIME message. enmime decodes quoted-printable/base64 and converts charsets to utf-8.
func Parse(r io.Reader) (*Parsed, error) {
	env, err := enmime.ReadEnvelope(r)
	if err != nil {
		return nil, err
	}
	// Soft parse errors (env.Errors, e.g. a truncated boundary) are intentionally ignored: ReadEnvelope still
	// returns a usable envelope. env.Inlines (embedded images) and env.OtherParts are not surfaced in v0.1
	// (deuda conocida — TODO v0.2: handle inline parts). The mail body + named attachments suffice for the TUI.
	p := &Parsed{
		Subject:    env.GetHeader("Subject"),
		From:       env.GetHeader("From"),
		Date:       env.GetHeader("Date"),
		TextPlain:  env.Text,
		TextHTML:   env.HTML,
		MessageID:  env.GetHeader("Message-ID"),
		References: env.GetHeader("References"),
	}
	for _, a := range env.Attachments {
		p.Attachments = append(p.Attachments, Attachment{
			FileName:    a.FileName,
			ContentType: a.ContentType,
			Size:        len(a.Content),
		})
	}
	return p, nil
}
