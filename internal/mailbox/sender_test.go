package mailbox

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	sestypes "github.com/aws/aws-sdk-go-v2/service/ses/types"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
)

type fakeRaw struct {
	id       string
	err      error
	gotDests []string // capture for the Destinations test
}

func (f *fakeRaw) SendRawEmail(ctx context.Context, in *ses.SendRawEmailInput, _ ...func(*ses.Options)) (*ses.SendRawEmailOutput, error) {
	f.gotDests = in.Destinations
	if f.err != nil {
		return nil, f.err
	}
	return &ses.SendRawEmailOutput{MessageId: aws.String(f.id)}, nil
}

type fakeAcct struct {
	prod bool
	err  error
}

func (f fakeAcct) GetAccount(ctx context.Context, in *sesv2.GetAccountInput, _ ...func(*sesv2.Options)) (*sesv2.GetAccountOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &sesv2.GetAccountOutput{ProductionAccessEnabled: f.prod}, nil
}

func TestSendOK(t *testing.T) {
	s := NewSender(&fakeRaw{id: "mid-1"}, fakeAcct{prod: false})
	id, err := s.Send(context.Background(), []byte("RAW"), []string{"to@x"})
	if err != nil || id != "mid-1" {
		t.Fatalf("send: id=%q err=%v", id, err)
	}
}

func TestSendSandboxRejectMapped(t *testing.T) {
	// AWS does NOT type recipient-not-verified — it surfaces as MessageRejected generic.
	rejected := &sestypes.MessageRejected{}
	s := NewSender(&fakeRaw{err: rejected}, fakeAcct{prod: false})
	_, err := s.Send(context.Background(), []byte("RAW"), []string{"to@x"})
	if !errors.Is(err, ErrSandboxRecipient) {
		t.Fatalf("expected ErrSandboxRecipient (MessageRejected + sandbox), got %v", err)
	}
}

func TestDetectSandbox(t *testing.T) {
	s := NewSender(&fakeRaw{}, fakeAcct{prod: false})
	sb, err := s.DetectSandbox(context.Background())
	if err != nil || !sb {
		t.Fatalf("sandbox: %v err=%v", sb, err)
	}
}

func TestDetectSandboxProd(t *testing.T) { // audit F-02: cover the production (not-sandbox) case
	s := NewSender(&fakeRaw{}, fakeAcct{prod: true})
	sb, err := s.DetectSandbox(context.Background())
	if err != nil || sb {
		t.Fatalf("expected not-sandbox for prod account: sb=%v err=%v", sb, err)
	}
}

func TestSendRejectSandboxDetectionFails(t *testing.T) { // audit F-01: a failed DetectSandbox must NOT be swallowed
	rejected := &sestypes.MessageRejected{}
	detErr := errors.New("getaccount denied")
	s := NewSender(&fakeRaw{err: rejected}, fakeAcct{err: detErr})
	_, err := s.Send(context.Background(), []byte("RAW"), []string{"to@x"})
	if !errors.Is(err, detErr) {
		t.Fatalf("DetectSandbox failure must surface to the caller, got %v", err)
	}
}

func TestSendRejectInProduction(t *testing.T) { // not sandbox → MessageRejected returned unwrapped, not as ErrSandboxRecipient
	rejected := &sestypes.MessageRejected{}
	s := NewSender(&fakeRaw{err: rejected}, fakeAcct{prod: true})
	_, err := s.Send(context.Background(), []byte("RAW"), []string{"to@x"})
	if errors.Is(err, ErrSandboxRecipient) {
		t.Fatalf("prod reject must NOT map to ErrSandboxRecipient, got %v", err)
	}
}

func TestSendTooLarge(t *testing.T) { // MaxRawBytes guard fires before any AWS call
	s := NewSender(&fakeRaw{}, fakeAcct{})
	_, err := s.Send(context.Background(), make([]byte, MaxRawBytes+1), []string{"to@x"})
	if err == nil {
		t.Fatal("expected size-limit error for >10MB message")
	}
}

func TestSendPassesDestinations(t *testing.T) {
	fr := &fakeRaw{id: "mid-1"}
	s := NewSender(fr, fakeAcct{prod: false})
	_, err := s.Send(context.Background(), []byte("raw"), []string{"to@x", "bcc@x"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fr.gotDests, []string{"to@x", "bcc@x"}) {
		t.Fatalf("Destinations not passed: %v", fr.gotDests)
	}
}
