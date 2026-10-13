package session

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/pierrec/lz4/v4"
)

// ErrNoBrowserStore means no profile of the default (or any) Firefox-family
// browser was found.
var ErrNoBrowserStore = errors.New("no Firefox-based browser profile found")

// Brightspace's session cookies have no expiry, so the browser never writes
// them to cookies.sqlite. Firefox-family browsers do save them in the session
// store, which they rewrite about every 15 seconds while running.

// firefoxFamilies maps a browser name, as it appears in its .desktop file
// name, to the directories holding its profiles.
func firefoxFamilies() map[string][]string {
	home, _ := os.UserHomeDir()
	j := func(parts ...string) string { return filepath.Join(append([]string{home}, parts...)...) }
	if runtime.GOOS == "darwin" {
		as := j("Library", "Application Support")
		return map[string][]string{
			"firefox":   {filepath.Join(as, "Firefox", "Profiles")},
			"zen":       {filepath.Join(as, "zen", "Profiles")},
			"librewolf": {filepath.Join(as, "librewolf", "Profiles")},
			"floorp":    {filepath.Join(as, "Floorp", "Profiles")},
		}
	}
	return map[string][]string{
		"firefox": {
			j(".mozilla", "firefox"),
			j(".config", "mozilla", "firefox"),
			j(".var", "app", "org.mozilla.firefox", ".mozilla", "firefox"),
			j(".var", "app", "org.mozilla.firefox", "config", "mozilla", "firefox"),
			j("snap", "firefox", "common", ".mozilla", "firefox"),
		},
		"zen":       {j(".zen"), j(".var", "app", "app.zen_browser.zen", ".zen")},
		"librewolf": {j(".librewolf"), j(".var", "app", "io.gitlab.librewolf-community", ".librewolf")},
		"floorp":    {j(".floorp"), j(".var", "app", "one.ablaze.floorp", ".floorp")},
		"waterfox":  {j(".waterfox")},
	}
}

// chromiumNames appear in the .desktop names of Chromium-family browsers,
// whose cookie stores d2l can't read.
var chromiumNames = []string{"chrom", "brave", "edge", "vivaldi", "opera", "thorium", "helium"}

// ErrChromiumDefault means the default browser is Chromium-based.
var ErrChromiumDefault = errors.New("the default browser is Chromium-based")

// defaultBrowser returns the default browser's .desktop name, lowercased, or
// "" if it can't be determined. A variable so tests can stub it.
var defaultBrowser = func() string {
	if runtime.GOOS != "linux" && runtime.GOOS != "freebsd" && runtime.GOOS != "openbsd" {
		return ""
	}
	out, err := exec.Command("xdg-settings", "get", "default-web-browser").Output()
	if err != nil {
		out, err = exec.Command("xdg-mime", "query", "default", "x-scheme-handler/https").Output()
		if err != nil {
			return ""
		}
	}
	return strings.ToLower(strings.TrimSpace(string(out)))
}

// browserRoots returns the profile directories of the default browser. When
// the default is unknown, it returns every Firefox-family location, and the
// most recently written session store wins.
func browserRoots() ([]string, error) {
	families := firefoxFamilies()
	if def := defaultBrowser(); def != "" {
		for name, roots := range families {
			if strings.Contains(def, name) {
				return roots, nil
			}
		}
		for _, name := range chromiumNames {
			if strings.Contains(def, name) {
				return nil, ErrChromiumDefault
			}
		}
	}
	var all []string
	for _, roots := range families {
		all = append(all, roots...)
	}
	return all, nil
}

type storeFile struct {
	path  string
	mtime time.Time
}

