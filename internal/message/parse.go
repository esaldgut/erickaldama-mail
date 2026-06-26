// Package message parses inbound MIME and builds outbound MIME for the mail client. Pure (no AWS/network).
package message

import (
	"io"
	"strings"

	"github.com/jhillyerd/enmime/v2"
)

// Attachment holds metadata for a MIME attachment part.
type Attachment struct {
	FileName    string
	ContentType string
	Size        int
}

// InlineImage is an embedded image part referenced by the HTML via cid:.
// Data is already base64/QP-decoded by enmime.
type InlineImage struct {
	ContentID   string // without angle brackets (enmime strips them)
	ContentType string
	Data        []byte
}

// Parsed holds the decoded fields extracted from a raw MIME message.
type Parsed struct {
	Subject      string
	From         string
	To           string // for reply-all
	Cc           string // for reply-all
	Date         string
	TextPlain    string
	TextHTML     string
	MessageID    string
	InReplyTo    string
	References   string
	Attachments  []Attachment
	InlineImages []InlineImage
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
		To:         env.GetHeader("To"),
		Cc:         env.GetHeader("Cc"),
		Date:       env.GetHeader("Date"),
		TextPlain:  env.Text,
		TextHTML:   env.HTML,
		MessageID:  env.GetHeader("Message-ID"),
		InReplyTo:  env.GetHeader("In-Reply-To"),
		References: env.GetHeader("References"),
	}
	for _, a := range env.Attachments {
		p.Attachments = append(p.Attachments, Attachment{
			FileName:    a.FileName,
			ContentType: a.ContentType,
			Size:        len(a.Content),
		})
	}
	for _, parts := range [][]*enmime.Part{env.Inlines, env.OtherParts} {
		for _, ip := range parts {
			if ip.ContentID == "" || !strings.HasPrefix(ip.ContentType, "image/") {
				continue
			}
			p.InlineImages = append(p.InlineImages, InlineImage{
				ContentID:   ip.ContentID, // enmime já quitó los <>
				ContentType: ip.ContentType,
				Data:        ip.Content, // já decodificado
			})
		}
	}
	return p, nil
}
