package tui

import (
	"slices"
	"strings"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/BalajiLeninrajan/d2l-cli/internal/d2l"
)

type newsMsg struct {
	courseID int
	items    []d2l.NewsItem
	err      error
}

func (m newsMsg) failure() error { return m.err }

type newsItem struct{ n d2l.NewsItem }

func (i newsItem) Title() string       { return i.n.Title }
func (i newsItem) FilterValue() string { return i.n.Title }
func (i newsItem) Description() string {
	var parts []string
	if i.n.StartDate != nil {
		parts = append(parts, fmtWhen(*i.n.StartDate))
	}
	if k := len(i.n.Attachments); k > 0 {
		parts = append(parts, plural(k, "attachment", "attachments"))
	}
	return strings.Join(parts, " · ")
}

type newsTab struct {
	listTab
	app    *app
	course d2l.Course
	parent view
}

func newNewsTab(a *app, c d2l.Course, parent view) *newsTab {
	t := &newsTab{app: a, course: c, parent: parent}
	t.list = newList("Announcements", "announcement", "announcements", keyEnter, keyBack, keySearch, keyReload, keyTabN, keyNextTab, keyClose)
	return t
}

func (t *newsTab) name() string { return "Announcements" }

func (t *newsTab) Init() tea.Cmd {
	a, c := t.app, t.course
	fetch := func() tea.Msg {
		items, err := a.client.News(a.ctx, c.ID())
		return newsMsg{courseID: c.ID(), items: items, err: err}
	}
	return tea.Batch(t.startLoading(), fetch)
}

func (t *newsTab) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case newsMsg:
		if msg.courseID != t.course.ID() || !t.finish(msg.err) || msg.err != nil {
			return nil
		}
		shown := slices.DeleteFunc(msg.items, func(n d2l.NewsItem) bool { return n.IsHidden })
		slices.SortStableFunc(shown, func(a, b d2l.NewsItem) int {
			switch {
			case a.StartDate == nil && b.StartDate == nil:
				return 0
			case a.StartDate == nil:
				return 1
			case b.StartDate == nil:
				return -1
			}
			return b.StartDate.Compare(*a.StartDate)
		})
		items := make([]list.Item, len(shown))
		for i, n := range shown {
			items[i] = newsItem{n}
		}
		return setItems(&t.list, items)
	case tea.KeyMsg:
		if t.captures(msg) {
			break
		}
		switch {
		case key.Matches(msg, keyBack):
			return pop(t.parent)
		case key.Matches(msg, keyReload):
			return t.Init()
		case key.Matches(msg, keyEnter):
			if it, ok := t.list.SelectedItem().(newsItem); ok {
				return push(newNewsDetail(t.app, t.course, it.n))
			}
			return nil
		}
	}
	return t.update(msg)
}

func newNewsDetail(a *app, c d2l.Course, n d2l.NewsItem) *detailView {
	body := htmlToMarkdown(n.Body)
	if len(n.Attachments) > 0 {
		names := make([]string, len(n.Attachments))
		for i, att := range n.Attachments {
			names[i] = "- " + att.FileName
		}
		body += "\n\n---\n\n**Attachments**\n\n" + strings.Join(names, "\n")
	}
	sub := c.Code()
	if n.StartDate != nil {
		sub += " · " + fmtWhen(*n.StartDate)
	}
	d := newDetail(a, n.Title, sub, body)
	if len(n.Attachments) > 0 {
		d.keys = []key.Binding{keySaveAll, keyOpenAtt}
		d.handle = func(k tea.KeyMsg) (tea.Cmd, bool) {
			switch {
			case key.Matches(k, keySaveAll):
				return a.saveAttachments(c, n), true
			case key.Matches(k, keyOpenAtt):
				return a.openAttachment(c, n), true
			}
			return nil, false
		}
	}
	return d
}

// htmlToMarkdown prefers the HTML body and falls back to the plain text.
func htmlToMarkdown(r d2l.RichText) string {
	if strings.TrimSpace(r.HTML) != "" {
		if md, err := htmltomarkdown.ConvertString(r.HTML); err == nil {
			return md
		}
	}
	return r.Text
}
