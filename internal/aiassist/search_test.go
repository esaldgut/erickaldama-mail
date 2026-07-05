package aiassist

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"erickaldama-mail/internal/cache"
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

// fakeFTS builds an injectable ftsSearch closure recording invocation and returning canned results.
func fakeFTS(ret []mailbox.Header, err error, called *bool) func(string, string, int) ([]mailbox.Header, error) {
	return func(mb, q string, n int) ([]mailbox.Header, error) {
		*called = true
		return ret, err
	}
}

func TestFallbackUsesFTSWhenCacheOK(t *testing.T) {
	ftsCalled, subCalled := false, false
	// sub records via a reader that would return a different result; here we detect it wasn't used.
	rSub := mailbox.NewReader(ddbWith("SUBSTRING-ONLY"), fakeS3{})
	f := &fallbackSearcher{
		ftsSearch: fakeFTS([]mailbox.Header{{Subject: "from FTS"}}, nil, &ftsCalled),
		sub:       &recordingSub{inner: &substringSearcher{r: rSub}, called: &subCalled},
	}
	hits, err := f.Search(context.Background(), "inbox", "q", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !ftsCalled {
		t.Error("FTS backend was not called")
	}
	if subCalled {
		t.Error("substring was called despite FTS success")
	}
	if len(hits) != 1 || hits[0].Subject != "from FTS" {
		t.Errorf("want FTS result, got %v", hits)
	}
}

// TF-2 — THE test with teeth: 0 results from FTS (no error) must NOT trigger fallback.
func TestFallbackZeroResultsNoFallback(t *testing.T) {
	ftsCalled, subCalled := false, false
	f := &fallbackSearcher{
		ftsSearch: fakeFTS(nil, nil, &ftsCalled), // 0 results, NO error
		sub:       &recordingSub{inner: &substringSearcher{r: mailbox.NewReader(ddbWith("SHOULD-NOT-APPEAR"), fakeS3{})}, called: &subCalled},
	}
	hits, err := f.Search(context.Background(), "inbox", "q", 50)
	if err != nil {
		t.Fatal(err)
	}
	if subCalled {
		t.Error("AR-1 VIOLATED: substring called on 0-results (must only fall back on infra error)")
	}
	if len(hits) != 0 {
		t.Errorf("want 0 results, got %v", hits)
	}
}

func TestFallbackOnInfraError(t *testing.T) {
	ftsCalled, subCalled := false, false
	f := &fallbackSearcher{
		ftsSearch: fakeFTS(nil, errors.New("db locked"), &ftsCalled), // infra error → fall back
		sub:       &recordingSub{inner: &substringSearcher{r: mailbox.NewReader(ddbWith("SUBSTRING-HIT"), fakeS3{})}, called: &subCalled},
	}
	hits, err := f.Search(context.Background(), "inbox", "SUBSTRING", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !subCalled {
		t.Error("substring was NOT called after FTS infra error")
	}
	if len(hits) != 1 || hits[0].Subject != "SUBSTRING-HIT" {
		t.Errorf("want substring fallback result, got %v", hits)
	}
}

func TestFallbackNilFTSUsesSubstring(t *testing.T) {
	subCalled := false
	f := &fallbackSearcher{
		ftsSearch: nil, // no cache wired → always substring
		sub:       &recordingSub{inner: &substringSearcher{r: mailbox.NewReader(ddbWith("NIL-FTS-HIT"), fakeS3{})}, called: &subCalled},
	}
	hits, err := f.Search(context.Background(), "inbox", "NIL", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !subCalled || len(hits) != 1 {
		t.Errorf("nil FTS: want substring hit, got called=%v hits=%v", subCalled, hits)
	}
}

// recordingSub wraps a substringSearcher to record whether Search was invoked.
type recordingSub struct {
	inner  *substringSearcher
	called *bool
}

func (r *recordingSub) Search(ctx context.Context, mb, q string, n int) ([]mailbox.Header, error) {
	*r.called = true
	return r.inner.Search(ctx, mb, q, n)
}

// TI-1 — wiring: NewFallbackSearcher over a real (tmpdir) cache finds a synced header via FTS5.
func TestNewFallbackSearcherFTSWiring(t *testing.T) {
	dir := t.TempDir()
	ca, err := cache.Open(filepath.Join(dir, "index.sqlite"))
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}
	defer ca.Close()
	// Seed the cache via Sync from a fake reader that returns one header.
	r := mailbox.NewReader(ddbWith("factura de energía"), fakeS3{})
	if _, err := ca.Sync(context.Background(), r, "inbox", cache.SyncPageLimit); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	s := NewFallbackSearcher(ca, r)
	hits, err := s.Search(context.Background(), "inbox", "factura", 50)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Subject != "factura de energía" {
		t.Errorf("FTS wiring: want 'factura de energía', got %v", hits)
	}
}

func TestNewFallbackSearcherNilCache(t *testing.T) {
	r := mailbox.NewReader(ddbWith("NIL-CACHE-HIT"), fakeS3{})
	s := NewFallbackSearcher(nil, r) // nil cache → substring only
	hits, err := s.Search(context.Background(), "inbox", "NIL", 50)
	if err != nil || len(hits) != 1 {
		t.Errorf("nil cache: want 1 substring hit, got hits=%v err=%v", hits, err)
	}
}

// keep the os import used even if the platform strips it
var _ = os.Stderr
