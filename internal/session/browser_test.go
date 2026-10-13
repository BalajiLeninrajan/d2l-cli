package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestMain doubles as a fake browser: with D2L_FAKE_BROWSER set, the test
// binary prints a debugging endpoint like a real browser and serves cookies.
func TestMain(m *testing.M) {
	if family := os.Getenv("D2L_FAKE_BROWSER"); family != "" {
		fakeBrowser(family)
		return
	}
	os.Exit(m.Run())
}

// fakeBrowser serves anonymous session cookies for the first two cookie
// requests and the logged-in pair after that.
func fakeBrowser(family string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	calls := 0
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := context.Background()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var req struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			json.Unmarshal(data, &req)
			value := "anon"
			if calls++; calls > 2 {
				value = "real"
			}
			var result any = map[string]any{}
			switch req.Method {
			case "Storage.getCookies":
				result = map[string]any{"cookies": []any{
					map[string]any{"name": "d2lSessionVal", "value": value, "domain": "learn.example", "path": "/"},
					map[string]any{"name": "d2lSecureSessionVal", "value": value, "domain": "learn.example", "path": "/"},
					map[string]any{"name": "other", "value": "x", "domain": "elsewhere.example", "path": "/"},
				}}
			case "storage.getCookies":
				str := func(v string) map[string]any { return map[string]any{"type": "string", "value": v} }
				result = map[string]any{"cookies": []any{
					map[string]any{"name": "d2lSessionVal", "value": str(value), "domain": ".learn.example", "path": "/"},
					map[string]any{"name": "d2lSecureSessionVal", "value": str(value), "domain": ".learn.example", "path": "/"},
				}}
			}
			// An event first, which call must skip.
			c.Write(ctx, websocket.MessageText, []byte(`{"type":"event","method":"log.entryAdded","params":{}}`))
			resp, _ := json.Marshal(map[string]any{"id": req.ID, "type": "success", "result": result})
			c.Write(ctx, websocket.MessageText, resp)
			if req.Method == "Browser.close" || req.Method == "browser.close" {
				os.Exit(0)
			}
		}
	})}
	if family == "firefox" {
		fmt.Fprintf(os.Stderr, "WebDriver BiDi listening on ws://%s\n", ln.Addr())
	} else {
		fmt.Fprintf(os.Stderr, "DevTools listening on ws://%s/devtools/browser/abc\n", ln.Addr())
	}
	srv.Serve(ln)
}

func TestLoginWithFakeBrowser(t *testing.T) {
	for _, family := range []Family{FamilyChromium, FamilyFirefox} {
		name := map[Family]string{FamilyChromium: "chromium", FamilyFirefox: "firefox"}[family]
		t.Run(name, func(t *testing.T) {
			t.Setenv("D2L_FAKE_BROWSER", name)
			b := &Browser{Name: "fake", Argv: []string{os.Args[0]}, Family: family}
			rejected := 0
			validate := func(_ context.Context, s *Session) error {
				if s.Cookies[0].Value != "real" {
					rejected++
					return errors.New("not logged in yet")
				}
				return nil
			}
			profile := t.TempDir()
			s, err := runLogin(context.Background(), LoginOptions{BaseURL: "https://learn.example", Validate: validate}, b, profile, true, 30*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if rejected != 1 || len(s.Cookies) != 2 {
				t.Fatalf("rejected=%d cookies=%v, want one rejection and two learn cookies", rejected, s.Cookies)
			}
			_, err = os.Stat(filepath.Join(profile, "user.js"))
			if (family == FamilyFirefox) != (err == nil) {
				t.Errorf("user.js present=%v, want %v", err == nil, family == FamilyFirefox)
			}
		})
	}
}

func TestLoginBrowserExitsEarly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	b := &Browser{Name: "broken", Argv: []string{"/bin/sh", "-c", "echo profile is locked >&2; exit 1", "sh"}, Family: FamilyChromium}
	_, err := runLogin(context.Background(), LoginOptions{BaseURL: "https://learn.example"}, b, t.TempDir(), true, 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "profile is locked") {
		t.Fatalf("err = %v, want the browser's output in it", err)
	}
}

func TestSplitCommand(t *testing.T) {
	for in, want := range map[string][]string{
		`/usr/bin/chromium-browser %U`:                {"/usr/bin/chromium-browser", "%U"},
		`"/opt/My Browser/browser" --flag`:            {"/opt/My Browser/browser", "--flag"},
		`env A=b\ c firefox`:                          {"env", "A=b c", "firefox"},
		`  zen   `:                                    {"zen"},
		`flatpak run "--command=launch script.sh" id`: {"flatpak", "run", "--command=launch script.sh", "id"},
	} {
		if got := splitCommand(in); !slices.Equal(got, want) {
			t.Errorf("splitCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFamilyOf(t *testing.T) {
	for names, want := range map[string]Family{
		"app.zen_browser.zen.desktop": FamilyFirefox,
		"firefox.desktop":             FamilyFirefox,
		"org.mozilla.firefox":         FamilyFirefox,
		"librewolf":                   FamilyFirefox,
		"chromium-browser.desktop":    FamilyChromium,
		"google-chrome":               FamilyChromium,
		"com.brave.Browser":           FamilyChromium,
		"msedge":                      FamilyChromium,
		"com.apple.safari":            FamilyUnknown,
		"org.gnome.Epiphany":          FamilyUnknown,
	} {
		if got := familyOf(names); got != want {
			t.Errorf("familyOf(%q) = %d, want %d", names, got, want)
		}
	}
}

func TestNewBrowserFlatpak(t *testing.T) {
	b := newBrowser("app.zen_browser.zen.desktop", []string{"/usr/bin/flatpak", "run", "--branch=stable", "--command=launch-script.sh", "app.zen_browser.zen"})
	if b.Flatpak != "app.zen_browser.zen" || b.Family != FamilyFirefox || b.Name != "app.zen_browser.zen" {
		t.Fatalf("got %+v", b)
	}
	home, _ := os.UserHomeDir()
	if want := filepath.Join(home, ".var/app/app.zen_browser.zen/data/d2l-login"); b.ProfileDir() != want {
		t.Errorf("ProfileDir = %s, want %s", b.ProfileDir(), want)
	}
}

func TestSessionFrom(t *testing.T) {
	s := sessionFrom("https://learn.uwaterloo.ca", []Cookie{
		{Name: "d2lSessionVal", Value: "a", Domain: "learn.uwaterloo.ca"},
		{Name: "d2lSecureSessionVal", Value: "b", Domain: "learn.uwaterloo.ca", Path: "/"},
		{Name: "sso", Value: "c", Domain: ".uwaterloo.ca", Path: "/"},
		{Name: "evil", Value: "d", Domain: "notuwaterloo.ca", Path: "/"},
		{Name: "other", Value: "e", Domain: "example.com", Path: "/"},
	})
	var names []string
	for _, c := range s.Cookies {
		names = append(names, c.Name)
		if c.Path == "" {
			t.Errorf("%s has an empty path", c.Name)
		}
	}
	if !slices.Equal(names, []string{"d2lSessionVal", "d2lSecureSessionVal", "sso"}) {
		t.Fatalf("kept %v", names)
	}
}
