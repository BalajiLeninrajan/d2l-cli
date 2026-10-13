package tui

import (
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/BalajiLeninrajan/d2l-cli/internal/d2l"
)

type gradesMsg struct {
	courseID int
	final    *d2l.GradeValue
	items    []d2l.GradeValue
	err      error
}

func (m gradesMsg) failure() error { return m.err }

type gradeItem struct {
	g     d2l.GradeValue
	final bool
}

func (i gradeItem) Title() string {
	if i.final {
		return "Final grade"
	}
	return i.g.GradeObjectName
}

func (i gradeItem) tone() tone {
	if i.final {
		return toneGood
	}
	return toneNormal
}
func (i gradeItem) FilterValue() string { return i.g.GradeObjectName }

func (i gradeItem) hasComments() bool {
	return strings.TrimSpace(i.g.Comments.HTML) != "" || strings.TrimSpace(i.g.Comments.Text) != ""
}

func (i gradeItem) Description() string {
	g := i.g
	var parts []string
	switch {
	case strings.TrimSpace(g.DisplayedGrade) != "":
		parts = append(parts, strings.TrimSpace(g.DisplayedGrade))
	case g.PointsNumerator != nil && g.PointsDenominator != nil:
		parts = append(parts, fmtNum(*g.PointsNumerator)+"/"+fmtNum(*g.PointsDenominator))
	default:
		parts = append(parts, "no grade")
	}
	if g.WeightedNumerator != nil && g.WeightedDenominator != nil {
		parts = append(parts, "w "+fmtNum(*g.WeightedNumerator)+"/"+fmtNum(*g.WeightedDenominator))
	}
	if i.hasComments() {
		parts = append(parts, "comments")
	}
	return strings.Join(parts, " · ")
}

type gradesTab struct {
	listTab
	app    *app
	course d2l.Course
	parent view
}

func newGradesTab(a *app, c d2l.Course, parent view) *gradesTab {
	t := &gradesTab{app: a, course: c, parent: parent}
	t.list = newList("Grades", "grade", "grades", keyEnter, keyBack, keySearch, keyReload, keyTabN, keyNextTab, keyClose)
	return t
}

func (t *gradesTab) name() string { return "Grades" }

func (t *gradesTab) Init() tea.Cmd {
	a, ou := t.app, t.course.ID()
	return tea.Batch(t.startLoading(), func() tea.Msg {
		items, err := a.client.Grades(a.ctx, ou)
		if err != nil {
			return gradesMsg{courseID: ou, err: err}
		}
		// The final grade is extra; only a dead session fails the tab.
		final, err := a.client.FinalGrade(a.ctx, ou)
		if errors.Is(err, d2l.ErrUnauthorized) {
			return gradesMsg{courseID: ou, err: err}
		}
		return gradesMsg{courseID: ou, final: final, items: items}
	})
}

func (t *gradesTab) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case gradesMsg:
		if msg.courseID != t.course.ID() || !t.finish(msg.err) || msg.err != nil {
			return nil
		}
		items := make([]list.Item, 0, len(msg.items)+1)
		if msg.final != nil {
			items = append(items, gradeItem{g: *msg.final, final: true})
		}
		for _, g := range msg.items {
			items = append(items, gradeItem{g: g})
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
			it, ok := t.list.SelectedItem().(gradeItem)
			if !ok {
				return nil
			}
			if !it.hasComments() {
				return info("No comments on " + it.Title() + ".")
			}
			sub := t.course.Code() + " · " + it.Description()
			return push(newDetail(t.app, it.Title(), sub, htmlToMarkdown(it.g.Comments)))
		}
	}
	return t.update(msg)
}
