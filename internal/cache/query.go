package cache

import (
	"fmt"

	"erickaldama-mail/internal/mailbox"
)

// scanHeaders reads a *sql.Rows of the header columns into []mailbox.Header.
func scanHeaders(rowsQuery func() (rows, error)) ([]mailbox.Header, error) {
	rs, err := rowsQuery()
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []mailbox.Header
	for rs.Next() {
		var h mailbox.Header
		if err := rs.Scan(&h.S3Key, &h.PK, &h.SK, &h.MessageID, &h.From, &h.Subject, &h.Date); err != nil {
			return nil, fmt.Errorf("cache scan: %w", err)
		}
		out = append(out, h)
	}
	return out, rs.Err()
}

// rows is the subset of *sql.Rows scanHeaders needs.
type rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Err() error
}

const headerCols = `s3Key, pk, sk, messageId, sender, subject, date`

// List returns cached headers for the mailbox, newest first (sk DESC), capped at limit.
func (c *Cache) List(mailbox string, limit int) ([]mailbox.Header, error) {
	return scanHeaders(func() (rows, error) {
		return c.db.Query(
			`SELECT `+headerCols+` FROM headers WHERE pk=? ORDER BY sk DESC LIMIT ?`,
			"mailbox#"+mailbox, limit,
		)
	})
}

// Search returns cached headers for the mailbox whose sender/subject match the FTS5 query,
// ranked best-first. The query is escaped (EscapeFTS) to neutralize FTS operators.
func (c *Cache) Search(mailbox, query string, limit int) ([]mailbox.Header, error) {
	return scanHeaders(func() (rows, error) {
		return c.db.Query(
			`SELECT `+prefixCols("h")+`
			 FROM headers_fts f JOIN headers h ON h.rowid = f.rowid
			 WHERE f.headers_fts MATCH ? AND h.pk = ?
			 ORDER BY rank LIMIT ?`,
			EscapeFTS(query), "mailbox#"+mailbox, limit,
		)
	})
}

// prefixCols qualifies headerCols with a table alias for the join query.
func prefixCols(alias string) string {
	return alias + ".s3Key, " + alias + ".pk, " + alias + ".sk, " + alias + ".messageId, " +
		alias + ".sender, " + alias + ".subject, " + alias + ".date"
}
