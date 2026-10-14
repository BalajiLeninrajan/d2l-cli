// Package files maps course content to local paths, downloads it, and opens it.
package files

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BalajiLeninrajan/d2l-cli/internal/config"
	"github.com/BalajiLeninrajan/d2l-cli/internal/d2l"
)

// Sanitize makes s safe as a single path component.
func Sanitize(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == 0:
			return '-'
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case r < 0x20:
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, " .")
	if r := []rune(s); len(r) > 120 {
		s = strings.Trim(string(r[:120]), " .")
	}
	if s == "" {
		s = "untitled"
	}
	return s
}

// CourseDir is the folder of a course's files under root.
func CourseDir(root string, c d2l.Course) string {
	return filepath.Join(root, Sanitize(c.Name()))
}

// FileName picks the local name for a File topic: the name from its URL,
// falling back to the title.
func FileName(t d2l.Topic) string {
	if u, err := url.Parse(t.URL); err == nil {
		if name := path.Base(u.Path); name != "." && name != "/" && name != "" {
			return Sanitize(name)
		}
	}
	return Sanitize(t.Title)
}

// Names picks the local name of each File topic directly in m, keyed by
// TopicID. When names clash, ignoring case, the topic with the lowest ID
// keeps the name and the others get " (TopicID)" before the extension. IDs
// only grow, so adding or reordering topics never renames an existing file.
func Names(m *d2l.Module) map[int]string {
	var ids []int
	names := map[int]string{}
	for _, t := range m.Topics {
		if t.IsFile() {
			ids = append(ids, t.TopicID)
			names[t.TopicID] = FileName(t)
		}
	}
	return unique(ids, names, func(name string, id int) string {
		ext := filepath.Ext(name)
		return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), id, ext)
	})
}

// DirNames picks the local folder name of each submodule of m, keyed by
// ModuleID, with the same rule as Names.
func DirNames(m *d2l.Module) map[int]string {
	var ids []int
	names := map[int]string{}
	for _, sub := range m.Modules {
		ids = append(ids, sub.ModuleID)
		names[sub.ModuleID] = Sanitize(sub.Title)
	}
	return unique(ids, names, func(name string, id int) string {
		return fmt.Sprintf("%s (%d)", name, id)
	})
}

func unique(ids []int, names map[int]string, suffix func(string, int) string) map[int]string {
	slices.Sort(ids)
	taken := map[string]bool{}
	for _, id := range ids {
		name := names[id]
		if taken[strings.ToLower(name)] {
			name = suffix(name, id)
			names[id] = name
		}
		taken[strings.ToLower(name)] = true
	}
	return names
}

// ModuleDir is the folder of the last module in levels. levels[0] is the
// TOC root and each later module is a child of the one before it.
func ModuleDir(root string, c d2l.Course, levels []*d2l.Module) string {
	dir := CourseDir(root, c)
	for i := 1; i < len(levels); i++ {
		dir = filepath.Join(dir, DirNames(levels[i-1])[levels[i].ModuleID])
	}
	return dir
}

// Entry is a topic with the module titles leading to it and, for a File
// topic, where it is stored.
type Entry struct {
	Path  []string // module titles, outermost first
	Topic d2l.Topic
	Dest  string // empty unless Topic.IsFile()
}

// Entries returns every topic under m, depth first, in display order. dir
// is m's folder and prefix its module titles.
func Entries(m *d2l.Module, dir string, prefix []string) []Entry {
	var out []Entry
	names, dirs := Names(m), DirNames(m)
	for _, n := range m.Children() {
		if n.Module != nil {
			p := append(slices.Clone(prefix), n.Module.Title)
			out = append(out, Entries(n.Module, filepath.Join(dir, dirs[n.Module.ModuleID]), p)...)
			continue
		}
		e := Entry{Path: slices.Clone(prefix), Topic: *n.Topic}
		if n.Topic.IsFile() {
			e.Dest = filepath.Join(dir, names[n.Topic.TopicID])
		}
		out = append(out, e)
	}
	return out
}

// Fresh reports whether dest exists and is not older than the topic.
// Download stamps files with the topic's LastModifiedDate, so an equal
// mtime means unchanged.
func Fresh(dest string, t d2l.Topic) bool {
	st, err := os.Stat(dest)
	if err != nil || st.IsDir() {
		return false
	}
	if t.LastModifiedDate == nil {
		return true
	}
	return !st.ModTime().Before(t.LastModifiedDate.Truncate(time.Second))
}

