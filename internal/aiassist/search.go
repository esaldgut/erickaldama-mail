package aiassist

import (
	"context"
	"strings"

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
	hs, _, err := s.r.List(ctx, mb, int32(limit), nil)
	if err != nil {
		return nil, err
	}
	var hits []mailbox.Header
	lq := strings.ToLower(query)
	for _, h := range hs {
		// stdlib case-insensitive contains — handles Unicode (e.g. "café"); NO hand-rolled toLower (audit).
		if strings.Contains(strings.ToLower(h.Subject), lq) {
			hits = append(hits, h)
		}
	}
	return hits, nil
}
