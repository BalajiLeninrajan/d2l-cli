package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Both protocols announce their endpoint on stderr:
//
//	DevTools listening on ws://127.0.0.1:41235/devtools/browser/<id>
//	WebDriver BiDi listening on ws://127.0.0.1:41235
var listeningRE = regexp.MustCompile(`(DevTools|WebDriver BiDi) listening on (ws://\S+)`)

// browserProc is a running browser with an open automation websocket.
type browserProc struct {
	b      *Browser
	cmd    *exec.Cmd
	ws     *websocket.Conn
	exited chan struct{}

	mu     sync.Mutex // one request at a time on ws
	nextID atomic.Int64
}

// launch starts b with its own profile, opened on url, and connects to its
// automation endpoint.
func launch(ctx context.Context, b *Browser, profile string, headless bool, url string) (*browserProc, error) {
	args := append([]string{}, b.Argv[1:]...)
	switch b.Family {
	case FamilyFirefox:
		args = append(args, "--remote-debugging-port=0", "--no-remote", "--profile", profile)
		if headless {
			args = append(args, "--headless")
		}
	default:
		args = append(args,
			"--remote-debugging-port=0",
			"--user-data-dir="+profile,
			"--no-first-run",
			"--no-default-browser-check",
			"--password-store=basic", // no keyring prompt for d2l's own profile
		)
		if headless {
			args = append(args, "--headless=new")
		}
	}
	args = append(args, url)

	cmd := exec.Command(b.Argv[0], args...)
	cmd.Stdout = io.Discard
	setProcessGroup(cmd)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", b.Name, err)
	}
	p := &browserProc{b: b, cmd: cmd, exited: make(chan struct{})}

	endpoint := make(chan string, 1)
	var tail lastLines
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			tail.add(sc.Text())
			if m := listeningRE.FindStringSubmatch(sc.Text()); m != nil {
				select {
				case endpoint <- m[2]:
				default:
				}
			}
		}
		io.Copy(io.Discard, stderr)
	}()
	go func() {
		cmd.Wait()
		close(p.exited)
	}()

	var wsURL string
	select {
	case wsURL = <-endpoint:
	case <-p.exited:
		return nil, fmt.Errorf("%s exited before it opened a debugging port. Is another d2l login running? Its output:\n%s", b.Name, tail.String())
	case <-ctx.Done():
		p.kill()
		return nil, ctx.Err()
	case <-time.After(30 * time.Second):
		p.kill()
		return nil, fmt.Errorf("%s did not open a debugging port within 30s. Its output:\n%s", b.Name, tail.String())
	}
	if b.Family == FamilyFirefox {
		wsURL = strings.TrimRight(wsURL, "/") + "/session"
	}
	ws, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		p.kill()
		return nil, fmt.Errorf("connecting to %s: %w", b.Name, err)
	}
	ws.SetReadLimit(64 << 20)
	p.ws = ws
	if b.Family == FamilyFirefox {
		if err := p.call(ctx, "session.new", map[string]any{"capabilities": map[string]any{}}, nil); err != nil {
			p.close()
			return nil, fmt.Errorf("starting a WebDriver BiDi session: %w", err)
		}
	}
	return p, nil
}

// call sends one request and waits for its response, skipping events.
// CDP and WebDriver BiDi share this shape; they differ in how errors look.
func (p *browserProc) call(ctx context.Context, method string, params, result any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if params == nil {
		params = map[string]any{}
	}
	id := p.nextID.Add(1)
	req, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	if err := p.ws.Write(ctx, websocket.MessageText, req); err != nil {
		return err
	}
	for {
		_, data, err := p.ws.Read(ctx)
		if err != nil {
			return err
		}
		var resp struct {
			ID      *int64          `json:"id"`
			Result  json.RawMessage `json:"result"`
			Error   json.RawMessage `json:"error"`
			Message string          `json:"message"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
		if resp.ID == nil || *resp.ID != id {
			continue // an event, or a late reply to an abandoned call
		}
		if len(resp.Error) > 0 && string(resp.Error) != "null" {
			return fmt.Errorf("%s: %s %s", method, strings.Trim(string(resp.Error), `"`), resp.Message)
		}
		if result == nil {
			return nil
		}
		return json.Unmarshal(resp.Result, result)
	}
}

// cookies returns every cookie in the browser's profile.
func (p *browserProc) cookies(ctx context.Context) ([]Cookie, error) {
	if p.b.Family == FamilyFirefox {
		var r struct {
			Cookies []struct {
				Name  string `json:"name"`
				Value struct {
					Type  string `json:"type"`
					Value string `json:"value"`
				} `json:"value"`
				Domain string `json:"domain"`
				Path   string `json:"path"`
			} `json:"cookies"`
		}
		if err := p.call(ctx, "storage.getCookies", nil, &r); err != nil {
			return nil, err
		}
		out := make([]Cookie, 0, len(r.Cookies))
		for _, c := range r.Cookies {
			if c.Value.Type == "string" {
				out = append(out, Cookie{Name: c.Name, Value: c.Value.Value, Domain: c.Domain, Path: c.Path})
			}
		}
		return out, nil
	}
	var r struct {
		Cookies []Cookie `json:"cookies"`
	}
	if err := p.call(ctx, "Storage.getCookies", nil, &r); err != nil {
		return nil, err
	}
	return r.Cookies, nil
}

// close asks the browser to quit, and kills it if it doesn't within a few
// seconds.
func (p *browserProc) close() {
	if p.ws != nil {
		method := "Browser.close"
		if p.b.Family == FamilyFirefox {
			method = "browser.close"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		p.call(ctx, method, nil, nil)
		cancel()
		p.ws.CloseNow()
	}
	select {
	case <-p.exited:
	case <-time.After(3 * time.Second):
		p.kill()
	}
}

func (p *browserProc) kill() {
	killProcessGroup(p.cmd)
	select {
	case <-p.exited:
	case <-time.After(2 * time.Second):
	}
}

// lastLines keeps the end of the browser's output for error messages.
type lastLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *lastLines) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, s)
	if len(l.lines) > 15 {
		l.lines = l.lines[1:]
	}
}

func (l *lastLines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// firefoxPrefs keep a fresh profile from opening welcome and onboarding tabs.
const firefoxPrefs = `user_pref("browser.shell.checkDefaultBrowser", false);
user_pref("browser.startup.homepage_override.mstone", "ignore");
user_pref("browser.aboutwelcome.enabled", false);
user_pref("startup.homepage_welcome_url", "");
user_pref("startup.homepage_welcome_url.additional", "");
user_pref("datareporting.policy.dataSubmissionPolicyBypassNotification", true);
user_pref("toolkit.telemetry.reportingpolicy.firstRun", false);
user_pref("browser.sessionstore.resume_from_crash", false);
user_pref("zen.welcome-screen.seen", true);
`

func prepareProfile(b *Browser, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if b.Family != FamilyFirefox {
		return nil
	}
	userJS := filepath.Join(dir, "user.js")
	if _, err := os.Stat(userJS); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(userJS, []byte(firefoxPrefs), 0o600)
}
