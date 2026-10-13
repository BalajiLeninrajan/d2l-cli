package session

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// TestLiveBrowser drives a real browser headless against a local server that
// sets session-only, HttpOnly cookies the way Learn does. Opt in with
// D2L_LIVE_BROWSER=default (the system default) or a browser command.
func TestLiveBrowser(t *testing.T) {
	which := os.Getenv("D2L_LIVE_BROWSER")
	if which == "" {
		t.Skip("set D2L_LIVE_BROWSER=default or a browser command to run")
	}
	if which == "default" {
		which = ""
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "d2lSessionVal", Value: "plain123", Path: "/", HttpOnly: true})
		http.SetCookie(w, &http.Cookie{Name: "d2lSecureSessionVal", Value: "secure456", Path: "/", HttpOnly: true})
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<title>Learn</title>ok"))
	}))
	defer srv.Close()

	b, err := ResolveBrowser(which, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("browser %s %v family=%d", b.Name, b.Argv, b.Family)
	profile := t.TempDir()
	if b.Flatpak != "" || b.Snap != "" {
		profile = b.ProfileDir() + "-test" // the sandbox can't see the temp dir
		t.Cleanup(func() { os.RemoveAll(profile) })
	}

	validate := func(_ context.Context, s *Session) error {
		got := map[string]string{}
		for _, c := range s.Cookies {
			got[c.Name] = c.Value
		}
		if got["d2lSessionVal"] != "plain123" || got["d2lSecureSessionVal"] != "secure456" {
			return errors.New("wrong cookies")
		}
		return nil
	}
	start := time.Now()
	s, err := runLogin(context.Background(), LoginOptions{BaseURL: srv.URL, Validate: validate}, b, profile, true, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("captured %d cookies in %v", len(s.Cookies), time.Since(start).Round(time.Millisecond))
}
