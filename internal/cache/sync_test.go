package cache

import (
	"context"
	"path/filepath"
	"testing"

	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"erickaldama-mail/internal/mailbox"
)

// fakeLister returns a fixed page, ignoring the cursor (v0.5 fetches the full page).
type fakeLister struct{ hs []mailbox.Header }

func (f fakeLister) List(_ context.Context, _ string, _ int32, _ map[string]ddbtypes.AttributeValue) ([]mailbox.Header, map[string]ddbtypes.AttributeValue, error) {
	return f.hs, nil, nil
}

func sampleHeaders() []mailbox.Header {
	return []mailbox.Header{
		{PK: "mailbox#inbox", SK: "2026-06-25T14:32:00Z#a", S3Key: "inbound/aaa-000000", MessageID: "m1", From: "alice@example.com", Subject: "Hello", Date: "Wed, 25 Jun 2026 14:32:00 +0000"},
		{PK: "mailbox#inbox", SK: "2026-06-25T09:00:00Z#b", S3Key: "inbound/bbb-000000", MessageID: "m2", From: "bob@example.com", Subject: "Report", Date: "Wed, 25 Jun 2026 09:00:00 +0000"},
	}
}

func TestSyncPopulatesAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.sqlite")
	c, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()
	lister := fakeLister{hs: sampleHeaders()}

	n, err := c.Sync(context.Background(), lister, "inbox", 50)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if n != 2 {
		t.Fatalf("first Sync rows = %d, want 2", n)
	}
	// Second sync of the SAME page must not duplicate.
	if _, err := c.Sync(context.Background(), lister, "inbox", 50); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	var count int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM headers`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("headers count = %d, want 2 (idempotent)", count)
	}
	// FTS index must have the same number of rows (explicit sync, no triggers).
	var ftsCount int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM headers_fts`).Scan(&ftsCount); err != nil {
		t.Fatalf("fts count: %v", err)
	}
	if ftsCount != 2 {
		t.Errorf("headers_fts count = %d, want 2", ftsCount)
	}
}

// TestSyncMutationDoesNotLeaveStaleFTS is the test that catches the contentless-DELETE bug: it
// re-syncs the SAME s3Key with a CHANGED subject. If the FTS index still matched the OLD subject,
// the 'delete'-with-old-values sync path is broken. Also runs the FTS5 integrity-check.
func TestSyncMutationDoesNotLeaveStaleFTS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.sqlite")
	c, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()

	h := mailbox.Header{PK: "mailbox#inbox", SK: "2026-06-25T14:32:00Z#a", S3Key: "inbound/aaa-000000", From: "alice@example.com", Subject: "OldSubject"}
	if _, err := c.Sync(context.Background(), fakeLister{hs: []mailbox.Header{h}}, "inbox", 50); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}
	// Re-sync same s3Key with a new subject — must NOT error (contentless DELETE would) and must
	// replace the FTS term, not accumulate it.
	h.Subject = "NewSubject"
	if _, err := c.Sync(context.Background(), fakeLister{hs: []mailbox.Header{h}}, "inbox", 50); err != nil {
		t.Fatalf("Sync 2 (mutation): %v", err) // this is where `DELETE FROM contentless` blows up
	}
	var oldCount int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM headers_fts WHERE headers_fts MATCH 'OldSubject'`).Scan(&oldCount); err != nil {
		t.Fatalf("Search old: %v", err)
	}
	if oldCount != 0 {
		t.Errorf("stale FTS term: OldSubject still matches %d rows, want 0", oldCount)
	}
	var newCount int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM headers_fts WHERE headers_fts MATCH 'NewSubject'`).Scan(&newCount); err != nil {
		t.Fatalf("Search new: %v", err)
	}
	if newCount != 1 {
		t.Errorf("NewSubject matches %d rows, want 1", newCount)
	}
	// FTS5 integrity-check must pass (no orphaned/duplicate index entries).
	if _, err := c.db.Exec(`INSERT INTO headers_fts(headers_fts) VALUES('integrity-check')`); err != nil {
		t.Errorf("FTS5 integrity-check failed: %v", err)
	}
}
