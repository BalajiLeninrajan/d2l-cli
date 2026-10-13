// Package d2l is a minimal Brightspace Valence API client authenticated with
// browser session cookies.
package d2l

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// API versions used when the server does not say which it supports. They
// are the oldest fully supported ones as of LMS 20.26.1; Brightspace drops
// versions about two years after release, so pinning one would break.
const (
	defaultLP = "1.49"
	defaultLE = "1.82"
)

const (
	headerTimeout = 30 * time.Second
	idleTimeout   = 60 * time.Second
)

// ErrUnauthorized means the session is missing or expired.
var ErrUnauthorized = errors.New("session expired or invalid; run `d2l login`")

// ErrStalled means a response stopped sending data.
var ErrStalled = errors.New("connection stalled")

// APIError is a non-2xx response.
type APIError struct {
	Status int
	Path   string
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: HTTP %d %s", e.Path, e.Status, strings.TrimSpace(e.Body))
}

type Client struct {
	base string
	host string
	http *http.Client
	// idle is how long a response body may go without data before the
	// request is cancelled.
	idle time.Duration

	verMu  sync.Mutex
	lp, le string
}

// New builds a client that sends cookies for baseURL and refuses to follow
// redirects to the login page.
func New(baseURL string, cookies []*http.Cookie) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(u, cookies)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = headerTimeout
	hc := &http.Client{
		Jar:       jar,
		Transport: tr,
		// No overall timeout: downloads can be large. Stalls are caught by
		// the header timeout and the idle body timeout instead.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// An expired session bounces to /d2l/login before any SSO host.
			if req.URL.Host == u.Host && strings.HasPrefix(req.URL.Path, "/d2l/login") {
				return ErrUnauthorized
			}
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return nil
		},
	}
	return &Client{base: strings.TrimRight(baseURL, "/"), host: u.Host, http: hc, idle: idleTimeout}, nil
}

func (c *Client) BaseURL() string { return c.base }

// do sends a GET. The returned body cancels the request when no data
// arrives for c.idle, and must be closed.
func (c *Client) do(ctx context.Context, path string) (*http.Response, error) {
	ctx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		if errors.Is(err, ErrUnauthorized) {
			return nil, ErrUnauthorized
		}
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		cancel()
		return nil, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		cancel()
		if resp.StatusCode == http.StatusForbidden && sessionRejected(path, string(b)) {
			return nil, ErrUnauthorized
		}
		return nil, &APIError{Status: resp.StatusCode, Path: path, Body: string(b)}
	}
	resp.Body = newIdleBody(resp.Body, c.idle, cancel)
	return resp, nil
}

// OffSite reports whether resp came from a host other than Learn, which
// for an HTML page means a redirect to the sign-in page.
func (c *Client) OffSite(resp *http.Response) bool {
	return resp.Request != nil && resp.Request.URL.Host != c.host
}

// idleBody cancels its request when Read sees no data for idle.
type idleBody struct {
	io.ReadCloser
	idle    time.Duration
	timer   *time.Timer
	cancel  context.CancelFunc
	mu      sync.Mutex
	stalled bool
}

func newIdleBody(rc io.ReadCloser, idle time.Duration, cancel context.CancelFunc) *idleBody {
	b := &idleBody{ReadCloser: rc, idle: idle, cancel: cancel}
	b.timer = time.AfterFunc(idle, func() {
		b.mu.Lock()
		b.stalled = true
		b.mu.Unlock()
		cancel()
	})
	return b
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.timer.Reset(b.idle)
	}
	if err != nil && err != io.EOF {
		b.mu.Lock()
		stalled := b.stalled
		b.mu.Unlock()
		if stalled {
			return n, ErrStalled
		}
	}
	return n, err
}

func (b *idleBody) Close() error {
	b.timer.Stop()
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

type productVersions struct {
	ProductCode       string   `json:"ProductCode"`
	LatestVersion     string   `json:"LatestVersion"`
	SupportedVersions []string `json:"SupportedVersions"`
}

// versions returns the lp and le API versions to use: the newest the server
// supports, asked once per client, or the defaults when it does not answer.
func (c *Client) versions(ctx context.Context) (lp, le string) {
	c.verMu.Lock()
	defer c.verMu.Unlock()
	if c.lp != "" {
		return c.lp, c.le
	}
	lp, le = defaultLP, defaultLE
	var pvs []productVersions
	err := c.getJSON(ctx, "/d2l/api/versions/", &pvs)
	if err != nil && ctx.Err() != nil {
		// Cancelled, not an answer; ask again next time.
		return lp, le
	}
	for _, pv := range pvs {
		v := newest(pv)
		switch {
		case v == "":
		case strings.EqualFold(pv.ProductCode, "lp"):
			lp = v
		case strings.EqualFold(pv.ProductCode, "le"):
			le = v
		}
	}
	c.lp, c.le = lp, le
	return lp, le
}

// newest is the highest version in pv, comparing "1.82" style numbers
// numerically so that 1.100 sorts after 1.99.
func newest(pv productVersions) string {
	best := ""
	for _, v := range append([]string{pv.LatestVersion}, pv.SupportedVersions...) {
		if parseVersion(v) != nil && (best == "" || versionLess(best, v)) {
			best = v
		}
	}
	return best
}

func parseVersion(v string) []int {
	parts := strings.Split(v, ".")
	if len(parts) != 2 {
		return nil
	}
	out := make([]int, 2)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		out[i] = n
	}
	return out
}

func versionLess(a, b string) bool {
	x, y := parseVersion(a), parseVersion(b)
	if x[0] != y[0] {
		return x[0] < y[0]
	}
	return x[1] < y[1]
}

