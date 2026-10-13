package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/BalajiLeninrajan/d2l-cli/internal/d2l"
	"github.com/BalajiLeninrajan/d2l-cli/internal/files"
)

type testItem string

func (i testItem) FilterValue() string { return string(i) }

func items(ss ...string) []list.Item {
	out := make([]list.Item, len(ss))
	for i, s := range ss {
		out[i] = testItem(s)
	}
	return out
}

// A refilter started by setItems must reach only the list that asked, even
// when another list has focus.
func TestSetItemsFilterGoesToOwner(t *testing.T) {
	a := newList("A", "item", "items")
	b := newList("B", "item", "items")
	a.SetItems(items("lec01", "tut01"))
	a.SetFilterText("lec")
	b.SetItems(items("news"))

	cmd := setItems(&a, items("lec01", "lec02", "tut01"))
	if cmd == nil {
		t.Fatal("no refilter command with a filter applied")
	}
	msg, ok := cmd().(filterFor)
	if !ok {
		t.Fatalf("msg = %T, want filterFor", cmd())
	}
	if updateList(&b, msg) != nil || len(b.VisibleItems()) != 1 {
		t.Errorf("other list changed: %v", b.VisibleItems())
	}
	updateList(&a, msg)
	if got := len(a.VisibleItems()); got != 2 {
		t.Errorf("owner shows %d items, want 2", got)
	}
}

func TestQuitAsksAgainWhileDownloading(t *testing.T) {
	r := &root{app: &app{}}
	if _, ok := r.quit("q")().(tea.QuitMsg); !ok {
		t.Fatal("idle quit should quit")
	}
	r.jobs.bulk = &job{id: 1}
	if cmd := r.quit("q"); cmd != nil {
		t.Fatal("first quit during a download should only warn")
	}
	if r.status == "" {
		t.Error("no warning shown")
	}
	if _, ok := r.quit("q")().(tea.QuitMsg); !ok {
		t.Error("second quit should quit")
	}
}

func TestProgressIgnoresStaleUpdates(t *testing.T) {
	var q jobQueue
	q.bulk = &job{id: 3, total: 10}
	q.progress(jobProgressMsg{id: 3, done: 4, name: "b"})
	q.progress(jobProgressMsg{id: 3, done: 2, name: "a"})
	if q.bulk.done != 4 || q.bulk.current != "b" {
		t.Errorf("job = %+v, want done 4 at b", q.bulk)
	}
}

func TestNewDownloadSkipsUnavailable(t *testing.T) {
	future := time.Now().Add(24 * time.Hour)
	es := []files.Entry{
		{Topic: d2l.Topic{TopicID: 1, TypeIdentifier: "File"}, Dest: "/a"},
		{Topic: d2l.Topic{TopicID: 2, TypeIdentifier: "File", IsLocked: true}, Dest: "/b"},
		{Topic: d2l.Topic{TopicID: 3, TypeIdentifier: "File", StartDateTime: &future}, Dest: "/c"},
		{Topic: d2l.Topic{TopicID: 4, TypeIdentifier: "File", IsHidden: true}, Dest: "/d"},
		{Topic: d2l.Topic{TopicID: 5, TypeIdentifier: "Link"}},
	}
	m := newDownload(d2l.Course{}, es)
	if len(m.items) != 1 || m.items[0].dest != "/a" || m.unavailable != 2 {
		t.Errorf("download = %+v", m)
	}
}
