package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/BalajiLeninrajan/d2l-cli/internal/config"
)

// LoginOptions controls how Login drives the browser.
type LoginOptions struct {
	BaseURL string
	Browser string // empty means search for one
	// Interactive skips the silent headless attempt and opens a window right away.
	Interactive bool
	// Silent only tries the hidden browser and never opens a window.
	Silent bool
	// Logf receives progress lines.
	Logf func(format string, args ...any)
}

// ErrNoBrowser means no Chromium-family browser was found.
var ErrNoBrowser = errors.New("no Chromium-family browser found. Set browser in the config (d2l config --edit) or use d2l login --cookie -")

const silentTimeout = 25 * time.Second

// Login opens Chromium on the Brightspace home page and waits until SSO lands
// back on /d2l/, then captures the session cookies.
//
// The browser uses a dedicated profile under the state dir, so if the identity
// provider remembers you ("stay signed in"), a headless run can refresh the
// session without showing a window.
func Login(ctx context.Context, o LoginOptions) (*Session, error) {
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	exe, err := findBrowser(o.Browser)
	if err != nil {
		return nil, err
	}
	// A first login has no profile for the identity provider to remember,
	// so a hidden attempt could only time out.
	_, statErr := os.Stat(profileDir())
	hasProfile := statErr == nil
	if o.Silent {
		if !hasProfile {
			return nil, errors.New("no saved browser profile")
		}
		return runBrowser(ctx, o, exe, true, silentTimeout)
	}
	if !o.Interactive && hasProfile {
		logf("Trying silent login with the saved browser profile...")
		s, err := runBrowser(ctx, o, exe, true, silentTimeout)
		if err == nil || ctx.Err() != nil {
			return s, err
		}
		logf("Silent login didn't finish (%v); opening a browser window.", err)
	}
	if !hasDisplay() {
		return nil, errors.New("no display to open a browser window on. Log in on a machine with a desktop, or use d2l login --cookie -")
	}
	logf("Log in through the browser window. It closes on its own when you reach Learn.")
	s, err := runBrowser(ctx, o, exe, false, 5*time.Minute)
	if err != nil && ctx.Err() == nil && strings.HasPrefix(err.Error(), "starting browser") {
		err = fmt.Errorf("%w. If the browser cannot run here, use d2l login --cookie -", err)
	}
	return s, err
}

func profileDir() string { return filepath.Join(config.StateDir(), "browser") }

// browserNames are tried in order when the config names no browser. It
// extends chromedp's list with other Chromium-family browsers.
func browserNames() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	case "windows":
		return []string{
			"chrome", "msedge", "brave",
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		}
	}
	return []string{
		"chromium", "chromium-browser", "chromium-freeworld",
		"google-chrome", "google-chrome-stable", "google-chrome-beta", "google-chrome-unstable",
		"brave-browser", "brave", "microsoft-edge", "microsoft-edge-stable",
		"chrome", "/usr/bin/google-chrome", "/snap/bin/chromium",
	}
}

// findBrowser resolves the browser to run: the configured one if set,
// otherwise the first of browserNames that exists.
func findBrowser(configured string) (string, error) {
	if configured != "" {
		p, err := exec.LookPath(configured)
		if err != nil {
			return "", fmt.Errorf("browser %q from the config: %w", configured, err)
		}
		return p, nil
	}
	for _, name := range browserNames() {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", ErrNoBrowser
}

// hasDisplay reports whether a window can be shown. Only Linux and the BSDs
// need a display server.
func hasDisplay() bool {
	switch runtime.GOOS {
	case "darwin", "windows":
		return true
	}
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

func runBrowser(ctx context.Context, o LoginOptions, exe string, headless bool, timeout time.Duration) (*Session, error) {
	// The timeout covers starting the browser and the first page load too,
	// which can hang on an unresponsive sign-in page.
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserDataDir(profileDir()),
		chromedp.ExecPath(exe),
	)
	if !headless {
		opts = append(opts, chromedp.VisibleWindow)
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(tctx, opts...)
	defer cancelAlloc()
	bctx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	// stopped explains why bctx ended: the caller cancelled, the timeout
	// passed, or the user closed the window.
	stopped := func(err error) error {
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case tctx.Err() != nil:
			return errors.New("timed out waiting for login")
		case bctx.Err() != nil:
			return errors.New("browser closed before login finished")
		}
		return err
	}

	if err := chromedp.Do(bctx, chromedp.Navigate(o.BaseURL+"/d2l/home")); err != nil {
		if ctx.Err() != nil || tctx.Err() != nil {
			return nil, stopped(err)
		}
		return nil, fmt.Errorf("starting browser: %w", err)
	}

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-bctx.Done():
			return nil, stopped(bctx.Err())
		case <-tick.C:
		}
		loc, err := chromedp.Run(bctx, chromedp.Location())
		if err != nil {
			// Navigation in progress or the window was closed.
			if bctx.Err() != nil {
				return nil, stopped(err)
			}
			continue
		}
		if !strings.HasPrefix(loc, o.BaseURL+"/d2l/") || strings.HasPrefix(loc, o.BaseURL+"/d2l/login") {
			continue
		}
		cookies, err := chromedp.Run(bctx, getCookies(o.BaseURL))
		if err != nil {
			continue
		}
		s := &Session{BaseURL: o.BaseURL, SavedAt: time.Now()}
		for _, c := range cookies {
			s.Cookies = append(s.Cookies, Cookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path})
		}
		if s.hasSessionCookie() {
			return s, nil
		}
	}
}

func getCookies(baseURL string) chromedp.Action[[]*network.Cookie] {
	return func(ctx context.Context, t *chromedp.Target) ([]*network.Cookie, error) {
		r, err := cdp.Call(ctx, t, network.GetCookies, network.GetCookiesParams{URLs: []string{baseURL + "/"}})
		return r.Cookies, err
	}
}
