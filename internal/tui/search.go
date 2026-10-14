package tui

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/sync/errgroup"

	"github.com/BalajiLeninrajan/d2l-cli/internal/d2l"
	"github.com/BalajiLeninrajan/d2l-cli/internal/files"
)

type searchProgressMsg struct{ id, done, total int }

type searchDoneMsg struct {
	id     int
	hits   []searchHit
	failed int
	err    error // first failure
}

func (m searchDoneMsg) failure() error { return m.err }

type searchHit struct {
	c d2l.Course
	e files.Entry
}

func (h searchHit) Title() string { return h.e.Topic.Title }
func (h searchHit) Description() string {
	return strings.Join(append([]string{h.c.Code()}, h.e.Path...), " › ")
}
func (h searchHit) FilterValue() string {
	return h.c.Code() + " " + strings.Join(h.e.Path, " ") + " " + h.e.Topic.Title
}
func (h searchHit) tone() tone {
	if h.e.Topic.IsBroken || !h.e.Topic.Available(time.Now()) {
		return toneDim
	}
	return toneNormal
}

type searchView struct {
	app     *app
	id      int
	title   string
	courses []d2l.Course
	list    list.Model
	loading bool
	done    int // courses loaded so far
	err     error
}

func newSearchView(a *app, title string, courses []d2l.Course) *searchView {
	v := &searchView{app: a, id: a.nextID(), title: title, courses: courses}
	v.list = newList(title, "topic", "topics", keyEnter, keyDownload, keyWeb, keyBack)
	v.list.FilterInput.Prompt = "Search: "
	v.list.SetFilterState(list.Filtering)
	return v
}

func (v *searchView) Init() tea.Cmd {
	v.loading = true
	v.list.Title = fmt.Sprintf("%s (0/%d)", v.title, len(v.courses))
	v.list.FilterInput.Prompt = fmt.Sprintf("Search (0/%d): ", len(v.courses))
	a, id, courses := v.app, v.id, v.courses
	load := func() tea.Msg {
		perCourse := make([][]searchHit, len(courses))
		var (
			mu    sync.Mutex
			done  int
			first error
			fails int
		)
		var g errgroup.Group
		g.SetLimit(4)
		for i, c := range courses {
			g.Go(func() error {
				toc, err := a.toc(c.ID(), false)
				mu.Lock()
				done++
				n := done
				if err != nil {
					fails++
					if first == nil || errors.Is(err, d2l.ErrUnauthorized) {
						first = fmt.Errorf("%s: %w", c.Code(), err)
					}
				}
				mu.Unlock()
				a.post(searchProgressMsg{id: id, done: n, total: len(courses)})
				if err != nil {
					return nil
				}
				for _, e := range files.Entries(toc.Root(), files.CourseDir(a.cache, c), nil) {
					if !e.Topic.IsHidden {
						perCourse[i] = append(perCourse[i], searchHit{c: c, e: e})
					}
				}
				return nil
			})
		}
		_ = g.Wait()
		var hits []searchHit
		for _, h := range perCourse {
			hits = append(hits, h...)
		}
		return searchDoneMsg{id: id, hits: hits, failed: fails, err: first}
	}
	return tea.Batch(v.list.StartSpinner(), load)
}

func (v *searchView) SetSize(w, h int) { sizeList(&v.list, w, h) }

func (v *searchView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case searchProgressMsg:
		if msg.id == v.id && v.loading && msg.done > v.done {
			v.done = msg.done
			// The title is hidden while the filter is typed, so the prompt
			// carries the progress too.
			v.list.Title = fmt.Sprintf("%s (%d/%d)", v.title, msg.done, msg.total)
			v.list.FilterInput.Prompt = fmt.Sprintf("Search (%d/%d): ", msg.done, msg.total)
		}
		return nil
	case searchDoneMsg:
		if msg.id != v.id {
			return nil
		}
		v.loading = false
		v.list.StopSpinner()
		v.list.Title = v.title
		v.list.FilterInput.Prompt = "Search: "
		if msg.failed == len(v.courses) && msg.err != nil {
			v.err = msg.err
			// The hidden filter input would otherwise take every key.
			v.list.ResetFilter()
			return nil
		}
		items := make([]list.Item, len(msg.hits))
		for i, h := range msg.hits {
			items[i] = h
		}
		status := info(fmt.Sprintf("Found %s.", plural(len(items), "topic", "topics")))
		if msg.err != nil {
			status = fail(fmt.Errorf("could not load %s: %w", plural(msg.failed, "course", "courses"), msg.err))
		}
		return tea.Batch(setItems(&v.list, items), status)
	case tea.KeyMsg:
		if v.err != nil {
			if key.Matches(msg, keyBack) || key.Matches(msg, keyClose) {
				return pop(v)
			}
			return nil
		}
		if listCaptures(v.list, msg) {
			break
		}
		if key.Matches(msg, keyBack) || key.Matches(msg, keyClose) {
			return pop(v)
		}
		h, ok := v.list.SelectedItem().(searchHit)
		if !ok {
			break
		}
		switch {
		case key.Matches(msg, keyEnter):
			return v.app.openTopic(h.c, h.e)
		case key.Matches(msg, keyDownload):
			return v.app.downloadTopic(h.c, h.e)
		case key.Matches(msg, keyWeb):
			return openURL(v.app.client.TopicWebURL(h.c.ID(), h.e.Topic.TopicID))
		}
	}
	return updateList(&v.list, msg)
}

func (v *searchView) View() string {
	if v.err != nil {
		return "\n" + inlineMargin.Render(errStyle.Render("Search failed: "+errText(v.err))+"\n\n"+subtleStyle.Render("esc back"))
	}
	return v.list.View()
}
