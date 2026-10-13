package session

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pierrec/lz4/v4"
)

func writeMozLz4(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, lz4.CompressBlockBound(len(raw)))
	n, err := lz4.CompressBlock(raw, buf, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := append([]byte("mozLz40\x00"), binary.LittleEndian.AppendUint32(nil, uint32(len(raw)))...)
	out = append(out, buf[:n]...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func cookie(host, name, value string, container int) map[string]any {
	return map[string]any{
		"host": host, "name": name, "value": value, "path": "/",
		"originAttributes": map[string]any{"userContextId": container},
	}
}

func stubDefault(t *testing.T, name string) {
	t.Helper()
	orig := defaultBrowser
	defaultBrowser = func() string { return name }
	t.Cleanup(func() { defaultBrowser = orig })
}

func TestFromBrowser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubDefault(t, "")
	store := filepath.Join(home, ".var/app/app.zen_browser.zen/.zen/abc.Default (release)/sessionstore-backups/recovery.jsonlz4")
	writeMozLz4(t, store, map[string]any{
		"windows": []any{},
		"cookies": []any{
			cookie("learn.uwaterloo.ca", "d2lSessionVal", "stale", 0),
			cookie("learn.uwaterloo.ca", "d2lSecureSessionVal", "stale2", 0),
			cookie("learn.uwaterloo.ca", "d2lSessionVal", "good", 2),
			cookie("learn.uwaterloo.ca", "d2lSecureSessionVal", "good2", 2),
			cookie(".uwaterloo.ca", "shared", "x", 2),
			cookie("example.com", "d2lSessionVal", "other-site", 0),
		},
	})

	var tried []string
	validate := func(_ context.Context, s *Session) error {
		tried = append(tried, s.fingerprint())
		for _, c := range s.Cookies {
			if c.Value == "good" {
				return nil
			}
		}
		return errors.New("rejected")
	}
	s, err := FromBrowser(context.Background(), "https://learn.uwaterloo.ca", validate)
	if err != nil {
		t.Fatal(err)
	}
	if len(tried) != 2 {
		t.Fatalf("validated %d candidates, want 2 (one per container)", len(tried))
	}
	names := map[string]string{}
	for _, c := range s.Cookies {
		names[c.Name] = c.Value
	}
	if names["d2lSessionVal"] != "good" || names["shared"] != "x" {
		t.Fatalf("got cookies %v", names)
	}
	if _, ok := names["d2lSessionVal"]; !ok || len(s.Cookies) != 3 {
		t.Fatalf("other-site cookie leaked or cookies missing: %v", s.Cookies)
	}
}

func TestFromBrowserNoProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stubDefault(t, "")
	_, err := FromBrowser(context.Background(), "https://learn.uwaterloo.ca", func(context.Context, *Session) error { return nil })
	if !errors.Is(err, ErrNoBrowserStore) {
		t.Fatalf("err = %v, want ErrNoBrowserStore", err)
	}
}

func TestReadMozLz4RejectsOtherFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.jsonlz4")
	os.WriteFile(p, []byte("not a mozlz4 file"), 0o644)
	if _, err := readMozLz4(p); err == nil {
		t.Fatal("expected an error")
	}
}

func TestFromBrowserFollowsDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sess := func(v string) map[string]any {
		return map[string]any{"cookies": []any{
			cookie("learn.uwaterloo.ca", "d2lSessionVal", v, 0),
			cookie("learn.uwaterloo.ca", "d2lSecureSessionVal", v, 0),
		}}
	}
	writeMozLz4(t, filepath.Join(home, ".zen/a.Default/sessionstore-backups/recovery.jsonlz4"), sess("zen"))
	writeMozLz4(t, filepath.Join(home, ".mozilla/firefox/b.default-release/sessionstore-backups/recovery.jsonlz4"), sess("firefox"))
	accept := func(context.Context, *Session) error { return nil }

	for _, tc := range []struct {
		desktop, want string
		err           error
	}{
		{"app.zen_browser.zen.desktop", "zen", nil},
		{"firefox.desktop", "firefox", nil},
		{"org.mozilla.firefox.desktop", "firefox", nil},
		{"librewolf.desktop", "", ErrNoBrowserStore},
		{"chromium-browser.desktop", "", ErrChromiumDefault},
		{"google-chrome.desktop", "", ErrChromiumDefault},
		{"brave-browser.desktop", "", ErrChromiumDefault},
	} {
		stubDefault(t, tc.desktop)
		s, err := FromBrowser(context.Background(), "https://learn.uwaterloo.ca", accept)
		if !errors.Is(err, tc.err) {
			t.Errorf("%s: err = %v, want %v", tc.desktop, err, tc.err)
			continue
		}
		if tc.err == nil && s.Cookies[0].Value != tc.want {
			t.Errorf("%s: read the %s store, want %s", tc.desktop, s.Cookies[0].Value, tc.want)
		}
	}
}
