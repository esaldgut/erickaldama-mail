package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesFileWith0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "index.sqlite")
	c, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o, want 600", perm)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir perm = %o, want 700", perm)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.sqlite")
	c1, err := Open(path)
	if err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	c1.Close()
	c2, err := Open(path) // re-open must not error on CREATE ... IF NOT EXISTS
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	c2.Close()
}

func TestFTS5ModuleCompiled(t *testing.T) {
	// Contract test: fails if the FTS5 module is NOT compiled into the driver.
	// Runs on CI (linux/amd64) and local (darwin/arm64) — closes research NO VERIFICADO #6.
	path := filepath.Join(t.TempDir(), "index.sqlite")
	c, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()
	// Open already runs the schema init which includes a fts5 virtual table.
	// If FTS5 were missing, Open would have failed with "no such module: fts5".
	var name string
	err = c.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='headers_fts'`).Scan(&name)
	if err != nil {
		t.Fatalf("headers_fts not created (FTS5 missing?): %v", err)
	}
	if name != "headers_fts" {
		t.Errorf("got %q, want headers_fts", name)
	}
}