// sessionStores returns every session store file, most recently written first,
// so the profile in use wins.
func sessionStores(roots []string) []storeFile {
	var out []storeFile
	for _, root := range roots {
		for _, pattern := range []string{
			filepath.Join(root, "*", "sessionstore-backups", "recovery.jsonlz4"),
			filepath.Join(root, "*", "sessionstore.jsonlz4"),
		} {
			matches, _ := filepath.Glob(pattern)
			for _, m := range matches {
				if st, err := os.Stat(m); err == nil {
					out = append(out, storeFile{m, st.ModTime()})
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b storeFile) int { return b.mtime.Compare(a.mtime) })
	return out
}

// readMozLz4 decodes Mozilla's lz4 container: an 8-byte magic, a little-endian
// uint32 decompressed size, then one lz4 block.
func readMozLz4(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 12 || !bytes.Equal(b[:8], []byte("mozLz40\x00")) {
		return nil, fmt.Errorf("%s: not a mozlz4 file", path)
	}
	dst := make([]byte, binary.LittleEndian.Uint32(b[8:12]))
	n, err := lz4.UncompressBlock(b[12:], dst)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return dst[:n], nil
}

type storeCookie struct {
	Host             string          `json:"host"`
	Name             string          `json:"name"`
	Value            string          `json:"value"`
	Path             string          `json:"path"`
	OriginAttributes json.RawMessage `json:"originAttributes"`
}

// sessionsInStore returns one candidate session per cookie container (normal
// tabs, each container tab) that holds both Brightspace session cookies.
func sessionsInStore(path, baseURL string) ([]*Session, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	raw, err := readMozLz4(path)
	if err != nil {
		return nil, err
	}
	var store struct {
		Cookies []storeCookie `json:"cookies"`
	}
	if err := json.Unmarshal(raw, &store); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	groups := map[string]*Session{}
	var order []string
	for _, c := range store.Cookies {
		host := strings.TrimPrefix(c.Host, ".")
		if u.Hostname() != host && !strings.HasSuffix(u.Hostname(), "."+host) {
			continue
		}
		key := string(c.OriginAttributes)
		s, ok := groups[key]
		if !ok {
			s = &Session{BaseURL: baseURL, SavedAt: time.Now()}
			groups[key] = s
			order = append(order, key)
		}
		s.Cookies = append(s.Cookies, Cookie{Name: c.Name, Value: c.Value, Domain: c.Host, Path: cmp.Or(c.Path, "/")})
	}
	var out []*Session
	for _, k := range order {
		if groups[k].hasSessionCookie() {
			out = append(out, groups[k])
		}
	}
	return out, nil
}

// fingerprint identifies a candidate so the same cookies aren't validated twice.
func (s *Session) fingerprint() string {
	var b strings.Builder
	for _, c := range s.Cookies {
		if c.Name == "d2lSessionVal" || c.Name == "d2lSecureSessionVal" {
			b.WriteString(c.Name + "=" + c.Value + ";")
		}
	}
	return b.String()
}

// FromBrowser returns the first session in a Firefox-family session store that
// validate accepts. It never opens anything.
func FromBrowser(ctx context.Context, baseURL string, validate func(context.Context, *Session) error) (*Session, error) {
	roots, err := browserRoots()
	if err != nil {
		return nil, err
	}
	stores := sessionStores(roots)
	if len(stores) == 0 {
		return nil, ErrNoBrowserStore
	}
	s, err := firstValid(ctx, stores, baseURL, validate, map[string]bool{})
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("no logged-in Learn session in the browser")
	}
	return s, nil
}

func firstValid(ctx context.Context, stores []storeFile, baseURL string, validate func(context.Context, *Session) error, tried map[string]bool) (*Session, error) {
	for _, st := range stores {
		cands, err := sessionsInStore(st.path, baseURL)
		if err != nil {
			continue // a store mid-rewrite or from an unrelated browser version
		}
		for _, s := range cands {
			fp := s.fingerprint()
			if tried[fp] {
				continue
			}
			tried[fp] = true
			if err := validate(ctx, s); err == nil {
				return s, nil
			} else if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
	}
	return nil, nil
}

// LoginDefaultBrowser uses a session the default browser already has, or opens
// Learn there and waits until the browser saves a session validate accepts.
func LoginDefaultBrowser(ctx context.Context, baseURL string, validate func(context.Context, *Session) error, logf func(string, ...any)) (*Session, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	roots, err := browserRoots()
	if err != nil {
		return nil, err
	}
	stores := sessionStores(roots)
	if len(stores) == 0 {
		return nil, ErrNoBrowserStore
	}
	tried := map[string]bool{}
	if s, err := firstValid(ctx, stores, baseURL, validate, tried); s != nil || err != nil {
		return s, err
	}

	if err := openURL(baseURL + "/d2l/home"); err != nil {
		return nil, fmt.Errorf("opening the browser: %w", err)
	}
	logf("Log in to Learn in the browser tab that just opened.")
	logf("The browser saves its session about every 15 seconds, so this can take a moment after you finish.")

	seen := map[string]time.Time{}
	for _, st := range stores {
		seen[st.path] = st.mtime
	}
	deadline := time.After(5 * time.Minute)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return nil, errors.New("timed out waiting for the browser to save a Learn session. Make sure you are logged in, or try d2l login --chromium")
		case <-tick.C:
		}
		var changed []storeFile
		for _, st := range sessionStores(roots) {
			if st.mtime.After(seen[st.path]) {
				seen[st.path] = st.mtime
				changed = append(changed, st)
			}
		}
		if s, err := firstValid(ctx, changed, baseURL, validate, tried); s != nil || err != nil {
			return s, err
		}
	}
}

func openURL(u string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	cmd := exec.Command(name, u)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
