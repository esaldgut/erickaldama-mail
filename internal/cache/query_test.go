package cache

import (
	"context"
	"path/filepath"
	"testing"
)

func seededCache(t *testing.T) *Cache {
	t.Helper()
	c, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := c.Sync(context.Background(), fakeLister{hs: sampleHeaders()}, "inbox", 50); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return c
}

func TestListNewestFirst(t *testing.T) {
	c := seededCache(t)
	defer c.Close()
	got, err := c.List("inbox", 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	// sample: SK "...14:32...#a" > "...09:00...#b" → 'a' (Hello) first.
	if got[0].Subject != "Hello" {
		t.Errorf("first subject = %q, want Hello (newest-first)", got[0].Subject)
	}
	if got[0].S3Key != "inbound/aaa-000000" {
		t.Errorf("first s3Key = %q, want inbound/aaa-000000", got[0].S3Key)
	}
}

func TestSearchMatchesSubject(t *testing.T) {
	c := seededCache(t)
	defer c.Close()
	got, err := c.Search("inbox", "Report", 50)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Subject != "Report" {
		t.Errorf("subject = %q, want Report", got[0].Subject)
	}
}

func TestSearchMatchesSender(t *testing.T) {
	c := seededCache(t)
	defer c.Close()
	got, err := c.Search("inbox", "alice", 50)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].From != "alice@example.com" {
		t.Errorf("got %+v, want 1 result from alice", got)
	}
}
