package tui

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/BalajiLeninrajan/d2l-cli/internal/d2l"
	"github.com/BalajiLeninrajan/d2l-cli/internal/files"
)

type tocMsg struct {
	courseID int
	toc      *d2l.TOC
	err      error
}

func (m tocMsg) failure() error { return m.err }

// freshMsg reports which File topics are cached and current.
type freshMsg struct {
	courseID int
	fresh    map[int]bool
}

type nodeItem struct {
	n     d2l.Node
	fresh bool
}

func (i nodeItem) FilterValue() string { return i.n.Title() }

func (i nodeItem) Title() string {
	if i.n.Module != nil {
		return "▸ " + i.n.Module.Title
	}
	return i.n.Topic.Title
}

func (i nodeItem) Description() string {
	if m := i.n.Module; m != nil {
		return "folder · " + plural(len(m.Modules)+len(m.Topics), "item", "items")
	}
	t := i.n.Topic
	var s string
	switch {
	case t.IsBroken:
		s = "broken"
	case kindOf(*t) == kindFile:
		s = strings.TrimPrefix(strings.ToLower(filepath.Ext(files.FileName(*t))), ".")
		if s == "" {
			s = "file"
		}
		if i.fresh {
			s += " · ✓ cached"
		}
	case kindOf(*t) == kindLink:
		s = "↗ link"
	default:
		s = "web only · " + activityLabel(*t)
	}
	if u := unavailable(*t, time.Now()); u != "" {
		s += " · " + u
	}
	if t.IsHidden {
		s += " · hidden"
	}
	return s
}

func (i nodeItem) tone() tone {
	switch {
	case i.n.Topic != nil && (i.n.Topic.IsBroken || !i.n.Topic.Available(time.Now())):
		return toneDim
	case i.fresh:
		return toneGood
	}
	return toneNormal
}

type contentTab struct {
	listTab
	app     *app
	course  d2l.Course
	parent  view
	toc     *d2l.TOC
	levels  []*d2l.Module // levels[0] is the TOC root
	cursors []int         // selection to restore when going back up
	fresh   map[int]bool
	w       int
}

func newContentTab(a *app, c d2l.Course, parent view) *contentTab {
	t := &contentTab{app: a, course: c, parent: parent, fresh: map[int]bool{}}
	t.list = newList("Content", "item", "items", keyEnter, keyBack, keyDownload, keyWeb, keySearch, keyReload, keyTabN, keyNextTab, keyClose)
	return t
}

func (t *contentTab) name() string { return "Content" }

func (t *contentTab) Init() tea.Cmd { return t.load(false) }

func (t *contentTab) load(force bool) tea.Cmd {
	a, c := t.app, t.course
	fetch := func() tea.Msg {
		toc, err := a.toc(c.ID(), force)
		return tocMsg{courseID: c.ID(), toc: toc, err: err}
	}
	return tea.Batch(t.startLoading(), fetch)
}

func (t *contentTab) SetSize(w, h int) {
	t.w = w
	t.listTab.SetSize(w, h)
	t.setTitle()
}

// path is the module titles from the root to the current level.
func (t *contentTab) path() []string {
	var p []string
	for _, m := range t.levels[1:] {
		p = append(p, m.Title)
	}
	return p
}

func (t *contentTab) setTitle() {
	if len(t.levels) == 0 {
		return
	}
	crumbs := append([]string{"Content"}, t.path()...)
	t.list.Title = truncateLeft(strings.Join(crumbs, " › "), max(10, t.w-12))
}

func (t *contentTab) current() *d2l.Module { return t.levels[len(t.levels)-1] }

// setItems rebuilds the rows for the current level. With check set it also
// starts a freshness scan of the files shown.
func (t *contentTab) setItems(check bool) tea.Cmd {
	kids := t.current().Children()
	items := make([]list.Item, len(kids))
	for i, n := range kids {
		it := nodeItem{n: n}
		if n.Topic != nil {
			it.fresh = t.fresh[n.Topic.TopicID]
		}
		items[i] = it
	}
	t.setTitle()
	cmd := setItems(&t.list, items)
	if check {
		cmd = tea.Batch(cmd, t.checkFresh())
	}
	return cmd
}

// dir is the cache folder of the current level.
func (t *contentTab) dir() string { return files.ModuleDir(t.app.cache, t.course, t.levels) }

// entry describes a topic of the current level.
func (t *contentTab) entry(tp d2l.Topic) files.Entry {
	e := files.Entry{Path: t.path(), Topic: tp}
	if tp.IsFile() {
		e.Dest = filepath.Join(t.dir(), files.Names(t.current())[tp.TopicID])
	}
	return e
}

