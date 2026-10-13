package session

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BalajiLeninrajan/d2l-cli/internal/config"
)

// Family is the automation protocol a browser speaks.
type Family int

const (
	FamilyUnknown  Family = iota
	FamilyChromium        // Chrome DevTools Protocol
	FamilyFirefox         // WebDriver BiDi
)

// Browser is a launchable browser.
type Browser struct {
	Name   string   // for messages and the profile dir, e.g. "zen" or "chromium-browser"
	Argv   []string // command prefix; d2l appends its flags and the URL
	Family Family
	// Flatpak and Snap browsers can only write inside their sandbox, so the
	// login profile has to live there.
	Flatpak string // app ID
	Snap    string // snap name
}

// ErrNoBrowser means no browser d2l can drive was found.
var ErrNoBrowser = errors.New("no supported browser found (Chromium- or Firefox-based). Set browser in the config (d2l config --edit) or use d2l login --cookie -")

var (
	firefoxWords  = []string{"firefox", "zen", "librewolf", "floorp", "waterfox", "icecat", "mullvad"}
	chromiumWords = []string{"chrom", "brave", "edge", "vivaldi", "opera", "thorium", "helium", "yandex"}
)

// familyOf guesses the protocol from a browser's name, desktop ID, bundle ID
// or executable.
func familyOf(names ...string) Family {
	s := strings.ToLower(strings.Join(names, " "))
	for _, w := range firefoxWords {
		if strings.Contains(s, w) {
			return FamilyFirefox
		}
	}
	for _, w := range chromiumWords {
		if strings.Contains(s, w) {
			return FamilyChromium
		}
	}
	return FamilyUnknown
}

// newBrowser builds a Browser from a command line, detecting Flatpak and Snap
// wrappers. id is the desktop or bundle ID, if known.
func newBrowser(id string, argv []string) *Browser {
	b := &Browser{Argv: argv}
	exe := filepath.Base(argv[0])
	if exe == "flatpak" {
		b.Flatpak = flatpakAppID(argv)
	}
	if strings.HasPrefix(argv[0], "/snap/bin/") {
		b.Snap = exe
	} else if p, err := exec.LookPath(argv[0]); err == nil {
		if real, err := filepath.EvalSymlinks(p); err == nil && filepath.Base(real) == "snap" {
			b.Snap = exe
		}
	}
	switch {
	case b.Flatpak != "":
		b.Name = b.Flatpak
	case id != "":
		b.Name = strings.TrimSuffix(id, ".desktop")
	default:
		b.Name = strings.TrimSuffix(exe, ".exe")
	}
	b.Family = familyOf(id, exe, b.Flatpak)
	return b
}

// flatpakAppID returns the app ID in `flatpak run [options] APP [args]`.
func flatpakAppID(argv []string) string {
	for i, a := range argv {
		if a != "run" {
			continue
		}
		for _, b := range argv[i+1:] {
			if !strings.HasPrefix(b, "-") {
				return b
			}
		}
	}
	return ""
}

// ProfileDir is the browser profile d2l logs in with. It is separate from
// the user's own profile and persists so SSO can remember them.
func (b *Browser) ProfileDir() string {
	home, _ := os.UserHomeDir()
	switch {
	case b.Flatpak != "":
		return filepath.Join(home, ".var", "app", b.Flatpak, "data", "d2l-login")
	case b.Snap != "":
		return filepath.Join(home, "snap", b.Snap, "common", "d2l-login")
	}
	return filepath.Join(config.StateDir(), "browser", sanitizeName(b.Name))
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitizeName(s string) string {
	s = unsafeName.ReplaceAllString(s, "-")
	if s == "" || s == "." || s == ".." {
		return "browser"
	}
	return s
}

// DeleteProfiles removes every login profile d2l created.
func DeleteProfiles() error {
	home, _ := os.UserHomeDir()
	dirs := []string{filepath.Join(config.StateDir(), "browser")}
	for _, pattern := range []string{
		filepath.Join(home, ".var", "app", "*", "data", "d2l-login"),
		filepath.Join(home, "snap", "*", "common", "d2l-login"),
	} {
		m, _ := filepath.Glob(pattern)
		dirs = append(dirs, m...)
	}
	var errs []error
	for _, d := range dirs {
		errs = append(errs, os.RemoveAll(d))
	}
	return errors.Join(errs...)
}

// ResolveBrowser picks the browser to log in with: the configured one, else
// the system default, else any installed browser d2l can drive.
func ResolveBrowser(configured string, logf func(string, ...any)) (*Browser, error) {
	if configured != "" {
		argv := splitCommand(configured)
		if _, err := os.Stat(configured); err == nil {
			argv = []string{configured} // a plain path, possibly with spaces or backslashes
		}
		if len(argv) == 0 {
			return nil, errors.New("browser in the config is empty")
		}
		if _, err := exec.LookPath(argv[0]); err != nil {
			return nil, fmt.Errorf("browser %q from the config: %w", configured, err)
		}
		b := newBrowser("", argv)
		if b.Family == FamilyUnknown {
			b.Family = FamilyChromium
		}
		return b, nil
	}
	def, err := defaultBrowser()
	if err == nil && def.Family != FamilyUnknown {
		return def, nil
	}
	if b := installedBrowser(); b != nil {
		switch {
		case err != nil:
			logf("Could not find your default browser (%v), so using %s.", err, b.Name)
		default:
			logf("d2l can't drive your default browser (%s), so using %s.", def.Name, b.Name)
		}
		return b, nil
	}
	return nil, ErrNoBrowser
}

// installedBrowser returns the first browser from installedCandidates that
// exists, Chromium-based first.
func installedBrowser() *Browser {
	for _, argv := range installedCandidates() {
		if _, err := exec.LookPath(argv[0]); err != nil {
			continue
		}
		if len(argv) > 2 && argv[1] == "run" && !flatpakInstalled(argv[len(argv)-1]) {
			continue
		}
		return newBrowser("", argv)
	}
	return nil
}

func flatpakInstalled(appID string) bool {
	return exec.Command("flatpak", "info", appID).Run() == nil
}

// splitCommand splits a command line on spaces, honouring double quotes and
// backslash escapes the way desktop entry Exec keys do.
func splitCommand(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote, have := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && (inQuote || s[i+1] == ' ' || s[i+1] == '"'):
			i++
			cur.WriteByte(s[i])
			have = true
		case c == '"':
			inQuote = !inQuote
			have = true
		case (c == ' ' || c == '\t') && !inQuote:
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteByte(c)
			have = true
		}
	}
	if have {
		out = append(out, cur.String())
	}
	return out
}
