// Package cache is a discardable local SQLite+FTS5 cache of mail headers. It decorates
// mailbox.Reader: DynamoDB stays the source of truth; the cache accelerates ls and enables
// full-text search. FTS5 is contentless — the index is synced explicitly by Go (no triggers).
package cache

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (NOT "sqlite3")
)

// Cache wraps a single-connection SQLite handle over the header index.
type Cache struct{ db *sql.DB }

// DefaultPath returns $XDG_CACHE_HOME/erickaldama-mail/index.sqlite, or ~/.cache/... as fallback.
func DefaultPath() (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cache path: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "erickaldama-mail", "index.sqlite"), nil
}

// Open opens (creating if needed) the cache at path. It ensures the directory is 0700 and the
// database file is 0600 (SQLite does not control file permissions), applies per-connection
// PRAGMAs, and runs the idempotent schema. The cache is discardable: callers should fall back
// to the live Reader if Open fails.
func Open(path string) (*Cache, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("cache mkdir: %w", err)
	}
	// Create the file with 0600 BEFORE SQLite opens it (SQLite would otherwise use the umask).
	f, err := os.OpenFile(path, os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cache create: %w", err)
	}
	f.Close()
	// Belt-and-suspenders: enforce 0600 even if the file pre-existed with looser perms.
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("cache chmod: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("cache open: %w", err)
	}
	db.SetMaxOpenConns(1) // single-process cache; serialize access, avoid SQLITE_BUSY churn

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",   // persistent, set once
		"PRAGMA synchronous=NORMAL", // per-connection; durability loss OK (cache is discardable)
		"PRAGMA busy_timeout=5000",  // per-connection; sleep-retry up to 5s on lock
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("cache pragma %q: %w", pragma, err)
		}
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("cache schema: %w", err)
	}
	// WAL creates sidecar -wal/-shm files with the process umask (SQLite doesn't control perms).
	// They hold un-checkpointed header pages → tighten to 0600 too (audit m-4). Best-effort: ignore
	// ENOENT (may not exist yet before the first write).
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Chmod(path+suffix, 0o600); err != nil && !os.IsNotExist(err) {
			db.Close()
			return nil, fmt.Errorf("cache chmod %s: %w", suffix, err)
		}
	}
	return &Cache{db: db}, nil
}

// Close closes the underlying database handle.
func (c *Cache) Close() error { return c.db.Close() }
