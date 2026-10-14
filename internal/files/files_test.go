package files

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BalajiLeninrajan/d2l-cli/internal/config"
	"github.com/BalajiLeninrajan/d2l-cli/internal/d2l"
)

func TestSanitize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Week 1/Lectures", "Week 1-Lectures"},
		{`a\b`, "a-b"},
		{"..", "untitled"},
		{"../etc", "-etc"},
		{".hidden", "hidden"},
		{"", "untitled"},
		{"   ", "untitled"},
		{"tab\there\nnewline", "tab here newline"},
		{"ctrl\x01char", "ctrlchar"},
		{"trailing. ", "trailing"},
		{"  many   spaces  ", "many spaces"},
	}
	for _, tt := range tests {
		if got := Sanitize(tt.in); got != tt.want {
			t.Errorf("Sanitize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	long := strings.Repeat("é", 200)
	if got := Sanitize(long); len([]rune(got)) != 120 {
		t.Errorf("long name kept %d runes, want 120", len([]rune(got)))
	}
	// Truncation must not leave a trailing dot or space.
	dotted := strings.Repeat("a", 119) + ". tail"
	if got := Sanitize(dotted); strings.HasSuffix(got, ".") || strings.HasSuffix(got, " ") {
		t.Errorf("truncated name ends badly: %q", got)
	}
}

func TestFileName(t *testing.T) {
	tests := []struct {
		topic d2l.Topic
		want  string
	}{
		{d2l.Topic{Title: "Lecture 1", URL: "/content/enforced/12345-CS135/Week%201/lec%2001.pdf"}, "lec 01.pdf"},
		{d2l.Topic{Title: "Notes", URL: "/content/enforced/1-X/a.pdf?ou=1"}, "a.pdf"},
		{d2l.Topic{Title: "No URL"}, "No URL"},
		{d2l.Topic{Title: "Slash/Title", URL: "/"}, "Slash-Title"},
	}
	for _, tt := range tests {
		if got := FileName(tt.topic); got != tt.want {
			t.Errorf("FileName(%q) = %q, want %q", tt.topic.URL, got, tt.want)
		}
	}
}

func file(id int, url string) d2l.Topic {
	return d2l.Topic{TopicID: id, Title: fmt.Sprint("T", id), TypeIdentifier: "File", URL: url}
}

func TestEntriesPaths(t *testing.T) {
	var c d2l.Course
	c.OrgUnit.Name = "CS 135: Design"
	inner := d2l.Module{ModuleID: 2, Title: "a/b", Topics: []d2l.Topic{file(9, "/x/lec01.pdf")}}
	root := &d2l.Module{Modules: []d2l.Module{{ModuleID: 1, Title: "Week 1", Modules: []d2l.Module{inner}}}}

	es := Entries(root, CourseDir("/dl", c), nil)
	if len(es) != 1 {
		t.Fatalf("entries = %+v", es)
	}
	want := filepath.Join("/dl", "CS 135: Design", "Week 1", "a-b", "lec01.pdf")
	if es[0].Dest != want || fmt.Sprint(es[0].Path) != "[Week 1 a/b]" {
		t.Errorf("entry = %+v, want dest %q", es[0], want)
	}
	levels := []*d2l.Module{root, &root.Modules[0], &root.Modules[0].Modules[0]}
	if got := ModuleDir("/dl", c, levels); got != filepath.Dir(want) {
		t.Errorf("ModuleDir = %q, want %q", got, filepath.Dir(want))
	}
}

func TestNamesResolveClashes(t *testing.T) {
	m := &d2l.Module{Topics: []d2l.Topic{
		file(30, "/tut2/handout.pdf"),
		file(10, "/tut1/handout.pdf"),
		file(20, "/x/Handout.PDF"),
		file(40, "/x/other.pdf"),
		{TopicID: 50, TypeIdentifier: "Link", URL: "/x/handout.pdf"},
	}}
	got := Names(m)
	want := map[int]string{10: "handout.pdf", 20: "Handout (20).PDF", 30: "handout (30).pdf", 40: "other.pdf"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("Names = %v, want %v", got, want)
	}

	dirs := DirNames(&d2l.Module{Modules: []d2l.Module{{ModuleID: 8, Title: "Week 1"}, {ModuleID: 3, Title: "week 1"}}})
	if dirs[3] != "week 1" || dirs[8] != "Week 1 (8)" {
		t.Errorf("DirNames = %v", dirs)
	}
}

func TestDownloadRejectsLoginPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<html>sign in</html>")
	}))
	defer srv.Close()
	client, _ := d2l.New(srv.URL, nil)
	dir := t.TempDir()
	dest := filepath.Join(dir, "lec.pdf")
	if _, err := Download(context.Background(), client, 1, file(2, "/lec.pdf"), dest); err == nil {
		t.Fatal("want error for an HTML page")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("HTML page was saved: %v", err)
	}
	// An HTML topic is still fine.
	page := filepath.Join(dir, "page.html")
	if _, err := Download(context.Background(), client, 1, file(3, "/page.html"), page); err != nil {
		t.Errorf("html topic: %v", err)
	}
}