func (t *contentTab) checkFresh() tea.Cmd {
	dir, names := t.dir(), files.Names(t.current())
	dests := map[int]string{}
	topics := map[int]d2l.Topic{}
	for _, tp := range t.current().Topics {
		if tp.IsFile() {
			dests[tp.TopicID] = filepath.Join(dir, names[tp.TopicID])
			topics[tp.TopicID] = tp
		}
	}
	if len(topics) == 0 {
		return nil
	}
	id := t.course.ID()
	return func() tea.Msg {
		fresh := make(map[int]bool, len(topics))
		for tid, tp := range topics {
			fresh[tid] = files.Fresh(dests[tid], tp)
		}
		return freshMsg{courseID: id, fresh: fresh}
	}
}

func (t *contentTab) descend(m *d2l.Module) tea.Cmd {
	t.cursors = append(t.cursors, t.list.GlobalIndex())
	t.levels = append(t.levels, m)
	t.list.ResetFilter()
	cmd := t.setItems(true)
	t.list.Select(0)
	return cmd
}

func (t *contentTab) ascend() tea.Cmd {
	t.levels = t.levels[:len(t.levels)-1]
	sel := t.cursors[len(t.cursors)-1]
	t.cursors = t.cursors[:len(t.cursors)-1]
	t.list.ResetFilter()
	cmd := t.setItems(true)
	t.selectRow(sel)
	return cmd
}

// selectRow selects row i, clamped to the rows that exist; list.Select
// does not clamp.
func (t *contentTab) selectRow(i int) {
	t.list.Select(max(0, min(i, len(t.list.Items())-1)))
}

func (t *contentTab) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tocMsg:
		if msg.courseID != t.course.ID() || !t.finish(msg.err) || msg.err != nil {
			return nil
		}
		sel := 0
		var old []*d2l.Module
		if len(t.levels) > 0 {
			old, sel = t.levels[1:], t.list.GlobalIndex()
		}
		t.toc = msg.toc
		t.levels = relevel(msg.toc.Root(), old)
		if depth := len(t.levels) - 1; depth < len(old) {
			// The module we were in is gone; land on the row that led to it.
			sel = t.cursors[depth]
		}
		t.cursors = t.cursors[:len(t.levels)-1]
		t.list.ResetFilter()
		cmd := t.setItems(true)
		t.selectRow(sel)
		return cmd
	case freshMsg:
		if msg.courseID != t.course.ID() || t.toc == nil {
			return nil
		}
		for id, ok := range msg.fresh {
			t.fresh[id] = ok
		}
		return t.setItems(false)
	case jobDoneMsg:
		if t.toc == nil {
			return nil
		}
		return t.checkFresh()
	case tea.KeyMsg:
		if t.captures(msg) {
			break
		}
		if key.Matches(msg, keyBack) {
			if len(t.levels) > 1 {
				return t.ascend()
			}
			return pop(t.parent)
		}
		if key.Matches(msg, keyReload) {
			return t.load(true)
		}
		if t.toc == nil {
			return nil
		}
		it, ok := t.list.SelectedItem().(nodeItem)
		if !ok {
			break
		}
		if cmd, handled := t.handleKey(msg, it.n); handled {
			return cmd
		}
	}
	return t.update(msg)
}

func (t *contentTab) handleKey(k tea.KeyMsg, n d2l.Node) (tea.Cmd, bool) {
	a, c := t.app, t.course
	switch {
	case key.Matches(k, keyEnter):
		if n.Module != nil {
			return t.descend(n.Module), true
		}
		return a.openTopic(c, t.entry(*n.Topic)), true
	case key.Matches(k, keyWeb):
		if n.Module != nil {
			return openURL(a.client.ModuleWebURL(c.ID(), n.Module.ModuleID)), true
		}
		return openURL(a.client.TopicWebURL(c.ID(), n.Topic.TopicID)), true
	case key.Matches(k, keyDownload):
		if n.Module != nil {
			prefix := append(slices.Clone(t.path()), n.Module.Title)
			dir := filepath.Join(a.cwd, files.DirNames(t.current())[n.Module.ModuleID])
			es := files.Entries(n.Module, dir, prefix)
			return func() tea.Msg { return newDownload(c, es) }, true
		}
		return a.downloadTopic(c, t.entry(*n.Topic)), true
	}
	return nil, false
}

// relevel finds the modules of a previous path in a freshly loaded TOC by
// ID, so a reload keeps the user where they were. It stops at the first
// module that no longer exists.
func relevel(root *d2l.Module, old []*d2l.Module) []*d2l.Module {
	levels := []*d2l.Module{root}
	for _, want := range old {
		cur := levels[len(levels)-1]
		i := slices.IndexFunc(cur.Modules, func(m d2l.Module) bool { return m.ModuleID == want.ModuleID })
		if i < 0 {
			break
		}
		levels = append(levels, &cur.Modules[i])
	}
	return levels
}
