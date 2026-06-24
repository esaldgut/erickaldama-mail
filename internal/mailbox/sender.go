package mailbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	sestypes "github.com/aws/aws-sdk-go-v2/service/ses/types"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
)

// ErrSandboxRecipient is returned when a send is rejected AND the account is in sandbox — the most likely
// cause is an unverified recipient. AWS does NOT type this case (it surfaces as MessageRejected generic), so
// we classify by errors.As(*MessageRejected) + a prior DetectSandbox, NEVER by string-matching the message.
var ErrSandboxRecipient = errors.New("send rejected; SES in sandbox — verify the recipient or use success@simulator.amazonses.com")

// SESRawAPI is the minimal SES v1 interface required by Sender.
type SESRawAPI interface {
	SendRawEmail(context.Context, *ses.SendRawEmailInput, ...func(*ses.Options)) (*ses.SendRawEmailOutput, error)
}

// SESAccountAPI is the minimal SES v2 interface required by Sender (for sandbox detection).
type SESAccountAPI interface {
	GetAccount(context.Context, *sesv2.GetAccountInput, ...func(*sesv2.Options)) (*sesv2.GetAccountOutput, error)
}

// Sender delivers raw MIME via SES v1 and can detect sandbox mode via sesv2.
type Sender struct {
	raw  SESRawAPI
	acct SESAccountAPI
}

// NewSender constructs a Sender backed by the given SES v1 and sesv2 clients.
func NewSender(raw SESRawAPI, acct SESAccountAPI) *Sender { return &Sender{raw: raw, acct: acct} }

// DetectSandbox reports whether the account is in the SES sandbox. ProductionAccessEnabled exists ONLY in
// sesv2 (GetAccount), never in SES v1.
func (s *Sender) DetectSandbox(ctx context.Context) (bool, error) {
	out, err := s.acct.GetAccount(ctx, &sesv2.GetAccountInput{})
	if err != nil {
		return false, err
	}
	return !out.ProductionAccessEnabled, nil
}

// MaxRawBytes is the SES v1 SendRawEmail hard limit (10MB after base64; inadjustable in v1).
const MaxRawBytes = 10 * 1024 * 1024

// Send delivers raw MIME via SES v1 SendRawEmail (sesv2 has no SendRawEmail). On a typed MessageRejected, if
// the account is in sandbox, wrap as ErrSandboxRecipient (the actionable cause) — no string-match.
func (s *Sender) Send(ctx context.Context, raw []byte) (string, error) {
	if len(raw) > MaxRawBytes {
		return "", fmt.Errorf("message is %d bytes; SES v1 SendRawEmail caps at %d (10MB, inadjustable)", len(raw), MaxRawBytes)
	}
	out, err := s.raw.SendRawEmail(ctx, &ses.SendRawEmailInput{
		RawMessage: &sestypes.RawMessage{Data: raw},
	})
	if err != nil {
		var rejected *sestypes.MessageRejected
		if errors.As(err, &rejected) {
			// AWS does not type recipient-not-verified; infer the cause from sandbox state. If sandbox
			// detection itself fails, surface THAT error (don't silently swallow it — audit F-01): the caller
			// must be able to tell "not sandbox" from "couldn't determine sandbox". If not in sandbox, the
			// MessageRejected is returned unwrapped below (may be an invalid address or content-policy reject).
			sb, derr := s.DetectSandbox(ctx)
			if derr != nil {
				return "", fmt.Errorf("send rejected (MessageRejected) and sandbox detection failed: %w", derr)
			}
			if sb {
				return "", fmt.Errorf("%w: %v", ErrSandboxRecipient, err)
			}
		}
		return "", fmt.Errorf("send raw email: %w", err)
	}
	return aws.ToString(out.MessageId), nil
}
