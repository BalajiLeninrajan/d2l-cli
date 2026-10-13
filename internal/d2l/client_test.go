package d2l

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, []*http.Cookie{{Name: "d2lSessionVal", Value: "x", Path: "/"}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	fmt.Fprint(w, body)
}

func TestCoursesFollowsBookmark(t *testing.T) {
	var calls int
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/d2l/api/lp/"+defaultLP+"/enrollments/myenrollments/" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("orgUnitTypeId"); got != "3" {
			t.Errorf("orgUnitTypeId = %q, want 3", got)
		}
		if ck, err := r.Cookie("d2lSessionVal"); err != nil || ck.Value != "x" {
			t.Errorf("session cookie not sent: %v", err)
		}
		calls++
		switch r.URL.Query().Get("bookmark") {
		case "":
			writeJSON(w, `{"PagingInfo":{"Bookmark":"101","HasMoreItems":true},"Items":[{"OrgUnit":{"Id":101,"Name":"A","Code":"A100"}}]}`)
		case "101":
			writeJSON(w, `{"PagingInfo":{"Bookmark":"102","HasMoreItems":false},"Items":[{"OrgUnit":{"Id":102,"Name":"B","Code":"B200"}}]}`)
		default:
			t.Errorf("unexpected bookmark %q", r.URL.Query().Get("bookmark"))
		}
	}))
	cs, err := c.Courses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if len(cs) != 2 || cs[0].ID() != 101 || cs[1].ID() != 102 || cs[1].Code() != "B200" {
		t.Errorf("courses = %+v", cs)
	}
}

const tocJSON = `{"Modules":[
 {"ModuleId":2,"Title":"Second","SortOrder":2,"Modules":[],"Topics":[]},
 {"ModuleId":1,"Title":"First","SortOrder":1,
  "Modules":[{"ModuleId":10,"Title":"Sub","SortOrder":2,"Modules":[],
    "Topics":[{"TopicId":100,"Title":"Deep","TypeIdentifier":"File","Url":"/content/deep.pdf","SortOrder":1}]}],
  "Topics":[
   {"TopicId":12,"Title":"T3","TypeIdentifier":"Link","Url":"https://example.com","SortOrder":3,"ActivityType":2},
   {"TopicId":11,"Title":"T1","TypeIdentifier":"File","Url":"/content/t1.pdf","SortOrder":1,"ActivityType":1,"LastModifiedDate":"2025-01-02T03:04:05.000Z"}
  ]}
]}`

func TestTOCChildrenOrder(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/d2l/api/le/"+defaultLE+"/555/content/toc" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, tocJSON)
	}))
	toc, err := c.TOC(context.Background(), 555)
	if err != nil {
		t.Fatal(err)
	}
	top := toc.Root().Children()
	if got := titles(top); fmt.Sprint(got) != "[First Second]" {
		t.Fatalf("root children = %v", got)
	}
	first := top[0].Module
	kids := first.Children()
	if got := titles(kids); fmt.Sprint(got) != "[T1 Sub T3]" {
		t.Errorf("First children = %v, want modules and topics interleaved by SortOrder", got)
	}
	if kids[0].Topic == nil || kids[1].Module == nil || kids[2].Topic == nil {
		t.Errorf("node kinds wrong: %+v", kids)
	}
	if lm := kids[0].Topic.LastModifiedDate; lm == nil || lm.Year() != 2025 {
		t.Errorf("LastModifiedDate = %v", lm)
	}
	if !kids[0].Topic.IsFile() || kids[2].Topic.IsFile() {
		t.Error("IsFile wrong")
	}

	flat := toc.Root().Walk(nil)
	var got []string
	for _, ft := range flat {
		got = append(got, fmt.Sprint(ft.Path, ft.Topic.Title))
	}
	if want := "[[First]T1 [First Sub]Deep [First]T3]"; fmt.Sprint(got) != want {
		t.Errorf("Walk = %v, want %v", got, want)
	}
}

func titles(ns []Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Title()
	}
	return out
}

