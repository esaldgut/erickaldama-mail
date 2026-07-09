package cache

import "strings"

// EscapeFTS makes an arbitrary user string safe to pass as an FTS5 MATCH argument by wrapping it
// in double quotes and doubling any embedded double quotes (SQL-style, per sqlite.org/fts5.html §3.1).
// This neutralizes the boolean operators AND/OR/NOT and NEAR (they become literal tokens inside the
// quoted phrase) and protects special characters.
//
// A trailing '*' is appended OUTSIDE the closing quote to enable prefix match on the last token of
// the phrase, per sqlite.org/fts5.html §3.3: "If a '*' character follows a string within an FTS
// expression, then the final token extracted from the string is marked as a prefix token." Per the
// same section, the '*' must follow the closing quote (`"foo bar"*`) — placing it inside the quotes
// (`"foo bar*"`) is discarded by the tokenizer and has no effect. This lets "transfer" match
// "transferencia" while keeping the whole phrase quoted (operators stay neutralized).
//
// The empty string is a special case: it is returned as `""` with NO trailing '*'. This is the
// query the TUI filter sends when cleared/cancelled; `""*` is invalid/undefined FTS5 syntax and must
// not be produced.
func EscapeFTS(q string) string {
	escaped := `"` + strings.ReplaceAll(q, `"`, `""`) + `"`
	if q == "" {
		return escaped
	}
	return escaped + "*"
}