func (c *Client) lpPath(ctx context.Context, rest string) string {
	lp, _ := c.versions(ctx)
	return "/d2l/api/lp/" + lp + rest
}

func (c *Client) lePath(ctx context.Context, orgUnit int, format string, args ...any) string {
	_, le := c.versions(ctx)
	return fmt.Sprintf("/d2l/api/le/%s/%d", le, orgUnit) + fmt.Sprintf(format, args...)
}

// sessionRejected reports whether a 403 means the session is invalid rather
// than a missing permission. Brightspace answers a dead session with 403
// "Authentication required", and with a bare 403 on calls that every
// logged-in user may make.
func sessionRejected(path, body string) bool {
	return strings.Contains(body, "Authentication required") ||
		strings.Contains(path, "/users/whoami") ||
		strings.Contains(path, "/enrollments/myenrollments/")
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	resp, err := c.do(ctx, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
		// An HTML page instead of JSON is the login page served with 200.
		return ErrUnauthorized
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s: decoding response: %w", path, err)
	}
	return nil
}

func (c *Client) WhoAmI(ctx context.Context) (*WhoAmI, error) {
	var w WhoAmI
	return &w, c.getJSON(ctx, c.lpPath(ctx, "/users/whoami"), &w)
}

// Courses returns every course offering enrollment (org unit type 3).
func (c *Client) Courses(ctx context.Context) ([]Course, error) {
	var all []Course
	bookmark := ""
	for {
		q := url.Values{"orgUnitTypeId": {"3"}}
		if bookmark != "" {
			q.Set("bookmark", bookmark)
		}
		var page paged[Course]
		if err := c.getJSON(ctx, c.lpPath(ctx, "/enrollments/myenrollments/?"+q.Encode()), &page); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if !page.PagingInfo.HasMoreItems || page.PagingInfo.Bookmark == "" {
			return all, nil
		}
		bookmark = page.PagingInfo.Bookmark
	}
}

func (c *Client) TOC(ctx context.Context, orgUnit int) (*TOC, error) {
	var t TOC
	return &t, c.getJSON(ctx, c.lePath(ctx, orgUnit, "/content/toc"), &t)
}

func (c *Client) News(ctx context.Context, orgUnit int) ([]NewsItem, error) {
	var n []NewsItem
	return n, c.getJSON(ctx, c.lePath(ctx, orgUnit, "/news/"), &n)
}

func (c *Client) DropboxFolders(ctx context.Context, orgUnit int) ([]DropboxFolder, error) {
	var f []DropboxFolder
	return f, c.getJSON(ctx, c.lePath(ctx, orgUnit, "/dropbox/folders/"), &f)
}

func (c *Client) MySubmissions(ctx context.Context, orgUnit, folderID int) ([]MySubmission, error) {
	var s []MySubmission
	return s, c.getJSON(ctx, c.lePath(ctx, orgUnit, "/dropbox/folders/%d/submissions/mysubmissions/", folderID), &s)
}

func (c *Client) Grades(ctx context.Context, orgUnit int) ([]GradeValue, error) {
	var g []GradeValue
	return g, c.getJSON(ctx, c.lePath(ctx, orgUnit, "/grades/values/myGradeValues/"), &g)
}

// FinalGrade returns the user's final course grade, or nil when the course
// has none or it is not released yet.
func (c *Client) FinalGrade(ctx context.Context, orgUnit int) (*GradeValue, error) {
	var g GradeValue
	err := c.getJSON(ctx, c.lePath(ctx, orgUnit, "/grades/final/values/myGradeValue"), &g)
	var apiErr *APIError
	if errors.As(err, &apiErr) && (apiErr.Status == http.StatusNotFound || apiErr.Status == http.StatusForbidden) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// TopicFile opens the file behind a File topic. The caller closes the body.
func (c *Client) TopicFile(ctx context.Context, orgUnit, topicID int) (*http.Response, error) {
	return c.do(ctx, c.lePath(ctx, orgUnit, "/content/topics/%d/file?stream=true", topicID))
}

// NewsAttachment opens an announcement attachment. The caller closes the body.
func (c *Client) NewsAttachment(ctx context.Context, orgUnit, newsID, fileID int) (*http.Response, error) {
	return c.do(ctx, c.lePath(ctx, orgUnit, "/news/%d/attachments/%d", newsID, fileID))
}

// TopicWebURL is the Learn page for a topic, for opening in a browser.
func (c *Client) TopicWebURL(orgUnit, topicID int) string {
	return fmt.Sprintf("%s/d2l/le/content/%d/viewContent/%d/View", c.base, orgUnit, topicID)
}

// ModuleWebURL is the content page for a module.
func (c *Client) ModuleWebURL(orgUnit, moduleID int) string {
	return fmt.Sprintf("%s/d2l/le/content/%d/Home?itemIdentifier=D2L.LE.Content.ContentObject.ModuleCO-%d", c.base, orgUnit, moduleID)
}

// CourseWebURL is the course home page.
func (c *Client) CourseWebURL(orgUnit int) string {
	return fmt.Sprintf("%s/d2l/home/%d", c.base, orgUnit)
}

// DropboxWebURL is the submission page for an assignment folder.
func (c *Client) DropboxWebURL(orgUnit, folderID int) string {
	return fmt.Sprintf("%s/d2l/lms/dropbox/user/folder_submit_files.d2l?db=%d&ou=%d", c.base, folderID, orgUnit)
}

// AbsURL resolves a possibly relative topic URL against the instance.
func (c *Client) AbsURL(u string) string {
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	return c.base + "/" + strings.TrimLeft(u, "/")
}
