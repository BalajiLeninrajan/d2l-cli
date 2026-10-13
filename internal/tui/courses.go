package tui

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/BalajiLeninrajan/d2l-cli/internal/d2l"
)

type coursesMsg struct {
	courses []d2l.Course
	err     error
}

func (m coursesMsg) failure() error { return m.err }

type courseItem struct{ c d2l.Course }

func (i courseItem) Title() string { return i.c.Name() }
func (i courseItem) Description() string {
	if end := i.c.Access.EndDate; end != nil && end.Before(time.Now()) {
		return i.c.Code() + " · ended " + end.Local().Format("Jan 2 2006")
	}
	return i.c.Code()
}
func (i courseItem) FilterValue() string { return i.c.Name() + " " + i.c.Code() }

type coursesView struct {
	app     *app
	list    list.Model
	all     []d2l.Course
	showAll bool
	loading bool
	err     error
	w, h    int
}

func newCoursesView(a *app) *coursesView {
	return &coursesView{
		app:  a,
		list: newList("Courses", "course", "courses", keyEnter, keySearch, keyDownload, keyWeb, keyAll, keyReload, keyQuit),
	}
}

func (v *coursesView) Init() tea.Cmd { return v.reload() }

func (v *coursesView) reload() tea.Cmd {
	v.loading, v.err = true, nil
	a := v.app
	load := func() tea.Msg {
		cs, err := a.client.Courses(a.ctx)
		return coursesMsg{cs, err}
	}
	return tea.Batch(v.list.StartSpinner(), load)
}

func (v *coursesView) SetSize(w, h int) {
	v.w, v.h = w, h
	sizeList(&v.list, w, h)
}

func (v *coursesView) refresh() tea.Cmd {
	shown := make([]d2l.Course, 0, len(v.all))
	for _, c := range v.all {
		// Learn refuses every request for a course the user cannot access.
		if (v.showAll && c.Access.CanAccess) || c.Current() || c.Pinned() {
			shown = append(shown, c)
		}
	}
	slices.SortStableFunc(shown, func(a, b d2l.Course) int {
		if a.Pinned() != b.Pinned() {
			if a.Pinned() {
				return -1
			}
			return 1
		}
		return cmp.Compare(strings.ToLower(a.Name()), strings.ToLower(b.Name()))
	})
	items := make([]list.Item, len(shown))
	for i, c := range shown {
		items[i] = courseItem{c}
	}
	v.list.Title = "Courses"
	if v.showAll {
		v.list.Title = "All courses"
	}
	return setItems(&v.list, items)
}

func (v *coursesView) selected() (d2l.Course, bool) {
	it, ok := v.list.SelectedItem().(courseItem)
	return it.c, ok
}

func (v *coursesView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case coursesMsg:
		if !v.loading {
			return nil
		}
		v.loading = false
		v.list.StopSpinner()
		if msg.err != nil {
			v.err = msg.err
			return nil
		}
		v.all = msg.courses
		return v.refresh()
	case tea.KeyMsg:
		if v.err != nil {
			// The list is hidden behind the error, so it gets no keys.
			switch {
			case key.Matches(msg, keyQuit):
				return quit(msg.String())
			case key.Matches(msg, keyReload):
				v.app.clearTOCs()
				return v.reload()
			}
			return nil
		}
		if listCaptures(v.list, msg) {
			break
		}
		switch {
		case key.Matches(msg, keyQuit):
			return quit(msg.String())
		case key.Matches(msg, keyReload):
			v.app.clearTOCs()
			return v.reload()
		case key.Matches(msg, keyAll):
			v.showAll = !v.showAll
			return v.refresh()
		case key.Matches(msg, keySearch):
			var cs []d2l.Course
			for _, it := range v.list.VisibleItems() {
				cs = append(cs, it.(courseItem).c)
			}
			if len(cs) == 0 {
				return nil
			}
			title := "Search all courses"
			if len(cs) == 1 {
				title = "Search " + cs[0].Code()
			}
			return push(newSearchView(v.app, title, cs))
		}
		c, ok := v.selected()
		if !ok {
			break
		}
		switch {
		case key.Matches(msg, keyEnter):
			return push(newCourseView(v.app, c))
		case key.Matches(msg, keyDownload):
			return v.app.downloadCourse(c)
		case key.Matches(msg, keyWeb):
			return openURL(v.app.client.CourseWebURL(c.ID()))
		}
	}
	return updateList(&v.list, msg)
}

func (v *coursesView) View() string {
	if v.err != nil {
		return "\n" + inlineMargin.Render(errStyle.Render("Could not load courses: "+errText(v.err))+"\n\n"+subtleStyle.Render("r retry · q quit"))
	}
	return v.list.View()
}
