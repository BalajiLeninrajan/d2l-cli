package tui

import (
	"errors"
	"slices"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/sync/errgroup"

	"github.com/BalajiLeninrajan/d2l-cli/internal/d2l"
)

type assignment struct {
	folder    d2l.DropboxFolder
	submitted bool
	score     *float64
	statusErr error
}

type assignMsg struct {
	courseID int
	items    []assignment
	err      error
}

func (m assignMsg) failure() error { return m.err }

type assignItem struct {
	a   assignment
	now time.Time
}

func (i assignItem) Title() string       { return i.a.folder.Name }
func (i assignItem) FilterValue() string { return i.a.folder.Name }

func (i assignItem) Description() string {
	due := "No due date"
	if d := i.a.folder.DueDate; d != nil {
		due = "Due " + d.Local().Format("Mon Jan 2 15:04") + " · " + relTime(*d, i.now)
	}
	return due + " · " + i.status()
}

func (i assignItem) status() string {
	switch {
	case i.a.statusErr != nil:
		return "status unknown"
	case i.a.score != nil:
		s := "feedback " + fmtNum(*i.a.score)
		if as := i.a.folder.Assessment; as != nil && as.ScoreDenominator != nil {
			s += "/" + fmtNum(*as.ScoreDenominator)
		}
		return s
	case i.a.submitted:
		return "submitted"
	}
	return "not submitted"
}

func (i assignItem) overdue() bool {
	d := i.a.folder.DueDate
	return d != nil && d.Before(i.now) && !i.a.submitted && i.a.statusErr == nil
}

func (i assignItem) tone() tone {
	switch {
	case i.overdue():
		return toneWarn
	case i.a.submitted || i.a.score != nil:
		return toneGood
	}
	return toneNormal
}

type assignmentsTab struct {
	listTab
	app    *app
	course d2l.Course
	parent view
}

func newAssignmentsTab(a *app, c d2l.Course, parent view) *assignmentsTab {
	t := &assignmentsTab{app: a, course: c, parent: parent}
	t.list = newList("Assignments", "assignment", "assignments", keyEnter, keyBack, keySearch, keyReload, keyTabN, keyNextTab, keyClose)
	return t
}

func (t *assignmentsTab) name() string { return "Assignments" }

func (t *assignmentsTab) Init() tea.Cmd {
	a, ou := t.app, t.course.ID()
	return tea.Batch(t.startLoading(), func() tea.Msg {
		items, err := loadAssignments(a, ou)
		return assignMsg{courseID: ou, items: items, err: err}
	})
}

// loadAssignments fetches the folders and then each folder's submission
// status. A failed status lookup marks that row unknown instead of failing
// the tab, except for an expired session.
func loadAssignments(a *app, ou int) ([]assignment, error) {
	folders, err := a.client.DropboxFolders(a.ctx, ou)
	if err != nil {
		return nil, err
	}
	folders = slices.DeleteFunc(folders, func(f d2l.DropboxFolder) bool { return f.IsHidden })
	slices.SortStableFunc(folders, func(x, y d2l.DropboxFolder) int {
		switch {
		case x.DueDate == nil && y.DueDate == nil:
			return 0
		case x.DueDate == nil:
			return 1
		case y.DueDate == nil:
			return -1
		}
		return x.DueDate.Compare(*y.DueDate)
	})

	out := make([]assignment, len(folders))
	g, ctx := errgroup.WithContext(a.ctx)
	g.SetLimit(6)
	for i, f := range folders {
		out[i].folder = f
		g.Go(func() error {
			subs, err := a.client.MySubmissions(ctx, ou, f.ID)
			if err != nil {
				if errors.Is(err, d2l.ErrUnauthorized) {
					return err
				}
				out[i].statusErr = err
				return nil
			}
			for _, s := range subs {
				if len(s.Submissions) > 0 {
					out[i].submitted = true
				}
				if s.Feedback != nil && s.Feedback.Score != nil {
					out[i].score = s.Feedback.Score
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

func (t *assignmentsTab) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case assignMsg:
		if msg.courseID != t.course.ID() || !t.finish(msg.err) || msg.err != nil {
			return nil
		}
		now := time.Now()
		items := make([]list.Item, len(msg.items))
		for i, a := range msg.items {
			items[i] = assignItem{a: a, now: now}
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
		case key.Matches(msg, keyEnter), key.Matches(msg, keyWeb):
			if it, ok := t.list.SelectedItem().(assignItem); ok {
				return openURL(t.app.client.DropboxWebURL(t.course.ID(), it.a.folder.ID))
			}
			return nil
		}
	}
	return t.update(msg)
}
