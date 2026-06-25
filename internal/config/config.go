// Package config loads the optional ~/.config/erickaldama-mail/config.toml (XDG path).
package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config mirrors config.toml. All fields optional; mailboxes drives the multi-mailbox `ls`.
type Config struct {
	Mailboxes   []string `toml:"mailboxes"`
	DefaultFrom string   `toml:"default_from"`
	ReadProfile string   `toml:"read_profile"`
	SendProfile string   `toml:"send_profile"`
}

// Path resolves the config path via XDG explicitly. NOT os.UserConfigDir() — on macOS that returns
// ~/Library/Application Support, not ~/.config (audit B-1b).
func Path() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "erickaldama-mail", "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "erickaldama-mail", "config.toml")
}

// Load reads the config at the XDG path. Returns (cfg, ok=false, nil) if absent (not an error).
func Load() (*Config, bool, error) { return LoadFrom(Path()) }

// LoadFrom is the pure, testable form. Missing file → (zero cfg, false, nil). Malformed → error with position.
func LoadFrom(path string) (*Config, bool, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Config{}, false, nil
		}
		return nil, false, err
	}
	return &cfg, true, nil
}
