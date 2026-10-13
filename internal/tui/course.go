package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/BalajiLeninrajan/d2l-cli/internal/d2l"
)

// tab is one pane of the course screen. Init runs the first time the tab is
// shown.
type tab interface {
	view
	name() string
	captures(tea.KeyMsg) bool
	failed() bool
}

// listTab holds what every tab shares: a list, a loading flag and an
// inline error.
type listTab struct {
	list    list.Model
	loading bool
	err     error
}

func (t *listTab) captures(k tea.KeyMsg) bool { return listCaptures(t.list, k) }
func (t *listTab) failed() bool               { return t.err != nil }
func (t *listTab) SetSize(w, h int)           { sizeList(&t.list, w, h) }

func (t *listTab) startLoading() tea.Cmd {
	t.loading, t.err = true, nil
	return t.list.StartSpinner()
}

// finish ends a load. It reports false for stale results that arrive when
// nothing is loading.
func (t *listTab) finish(err error) bool {
	if !t.loading {
		return false
	}
	t.loading = false
	t.list.StopSpinner()
	t.err = err
	return true
}

func (t *listTab) update(msg tea.Msg) tea.Cmd { return updateList(&t.list, msg) }

func (t *listTab) View() string {
	if t.err != nil {
		return "\n" + inlineMargin.Render(errStyle.Render(errText(t.err))+"\n\n"+subtleStyle.Render("r retry · esc back"))
	}
	return t.list.View()
}

type courseView struct {
	app     *app
	course  d2l.Course
	tabs    []tab
	started []bool
	active  int
	w, h    int
}

func newCourseView(a *app, c d2l.Course) *courseView {
	v := &courseView{app: a, course: c}
	v.tabs = []tab{
		newContentTab(a, c, v),
		newNewsTab(a, c, v),
		newAssignmentsTab(a, c, v),
		newGradesTab(a, c, v),
	}
	v.started = make([]bool, len(v.tabs))
	return v
}

func (v *courseView) Init() tea.Cmd { return v.show(0) }

func (v *courseView) show(i int) tea.Cmd {
	v.active = i
	if v.started[i] {
		return nil
	}
	v.started[i] = true
	return v.tabs[i].Init()
}

const courseHeaderLines = 3

func (v *courseView) SetSize(w, h int) {
	v.w, v.h = w, h
	for _, t := range v.tabs {
		t.SetSize(w, max(0, h-courseHeaderLines))
	}
}

func (v *courseView) Update(msg tea.Msg) tea.Cmd {
	t := v.tabs[v.active]
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if t.failed() {
			// The list is hidden behind the error, so it gets no keys.
			switch {
			case key.Matches(msg, keyReload):
				return t.Update(msg)
			case key.Matches(msg, keyBack), key.Matches(msg, keyClose):
				return pop(v)
			}
		} else if t.captures(msg) {
			return t.Update(msg)
		}
		n := len(v.tabs)
		switch {
		case key.Matches(msg, keyNextTab):
			return v.show((v.active + 1) % n)
		case key.Matches(msg, keyPrevTab):
			return v.show((v.active + n - 1) % n)
		case key.Matches(msg, keyTabN):
			if i := int(msg.String()[0] - '1'); i < n {
				return v.show(i)
			}
			return nil
		case key.Matches(msg, keyClose):
			return pop(v)
		case key.Matches(msg, keySearch):
			return push(newSearchView(v.app, "Search "+v.course.Code(), []d2l.Course{v.course}))
		}
		if t.failed() {
			return nil
		}
		return t.Update(msg)
	case tea.MouseMsg, list.FilterMatchesMsg:
		return t.Update(msg)
	}
	cmds := make([]tea.Cmd, len(v.tabs))
	for i, t := range v.tabs {
		cmds[i] = t.Update(msg)
	}
	return tea.Batch(cmds...)
}

func (v *courseView) View() string {
	head := titleStyle.Render(truncate(v.course.Name(), max(0, v.w-2-len(v.course.Code())-2)))
	head = "  " + head + "  " + subtleStyle.Render(v.course.Code())
	parts := make([]string, len(v.tabs))
	for i, t := range v.tabs {
		label := fmt.Sprintf("%d %s", i+1, t.name())
		if i == v.active {
			parts[i] = tabActive.Render(label)
		} else {
			parts[i] = tabInactive.Render(label)
		}
	}
	tabs := "  " + strings.Join(parts, "   ")
	return head + "\n" + tabs + "\n\n" + v.tabs[v.active].View()
}
