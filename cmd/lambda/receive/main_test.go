package main

import (
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

func sampleMail(recipients []string) events.SimpleEmailService {
	return events.SimpleEmailService{
		Mail: events.SimpleEmailMessage{
			MessageID: "ses-object-key-id",
			Timestamp: time.Date(2026, 6, 17, 21, 30, 0, 0, time.UTC),
			CommonHeaders: events.SimpleEmailCommonHeaders{
				From:      []string{"sender@example.com"},
				Subject:   "hi",
				MessageID: "<rfc5322-id@example.com>",
			},
		},
		Receipt: events.SimpleEmailReceipt{Recipients: recipients},
	}
}

func TestItemsForMessage_FiltersDomainRecipients(t *testing.T) {
	ses := sampleMail([]string{"erick@erickaldama.com", "outsider@gmail.com", "dmarc-reports@erickaldama.com"})
	items := itemsForMessage(ses, "erickaldama.com")

	if len(items) != 2 {
		t.Fatalf("expected 2 domain recipients, got %d", len(items))
	}
	if !strings.HasPrefix(items[0].PK, "mailbox#") {
		t.Fatalf("PK should be mailbox#<addr>, got %s", items[0].PK)
	}
	if !strings.Contains(items[0].SK, "2026-06-17T21:30:00Z") || !strings.Contains(items[0].SK, "<rfc5322-id@example.com>") {
		t.Fatalf("SK must embed Mail.Timestamp + CommonHeaders.MessageID, got %s", items[0].SK)
	}
	if items[0].S3Key != "inbound/ses-object-key-id" {
		t.Fatalf("S3Key must be inbound/<Mail.MessageID>, got %s", items[0].S3Key)
	}
}