// Download fetches a File topic to dest unless it is already Fresh.
func Download(ctx context.Context, c *d2l.Client, orgUnit int, t d2l.Topic, dest string) (skipped bool, err error) {
	if Fresh(dest, t) {
		return true, nil
	}
	resp, err := c.TopicFile(ctx, orgUnit, t.TopicID)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if err := checkBody(c, resp, dest); err != nil {
		return false, err
	}
	return false, writeAtomic(dest, resp.Body, t.LastModifiedDate)
}

// checkBody rejects an HTML page sent in place of a file that is not HTML,
// so a sign-in or error page is never saved and stamped as current.
func checkBody(c *d2l.Client, resp *http.Response, dest string) error {
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		return nil
	}
	switch strings.ToLower(filepath.Ext(dest)) {
	case ".htm", ".html", ".xhtml":
		return nil
	}
	if c.OffSite(resp) {
		return d2l.ErrUnauthorized
	}
	return fmt.Errorf("Learn sent a web page instead of %s", filepath.Base(dest))
}

// DownloadAttachment saves an announcement attachment into dir and returns its path.
func DownloadAttachment(ctx context.Context, c *d2l.Client, orgUnit, newsID int, a d2l.Attachment, dir string) (string, error) {
	dest := filepath.Join(dir, Sanitize(a.FileName))
	if st, err := os.Stat(dest); err == nil && st.Size() == a.Size {
		return dest, nil
	}
	resp, err := c.NewsAttachment(ctx, orgUnit, newsID, a.FileID)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := checkBody(c, resp, dest); err != nil {
		return "", err
	}
	return dest, writeAtomic(dest, resp.Body, nil)
}

func writeAtomic(dest string, r io.Reader, mtime *time.Time) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".d2l-*.part")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	// CreateTemp makes the file 0600; downloads should read like any other file.
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if mtime != nil {
		if err := os.Chtimes(tmp.Name(), *mtime, *mtime); err != nil {
			return err
		}
	}
	return os.Rename(tmp.Name(), dest)
}

// Opener is a command that opens a file or URL.
type Opener struct {
	Cmd *exec.Cmd
	// Terminal means the command needs the TTY; the TUI must suspend for it.
	Terminal bool
}

// OpenerFor picks the command for target (a local path or a URL) from the
// config's extension maps, falling back to the system opener.
func OpenerFor(cfg *config.Config, target string) Opener {
	isURL := strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")
	if !isURL {
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(target), "."))
		if cmd, ok := cfg.TerminalOpeners[ext]; ok {
			return Opener{Cmd: shellCmd(cmd, target), Terminal: true}
		}
		if cmd, ok := cfg.Openers[ext]; ok {
			return Opener{Cmd: shellCmd(cmd, target)}
		}
	}
	return Opener{Cmd: systemOpener(target)}
}

func shellCmd(command, target string) *exec.Cmd { return ShellCommand(command, target) }

// openerGrace is how long StartDetached watches an opener for an early
// failure, such as a command that is not installed.
const openerGrace = time.Second

// StartDetached runs a non-terminal opener in its own session so it outlives
// the TUI and doesn't scribble on the screen. It waits up to openerGrace and
// reports a non-zero exit in that time with the first line of stderr.
func StartDetached(cmd *exec.Cmd) error {
	// stderr goes to an unlinked temp file rather than a pipe, so a viewer
	// that logs after d2l exits does not die of SIGPIPE.
	errf, err := os.CreateTemp("", "d2l-open-*")
	if err == nil {
		os.Remove(errf.Name())
		defer errf.Close()
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if errf != nil {
		cmd.Stderr = errf
	}
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", cmd.Path, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			return nil
		}
		if errf != nil {
			if line := firstLine(errf); line != "" {
				return fmt.Errorf("%w: %s", err, line)
			}
		}
		return err
	case <-time.After(openerGrace):
		return nil
	}
}

func firstLine(f *os.File) string {
	b := make([]byte, 512)
	n, _ := f.ReadAt(b, 0)
	line, _, _ := strings.Cut(string(b[:n]), "\n")
	return strings.TrimSpace(line)
}
