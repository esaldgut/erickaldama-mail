package cache

import (
	"strings"
	"time"
)

// FormatDate parses common email Date header formats and returns "2006-01-02 15:04" in LOCAL time
// (24h, ordenable). Falls back to the raw trimmed string if none of the layouts parse — same
// tolerance strategy as v0.4's shortDate, but with time-of-day so same-day mail is distinguishable.
func FormatDate(raw string) string {
	d := strings.TrimSpace(raw)
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, "2 Jan 2006 15:04:05 -0700", time.RFC3339} {
		if t, err := time.Parse(layout, d); err == nil {
			return t.Local().Format("2006-01-02 15:04")
		}
	}
	return d
}
