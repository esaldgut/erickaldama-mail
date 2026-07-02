package cache

import "strings"

// EscapeFTS makes an arbitrary user string safe to pass as an FTS5 MATCH argument by wrapping it
// in double quotes and doubling any embedded double quotes (SQL-style, per sqlite.org/fts5.html §3.1).
// This neutralizes the boolean operators AND/OR/NOT and NEAR (they become literal tokens inside the
// quoted phrase) and protects special characters. Trade-off: prefix search ('*') is disabled.
func EscapeFTS(q string) string {
	return `"` + strings.ReplaceAll(q, `"`, `""`) + `"`
}