func TestUnauthorized(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"redirect to login", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/d2l/login" {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, "<html>login</html>")
				return
			}
			http.Redirect(w, r, "/d2l/login?sessionExpired=1", http.StatusFound)
		}},
		{"html with 200", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, "<html>please log in</html>")
		}},
		{"401", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusUnauthorized)
		}},
		{"403 authentication required", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Authentication required", http.StatusForbidden)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, tt.handler)
			_, err := c.News(context.Background(), 1)
			if !errors.Is(err, ErrUnauthorized) {
				t.Errorf("err = %v, want ErrUnauthorized", err)
			}
		})
	}
}

func TestForbiddenOnWhoAmIIsUnauthorized(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Forbidden", http.StatusForbidden)
	}))
	if _, err := c.WhoAmI(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("whoami err = %v, want ErrUnauthorized", err)
	}
	// A plain 403 elsewhere is a missing permission, not a dead session.
	_, err := c.Grades(context.Background(), 1)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		t.Errorf("grades err = %v, want *APIError 403", err)
	}
}

func TestNotFoundIsAPIError(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no such org unit", http.StatusNotFound)
	}))
	_, err := c.TOC(context.Background(), 42)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *APIError", err, err)
	}
	if apiErr.Status != 404 || apiErr.Path != "/d2l/api/le/"+defaultLE+"/42/content/toc" {
		t.Errorf("APIError = %+v", apiErr)
	}
	if errors.Is(err, ErrUnauthorized) {
		t.Error("404 must not be ErrUnauthorized")
	}
}

func TestAbsURL(t *testing.T) {
	c, _ := New("https://learn.example.edu", nil)
	if got := c.AbsURL("/content/a.pdf"); got != "https://learn.example.edu/content/a.pdf" {
		t.Errorf("relative: %s", got)
	}
	if got := c.AbsURL("https://other.example/x"); got != "https://other.example/x" {
		t.Errorf("absolute: %s", got)
	}
}

func TestVersionsPicksNewestSupported(t *testing.T) {
	var versionCalls atomic.Int32
	var paths []string
	var mu sync.Mutex
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/d2l/api/versions/" {
			versionCalls.Add(1)
			writeJSON(w, `[{"ProductCode":"lp","LatestVersion":"1.64","SupportedVersions":["1.49","1.64"]},
				{"ProductCode":"le","LatestVersion":"1.99","SupportedVersions":["1.82","1.99","1.100"]}]`)
			return
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		writeJSON(w, `[]`)
	}))
	ctx := context.Background()
	if _, err := c.News(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Grades(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if versionCalls.Load() != 1 {
		t.Errorf("versions asked %d times, want 1", versionCalls.Load())
	}
	want := []string{"/d2l/api/le/1.100/5/news/", "/d2l/api/le/1.100/5/grades/values/myGradeValues/"}
	if fmt.Sprint(paths) != fmt.Sprint(want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
	if lp, _ := c.versions(ctx); lp != "1.64" {
		t.Errorf("lp = %s, want 1.64", lp)
	}
}

func TestFinalGradeMissingIsNil(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no final grade", http.StatusNotFound)
	}))
	g, err := c.FinalGrade(context.Background(), 1)
	if g != nil || err != nil {
		t.Errorf("FinalGrade = %v, %v; want nil, nil", g, err)
	}
}

func TestNewsAttachmentSize(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `[{"Id":1,"Title":"A","Attachments":[{"FileId":7,"FileName":"a.pdf","FileSize":1234}]}]`)
	}))
	news, err := c.News(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if a := news[0].Attachments[0]; a.FileID != 7 || a.Size != 1234 {
		t.Errorf("attachment = %+v", a)
	}
}

func TestStalledBodyFails(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/d2l/api/versions/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, "partial")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	c.idle = 100 * time.Millisecond
	resp, err := c.TopicFile(context.Background(), 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(resp.Body)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrStalled) {
			t.Errorf("err = %v, want ErrStalled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read did not stop on a stalled body")
	}
}