func TestStartDetachedReportsEarlyFailure(t *testing.T) {
	err := StartDetached(exec.Command("sh", "-c", "echo 'no such viewer' >&2; exit 127"))
	if err == nil || !strings.Contains(err.Error(), "no such viewer") {
		t.Errorf("err = %v, want exit status with stderr", err)
	}
	if err := StartDetached(exec.Command("sh", "-c", "exit 0")); err != nil {
		t.Errorf("clean exit: %v", err)
	}
}

func TestDownload(t *testing.T) {
	var hits atomic.Int32
	body := "v1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/d2l/api/le/1.82/7/content/topics/9/file" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	client, err := d2l.New(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "Course", "Week 1", "lec.pdf")
	mod := time.Date(2025, 9, 8, 14, 30, 0, 0, time.UTC)
	topic := d2l.Topic{TopicID: 9, TypeIdentifier: "File", LastModifiedDate: &mod}
	ctx := context.Background()

	skipped, err := Download(ctx, client, 7, topic, dest)
	if err != nil || skipped {
		t.Fatalf("first download: skipped=%v err=%v", skipped, err)
	}
	assertFile(t, dest, "v1", mod)

	skipped, err = Download(ctx, client, 7, topic, dest)
	if err != nil || !skipped {
		t.Fatalf("second download: skipped=%v err=%v", skipped, err)
	}
	if hits.Load() != 1 {
		t.Errorf("server hit %d times, want 1", hits.Load())
	}

	body = "v2"
	newer := mod.Add(time.Hour)
	topic.LastModifiedDate = &newer
	skipped, err = Download(ctx, client, 7, topic, dest)
	if err != nil || skipped {
		t.Fatalf("newer download: skipped=%v err=%v", skipped, err)
	}
	assertFile(t, dest, "v2", newer)

	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".d2l-*"))
	if len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

func TestDownloadErrorLeavesNoFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()
	client, _ := d2l.New(srv.URL, nil)
	dest := filepath.Join(t.TempDir(), "x.pdf")
	if _, err := Download(context.Background(), client, 1, d2l.Topic{TopicID: 2}, dest); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("dest exists after failed download: %v", err)
	}
}

func assertFile(t *testing.T, path, body string, mtime time.Time) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != body {
		t.Errorf("%s = %q, want %q", path, b, body)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", st.Mode().Perm())
	}
	if !st.ModTime().Equal(mtime) {
		t.Errorf("mtime = %v, want %v", st.ModTime(), mtime)
	}
}

func TestOpenerFor(t *testing.T) {
	cfg := &config.Config{
		Openers:         map[string]string{"pdf": "zathura --fork"},
		TerminalOpeners: map[string]string{"md": "nvim"},
	}
	tests := []struct {
		target   string
		terminal bool
		argv0    string
		has      string
	}{
		{"/tmp/a.md", true, "sh", "nvim"},
		{"/tmp/A.PDF", false, "sh", "zathura --fork"},
		{"/tmp/b.docx", false, "xdg-open", ""},
		// URLs never use the extension maps.
		{"https://learn.example.edu/x.pdf", false, "xdg-open", ""},
	}
	for _, tt := range tests {
		o := OpenerFor(cfg, tt.target)
		if o.Terminal != tt.terminal {
			t.Errorf("%s: Terminal = %v, want %v", tt.target, o.Terminal, tt.terminal)
		}
		args := o.Cmd.Args
		if filepath.Base(args[0]) != tt.argv0 && !(tt.argv0 == "xdg-open" && args[0] == "open") {
			t.Errorf("%s: argv0 = %q, want %q", tt.target, args[0], tt.argv0)
		}
		if tt.has != "" && !strings.Contains(strings.Join(args, " "), tt.has) {
			t.Errorf("%s: args %q missing %q", tt.target, args, tt.has)
		}
		if args[len(args)-1] != tt.target {
			t.Errorf("%s: target is not the last arg: %q", tt.target, args)
		}
	}
}

func TestShellCmdPassesTargetSafely(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	target := filepath.Join(dir, "it's $(touch pwned) a file.pdf")
	cmd := shellCmd(`printf '%s' >`+out, target)
	// printf '%s' > out "$1" writes $1 into out.
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if string(b) != target {
		t.Errorf("got %q, want %q", b, target)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Error("target was evaluated by the shell")
	}
}
