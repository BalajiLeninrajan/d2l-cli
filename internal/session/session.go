// Package session stores the Brightspace session cookies and obtains them
// by driving a real Chromium window through SSO.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BalajiLeninrajan/d2l-cli/internal/config"
)

// ErrNoSession means the user has never logged in (or logged out).
var ErrNoSession = errors.New("not logged in; run `d2l login`")

type Cookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
}

type Session struct {
	BaseURL string    `json:"base_url"`
	Cookies []Cookie  `json:"cookies"`
	SavedAt time.Time `json:"saved_at"`
}

func path() string { return filepath.Join(config.StateDir(), "session.json") }

func Load() (*Session, error) {
	b, err := os.ReadFile(path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("corrupt session file %s: %w", path(), err)
	}
	return &s, nil
}

func (s *Session) Save() error {
	if err := os.MkdirAll(config.StateDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path())
}

func Delete() error {
	err := os.Remove(path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Session) HTTPCookies() []*http.Cookie {
	out := make([]*http.Cookie, 0, len(s.Cookies))
	for _, c := range s.Cookies {
		out = append(out, &http.Cookie{Name: c.Name, Value: c.Value, Path: c.Path})
	}
	return out
}

// FromHeader parses a pasted "a=b; c=d" Cookie header.
func FromHeader(baseURL, header string) (*Session, error) {
	s := &Session{BaseURL: baseURL, SavedAt: time.Now()}
	for part := range strings.SplitSeq(header, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || name == "" {
			continue
		}
		s.Cookies = append(s.Cookies, Cookie{Name: name, Value: value, Path: "/"})
	}
	if !s.hasSessionCookie() {
		return nil, errors.New("cookie string must include d2lSessionVal and d2lSecureSessionVal")
	}
	return s, nil
}

func (s *Session) hasSessionCookie() bool {
	var plain, secure bool
	for _, c := range s.Cookies {
		plain = plain || c.Name == "d2lSessionVal"
		secure = secure || c.Name == "d2lSecureSessionVal"
	}
	return plain && secure
}
