package aiassist

import (
	"context"
	"fmt"
	"os"
	"strings"

	"erickaldama-mail/internal/cache"
	"erickaldama-mail/internal/mailbox"
)

// Searcher busca correos por sender/subject y devuelve headers. La firma lleva ctx porque el substring
// backend lo necesita para reader.List; el FTS lo ignora, pero la firma se mantiene uniforme (AR-4).
// Precondición (AUDIT FIX H2): el caller normaliza `limit` a [1, 1000] antes de invocar (execTool lo
// hace: <=0→50, >1000→1000). Un `limit` fuera de rango es responsabilidad del caller, no del Searcher.
type Searcher interface {
	Search(ctx context.Context, mb, query string, limit int) ([]mailbox.Header, error)
}

// substringSearcher — el comportamiento histórico (fallback): reader.List(limit) + Contains sobre Subject.
// AUDIT FIX (plan H1): `limit` acota las FILAS que reader.List trae de DynamoDB, NO los matches — el
// substring degradado puede devolver menos que `limit` aunque existan más matches en filas no leídas;
// el FTS sí devuelve hasta `limit` matches. Coherente con spec §8 (limit = tope de filas, no cursor).
// El caller (execTool) garantiza `limit` en [1, 1000], así que int32(limit) nunca overflowea aquí.
type substringSearcher struct{ r *mailbox.Reader }

func (s *substringSearcher) Search(ctx context.Context, mb, query string, limit int) ([]mailbox.Header, error) {
	if s.r == nil {
		return nil, nil
	}
	// Empty query matches nothing — mirror the FTS backend, where EscapeFTS("") yields a MATCH that
	// returns no rows. Without this guard, strings.Contains(x, "") is always true and the fallback would
	// return every header (a wall of noise), diverging from FTS on the same tool call (whole-branch review).
	if query == "" {
		return nil, nil
	}
	hs, _, err := s.r.List(ctx, mb, int32(limit), nil)
	if err != nil {
		return nil, err
	}
	var hits []mailbox.Header
	lq := strings.ToLower(query)
	for _, h := range hs {
		// Match sender OR subject — same field coverage as the FTS backend (fts5(sender, subject)) and the
		// tool's "sender or subject" promise. stdlib case-insensitive Contains handles Unicode (e.g. "café").
		if strings.Contains(strings.ToLower(h.Subject), lq) ||
			strings.Contains(strings.ToLower(h.From), lq) {
			hits = append(hits, h)
		}
	}
	return hits, nil
}

// fallbackSearcher — FTS5 si el backend funciona, substring SOLO ante error de infra (AR-1).
// Un resultado de 0 filas sin error NO dispara fallback (es una respuesta válida "sin coincidencias").
// ftsSearch es inyectable: en producción envuelve cache.Search; nil → siempre substring; en tests, un fake.
type fallbackSearcher struct {
	ftsSearch func(mb, q string, n int) ([]mailbox.Header, error)
	sub       Searcher // substringSearcher en producción; recordingSub en tests
}

func (f *fallbackSearcher) Search(ctx context.Context, mb, query string, limit int) ([]mailbox.Header, error) {
	if f.ftsSearch != nil {
		hs, err := f.ftsSearch(mb, query, limit)
		if err == nil {
			return hs, nil // incluye 0 resultados = respuesta válida (AR-1)
		}
		// err REAL de infra (DB corrupta/bloqueada) → log a stderr (AR-3, nunca al LLM) + cae a substring.
		fmt.Fprintf(os.Stderr, "warning: FTS5 search failed, falling back to substring: %v\n", err)
	}
	return f.sub.Search(ctx, mb, query, limit)
}

// ftsSearchFromCache adapts (*cache.Cache).Search to the fallbackSearcher.ftsSearch closure shape.
// cache.Search already applies EscapeFTS internally (CT-4), so the raw query is passed through.
func ftsSearchFromCache(c *cache.Cache) func(mb, q string, n int) ([]mailbox.Header, error) {
	return func(mb, q string, n int) ([]mailbox.Header, error) {
		return c.Search(mb, q, n)
	}
}

// NewFallbackSearcher builds the composite Searcher. nil-safe: c==nil → the fts backend is nil → the
// composite always uses substring (AR-1, ER-1). The substring backend always wraps the live Reader.
func NewFallbackSearcher(c *cache.Cache, r *mailbox.Reader) Searcher {
	var fts func(mb, q string, n int) ([]mailbox.Header, error)
	if c != nil {
		fts = ftsSearchFromCache(c)
	}
	return &fallbackSearcher{ftsSearch: fts, sub: &substringSearcher{r: r}}
}
