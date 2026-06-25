package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromParses(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	os.WriteFile(p, []byte(`
mailboxes    = ["erick@erickaldama.com", "test@erickaldama.com"]
default_from = "erick@erickaldama.com"
read_profile = "mail-client-read"
`), 0o600)
	cfg, ok, err := LoadFrom(p)
	if err != nil || !ok {
		t.Fatalf("LoadFrom: ok=%v err=%v", ok, err)
	}
	if len(cfg.Mailboxes) != 2 || cfg.Mailboxes[0] != "erick@erickaldama.com" {
		t.Fatalf("mailboxes: %v", cfg.Mailboxes)
	}
	if cfg.DefaultFrom != "erick@erickaldama.com" || cfg.ReadProfile != "mail-client-read" {
		t.Fatalf("fields: %+v", cfg)
	}
}

func TestLoadFromMissingIsNotError(t *testing.T) {
	cfg, ok, err := LoadFrom(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if ok {
		t.Fatal("ok should be false for missing file")
	}
	if cfg == nil {
		t.Fatal("cfg should be non-nil zero value")
	}
}

func TestPathRespectsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdgtest")
	got := Path()
	want := "/tmp/xdgtest/erickaldama-mail/config.toml"
	if got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestLoadFromMalformed(t *testing.T) { // GAP-2: TOML inválido → error informativo
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.toml")
	os.WriteFile(p, []byte(`mailboxes = [unclosed`), 0o600)
	_, ok, err := LoadFrom(p)
	if err == nil {
		t.Fatal("malformed TOML must return error")
	}
	if ok {
		t.Fatal("ok should be false on error")
	}
	if err.Error() == "" {
		t.Fatal("error must be non-empty (BurntSushi includes line info)")
	}
}
