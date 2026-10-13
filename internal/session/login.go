package session

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"
)

// LoginOptions controls how Login drives the browser.
type LoginOptions struct {
	BaseURL string
	Browser string // command from the config; empty means the default browser
	// Interactive skips the silent headless attempt and opens a window right away.
	Interactive bool
	// Silent only tries the hidden browser and never opens a window.
	Silent bool
	// Validate checks captured cookies against the server. Login keeps
	// waiting until it accepts a session. Nil accepts the first one.
	Validate func(context.Context, *Session) error
	// Logf receives progress lines.
	Logf func(format string, args ...any)
}

const silentTimeout = 25 * time.Second

// Login launches a browser on Learn with a profile of its own and waits for
// a session the server accepts, reading cookies over the browser's automation
// protocol (CDP for Chromium-based browsers, WebDriver BiDi for Firefox-based
// ones). The user's own browser profile is never touched.
//
// The profile persists, so if the identity provider remembers the user
// ("stay signed in"), a hidden run can renew the session without a window.
func Login(ctx context.Context, o LoginOptions) (*Session, error) {
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	b, err := ResolveBrowser(o.Browser, logf)
	if err != nil {
		return nil, err
	}
	profile := b.ProfileDir()
	// A first login has no profile for the identity provider to remember,
	// so a hidden attempt could only time out.
	_, statErr := os.Stat(profile)
	hasProfile := statErr == nil
	if o.Silent {
		if !hasProfile {
			return nil, errors.New("no saved browser profile")
		}
		return runLogin(ctx, o, b, profile, true, silentTimeout)
	}
	if !o.Interactive && hasProfile {
		logf("Trying a hidden %s with the saved login profile...", b.Name)
		s, err := runLogin(ctx, o, b, profile, true, silentTimeout)
		if err == nil || ctx.Err() != nil {
			return s, err
		}
		logf("That didn't finish (%v); opening a window.", err)
	}
	if !hasDisplay() {
		return nil, errors.New("no display to open a browser window on. Log in on a machine with a desktop, or use d2l login --cookie -")
	}
	logf("Log in to Learn in the %s window. It closes on its own once you're in.", b.Name)
	return runLogin(ctx, o, b, profile, false, 5*time.Minute)
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

func runLogin(ctx context.Context, o LoginOptions, b *Browser, profile string, headless bool, timeout time.Duration) (*Session, error) {
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stopped := func(err error) error {
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case tctx.Err() != nil:
			return errors.New("timed out waiting for login")
		}
		return err
	}

	if err := prepareProfile(b, profile); err != nil {
		return nil, fmt.Errorf("creating the login profile: %w", err)
	}
	p, err := launch(tctx, b, profile, headless, o.BaseURL+"/d2l/home")
	if err != nil {
		return nil, stopped(err)
	}
	defer p.close()

	// Learn may set session cookies before login finishes, and may or may not
	// rotate them after, so a rejected pair is retried every few seconds
	// rather than given up on.
	tried := map[string]time.Time{}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tctx.Done():
			return nil, stopped(tctx.Err())
		case <-p.exited:
			return nil, errors.New("browser closed before login finished")
		case <-tick.C:
		}
		cookies, err := p.cookies(tctx)
		if err != nil {
			continue
		}
		s := sessionFrom(o.BaseURL, cookies)
		if !s.hasSessionCookie() {
			continue
		}
		fp := s.fingerprint()
		if last, ok := tried[fp]; ok && time.Since(last) < 5*time.Second {
			continue
		}
		tried[fp] = time.Now()
		if o.Validate == nil || o.Validate(tctx, s) == nil {
			return s, nil
		}
	}
}

// sessionFrom keeps the cookies the browser would send to baseURL.
func sessionFrom(baseURL string, cookies []Cookie) *Session {
	s := &Session{BaseURL: baseURL, SavedAt: time.Now()}
	u, err := url.Parse(baseURL)
	if err != nil {
		return s
	}
	host := u.Hostname()
	for _, c := range cookies {
		d := strings.TrimPrefix(c.Domain, ".")
		if host == d || strings.HasSuffix(host, "."+d) {
			if c.Path == "" {
				c.Path = "/"
			}
			s.Cookies = append(s.Cookies, c)
		}
	}
	return s
}

// fingerprint identifies a pair of session cookie values.
func (s *Session) fingerprint() string {
	var b strings.Builder
	for _, c := range s.Cookies {
		if c.Name == "d2lSessionVal" || c.Name == "d2lSecureSessionVal" {
			b.WriteString(c.Name + "=" + c.Value + ";")
		}
	}
	return b.String()
}
