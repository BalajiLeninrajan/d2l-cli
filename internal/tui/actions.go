package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/BalajiLeninrajan/d2l-cli/internal/d2l"
	"github.com/BalajiLeninrajan/d2l-cli/internal/files"
)

func openURL(u string) tea.Cmd { return func() tea.Msg { return openURLMsg{u} } }

// open runs the configured opener for a path or URL. Terminal openers take
// over the screen through tea.ExecProcess; the rest run detached.
func (r *root) open(target string) tea.Cmd {
	o := files.OpenerFor(r.app.cfg, target)
	name := target
	if !isURL(target) {
		name = filepath.Base(target)
	}
	if o.Terminal {
		return tea.ExecProcess(o.Cmd, func(err error) tea.Msg {
			if err != nil {
				return statusMsg{err: fmt.Errorf("opening %s: %w", name, err)}
			}
			return execDoneMsg{}
		})
	}
	start := func() tea.Msg {
		if err := files.StartDetached(o.Cmd); err != nil {
			return statusMsg{err: fmt.Errorf("opening %s: %w", name, err)}
		}
		return statusMsg{text: "Opened " + name}
	}
	return tea.Batch(info("Opening "+name+"..."), start)
}

func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

type topicKind int

const (
	kindFile topicKind = iota
	kindLink
	kindWeb
)

func kindOf(t d2l.Topic) topicKind {
	if t.IsFile() {
		return kindFile
	}
	switch t.ActivityType {
	case d2l.ActivityDropbox, d2l.ActivityQuiz, d2l.ActivityLTI:
		return kindWeb
	}
	if t.TypeIdentifier == "Link" && t.URL != "" {
		return kindLink
	}
	return kindWeb
}

func activityLabel(t d2l.Topic) string {
	switch t.ActivityType {
	case d2l.ActivityQuiz:
		return "quiz"
	case d2l.ActivityDropbox:
		return "assignment"
	case d2l.ActivityLTI:
		return "external tool"
	}
	return "page"
}

// unavailable says why Learn will not serve a topic now, or "" if it will.
func unavailable(t d2l.Topic, now time.Time) string {
	switch {
	case t.Available(now):
		return ""
	case t.StartDateTime != nil && now.Before(*t.StartDateTime):
		return "available " + fmtWhen(*t.StartDateTime)
	case t.EndDateTime != nil && now.After(*t.EndDateTime):
		return "closed " + fmtWhen(*t.EndDateTime)
	}
	return "locked"
}

// openTopic opens a topic the way enter does in the content list: files are
// downloaded first if stale, links open directly, the rest open on Learn.
func (a *app) openTopic(c d2l.Course, e files.Entry) tea.Cmd {
	t := e.Topic
	switch kindOf(t) {
	case kindFile:
		if u := unavailable(t, time.Now()); u != "" {
			return info(fmt.Sprintf("Can't open %s yet: %s.", t.Title, u))
		}
		return func() tea.Msg {
			if files.Fresh(e.Dest, t) {
				return openPathMsg{e.Dest}
			}
			return downloadMsg{items: []dlItem{{course: c, topic: t, dest: e.Dest}}, open: true}
		}
	case kindLink:
		return openURL(a.client.AbsURL(t.URL))
	default:
		return openURL(a.client.TopicWebURL(c.ID(), t.TopicID))
	}
}

func downloadTopic(c d2l.Course, e files.Entry) tea.Cmd {
	t := e.Topic
	if !t.IsFile() {
		return info(t.Title + " is not a file. Press w to open it on Learn.")
	}
	if u := unavailable(t, time.Now()); u != "" {
		return info(fmt.Sprintf("Can't download %s yet: %s.", t.Title, u))
	}
	return func() tea.Msg {
		return downloadMsg{items: []dlItem{{course: c, topic: t, dest: e.Dest}}}
	}
}

func (a *app) downloadCourse(c d2l.Course) tea.Cmd {
	load := func() tea.Msg {
		toc, err := a.toc(c.ID(), false)
		if err != nil {
			return statusMsg{err: fmt.Errorf("loading content for %s: %w", c.Code(), err)}
		}
		return newDownload(c, files.Entries(toc.Root(), files.CourseDir(a.cfg, c), nil))
	}
	return tea.Batch(info("Loading content for "+c.Code()+"..."), load)
}

// announcementDir is where one announcement's attachments go. Each gets its
// own folder, named with its ID, because attachments from different posts
// often share names like image.png.
func announcementDir(a *app, c d2l.Course, n d2l.NewsItem) string {
	title := []rune(files.Sanitize(n.Title))
	if len(title) > 80 {
		title = []rune(strings.TrimRight(string(title[:80]), " ."))
	}
	return filepath.Join(files.CourseDir(a.cfg, c), "Announcements", fmt.Sprintf("%s (%d)", string(title), n.ID))
}

func (a *app) saveAttachments(c d2l.Course, n d2l.NewsItem) tea.Cmd {
	if len(n.Attachments) == 0 {
		return info("This announcement has no attachments.")
	}
	dir := announcementDir(a, c, n)
	save := func() tea.Msg {
		if !a.begin() {
			return nil
		}
		defer a.work.Done()
		names := make([]string, 0, len(n.Attachments))
		for _, att := range n.Attachments {
			p, err := files.DownloadAttachment(a.ctx, a.client, c.ID(), n.ID, att, dir)
			if err != nil {
				return statusMsg{err: fmt.Errorf("saving %s: %w", att.FileName, err)}
			}
			names = append(names, filepath.Base(p))
		}
		return statusMsg{text: fmt.Sprintf("Saved %s → %s", strings.Join(names, ", "), a.short(dir))}
	}
	return tea.Batch(info(fmt.Sprintf("Saving %s...", plural(len(n.Attachments), "attachment", "attachments"))), save)
}

func (a *app) openAttachment(c d2l.Course, n d2l.NewsItem) tea.Cmd {
	if len(n.Attachments) == 0 {
		return info("This announcement has no attachments.")
	}
	att := n.Attachments[0]
	dir := announcementDir(a, c, n)
	return func() tea.Msg {
		if !a.begin() {
			return nil
		}
		defer a.work.Done()
		p, err := files.DownloadAttachment(a.ctx, a.client, c.ID(), n.ID, att, dir)
		if err != nil {
			return statusMsg{err: fmt.Errorf("saving %s: %w", att.FileName, err)}
		}
		return openPathMsg{p}
	}
}
