package aiassist

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"erickaldama-mail/internal/mailbox"
)

// ddbWith builds a fakeDDB (defined in tools_test.go) returning the given subjects as full headers.
// AUDIT FIX (plan H7): populate PK/SK/from from the start (not just subject/s3Key) so the SAME helper
// works for the substring tests AND for TI-1 (Task 3), where Sync indexes these fields and Search
// filters by pk="mailbox#inbox". No cross-task mutation of the helper. The dynamodbav tags are
// PK, SK, from, subject, s3Key (verified against mailbox.Header).
func ddbWith(subjects ...string) fakeDDB {
	var items []map[string]ddbtypes.AttributeValue
	for i, s := range subjects {
		id := string(rune('a' + i))
		items = append(items, map[string]ddbtypes.AttributeValue{
			"PK":      &ddbtypes.AttributeValueMemberS{Value: "mailbox#inbox"},
			"SK":      &ddbtypes.AttributeValueMemberS{Value: "2026-07-04#" + id},
			"subject": &ddbtypes.AttributeValueMemberS{Value: s},
			"from":    &ddbtypes.AttributeValueMemberS{Value: "sender-" + id + "@example.com"},
			"s3Key":   &ddbtypes.AttributeValueMemberS{Value: "inbound/" + id},
		})
	}
	return fakeDDB{out: &dynamodb.QueryOutput{Items: items}}
}

func TestSubstringSearcherMatchesSubject(t *testing.T) {
	r := mailbox.NewReader(ddbWith("café con leche", "informe mensual", "otra cosa"), fakeS3{})
	s := &substringSearcher{r: r}
	hits, err := s.Search(context.Background(), "inbox", "café", 50)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Subject != "café con leche" {
		t.Errorf("want 1 hit 'café con leche', got %v", hits)
	}
}

func TestSubstringSearcherNilReader(t *testing.T) {
	s := &substringSearcher{r: nil}
	hits, err := s.Search(context.Background(), "inbox", "x", 50)
	if err != nil || hits != nil {
		t.Errorf("nil reader: want (nil,nil), got (%v,%v)", hits, err)
	}
}
