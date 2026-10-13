// Package config loads ~/.config/d2l/config.toml and resolves XDG paths.
package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	// BaseURL is the Brightspace instance, without a trailing slash.
	BaseURL string `toml:"base_url"`
	// DownloadDir is where course files are mirrored. "~" is expanded.
	DownloadDir string `toml:"download_dir"`
	// Browser is the Chromium-family binary used by `d2l login`. Empty means auto-detect.
	Browser string `toml:"browser"`
	// Openers maps a file extension (no dot) to a command that runs detached, e.g. pdf = "zathura".
	Openers map[string]string `toml:"openers"`
	// TerminalOpeners maps an extension to a command that takes over the terminal, e.g. md = "nvim".
	TerminalOpeners map[string]string `toml:"terminal_openers"`
	// Concurrency is the number of parallel downloads.
	Concurrency int `toml:"concurrency"`
}

const defaultFile = `# d2l-cli configuration

base_url = "https://learn.uwaterloo.ca"
download_dir = "~/Learn"
concurrency = 4

# Chromium-family browser used by "d2l login". Leave unset to auto-detect.
# browser = "/usr/bin/chromium-browser"

# Files open with xdg-open unless the extension is listed here.
# The file path is appended as the last argument.
[openers]
# pdf = "zathura"

# Openers that need the terminal (the TUI suspends while they run).
[terminal_openers]
# txt = "nvim"
# md = "nvim"
`

func Dir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "d2l")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "d2l")
}

func StateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "d2l")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "d2l")
}

func Path() string { return filepath.Join(Dir(), "config.toml") }

// EnsureFile writes the default config if none exists and returns its path.
func EnsureFile() (string, error) {
	p := Path()
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return "", err
	}
	return p, os.WriteFile(p, []byte(defaultFile), 0o644)
}

func Load() (*Config, error) {
	c := &Config{
		BaseURL:     "https://learn.uwaterloo.ca",
		DownloadDir: "~/Learn",
		Concurrency: 4,
	}
	if _, err := toml.DecodeFile(Path(), c); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	c.DownloadDir = expandHome(c.DownloadDir)
	if c.Concurrency < 1 {
		c.Concurrency = 1
	}
	return c, nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[1:])
	}
	return p
}
