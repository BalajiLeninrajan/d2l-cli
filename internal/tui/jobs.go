package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/sync/errgroup"

	"github.com/BalajiLeninrajan/d2l-cli/internal/d2l"
	"github.com/BalajiLeninrajan/d2l-cli/internal/files"
)

// dlItem is one File topic to fetch to dest.
type dlItem struct {
	course d2l.Course
	topic  d2l.Topic
	dest   string
}

// downloadMsg asks the root to queue a download job. With open set, the
// first file is opened once it is on disk. unavailable counts files left
// out because Learn will not serve them yet.
type downloadMsg struct {
	items       []dlItem
	open        bool
	unavailable int
}

// newDownload builds a job for the visible, available files in es.
func newDownload(c d2l.Course, es []files.Entry) downloadMsg {
	var m downloadMsg
	now := time.Now()
	for _, e := range es {
		switch {
		case !e.Topic.IsFile() || e.Topic.IsHidden:
		case !e.Topic.Available(now):
			m.unavailable++
		default:
			m.items = append(m.items, dlItem{course: c, topic: e.Topic, dest: e.Dest})
		}
	}
	return m
}

type jobProgressMsg struct {
	id, done, total int
	name            string
}

type jobDoneMsg struct {
	id                          int
	downloaded, skipped, failed int
	unavailable                 int
	err                         error // first failure
	dests                       []string
	open                        bool
}

func (m jobDoneMsg) failure() error { return m.err }

type job struct {
	id          int
	items       []dlItem
	open        bool
	unavailable int
	done, total int
	current     string
}

// jobQueue runs single-file jobs right away, so opening a file never waits
// behind a bulk sync, and bulk jobs one at a time. pathLocks keeps any two
// jobs from writing the same file at once.
type jobQueue struct {
	next    int
	waiting []*job
	bulk    *job
	quick   []*job
}

func (q *jobQueue) add(a *app, m downloadMsg) tea.Cmd {
	q.next++
	j := &job{id: q.next, items: m.items, open: m.open, unavailable: m.unavailable, total: len(m.items)}
	if len(m.items) == 1 {
		q.quick = append(q.quick, j)
		return a.runJob(j)
	}
	q.waiting = append(q.waiting, j)
	return q.startNext(a)
}

func (q *jobQueue) startNext(a *app) tea.Cmd {
	if q.bulk != nil || len(q.waiting) == 0 {
		return nil
	}
	q.bulk, q.waiting = q.waiting[0], q.waiting[1:]
	return a.runJob(q.bulk)
}

func (q *jobQueue) find(id int) *job {
	if q.bulk != nil && q.bulk.id == id {
		return q.bulk
	}
	for _, j := range q.quick {
		if j.id == id {
			return j
		}
	}
	return nil
}

// progress applies an update. Updates are posted from goroutines and can
// arrive out of order, so one that would move the count back is dropped.
func (q *jobQueue) progress(m jobProgressMsg) {
	if j := q.find(m.id); j != nil && m.done >= j.done {
		j.done, j.current = m.done, m.name
	}
}

func (q *jobQueue) active() bool {
	return q.bulk != nil || len(q.quick) > 0 || len(q.waiting) > 0
}

func (q *jobQueue) finish(id int) {
	if q.bulk != nil && q.bulk.id == id {
		q.bulk = nil
		return
	}
	for i, j := range q.quick {
		if j.id == id {
			q.quick = append(q.quick[:i], q.quick[i+1:]...)
			return
		}
	}
}

func (q *jobQueue) view() string {
	var s string
	switch {
	case q.bulk != nil:
		s = fmt.Sprintf("↓ %d/%d %s", q.bulk.done, q.bulk.total, truncate(q.bulk.current, 32))
	case len(q.quick) > 0:
		s = "↓ " + truncate(q.quick[0].current, 32)
	default:
		return ""
	}
	if n := len(q.waiting); n > 0 {
		s += fmt.Sprintf(" (+%d queued)", n)
	}
	return s
}

func (r *root) jobDone(m jobDoneMsg) tea.Cmd {
	r.jobs.finish(m.id)
	cmds := []tea.Cmd{r.jobs.startNext(r.app)}
	single := len(m.dests) == 1
	switch {
	case m.failed > 0 && single:
		r.setStatus(statusMsg{err: fmt.Errorf("downloading %s: %w", filepath.Base(m.dests[0]), m.err)})
	case single && m.open:
		cmds = append(cmds, r.open(m.dests[0]))
	case single && m.skipped == 1:
		r.setStatus(statusMsg{text: filepath.Base(m.dests[0]) + " is up to date."})
	case single:
		r.setStatus(statusMsg{text: "Downloaded " + r.app.short(m.dests[0])})
	default:
		s := fmt.Sprintf("Downloaded %d, skipped %d, failed %d", m.downloaded, m.skipped, m.failed)
		if m.unavailable > 0 {
			s += fmt.Sprintf(", %d not available yet", m.unavailable)
		}
		s += " → " + r.app.short(commonDir(m.dests))
		if m.err != nil {
			r.setStatus(statusMsg{err: fmt.Errorf("%s (%s)", s, errText(m.err))})
		} else {
			r.setStatus(statusMsg{text: s})
		}
	}
	return tea.Batch(cmds...)
}

// runJob downloads j's items with cfg.Concurrency workers. It reads only
// copies of the job's fields, never the *job the root keeps mutating.
func (a *app) runJob(j *job) tea.Cmd {
	id, items, open, unavailable := j.id, j.items, j.open, j.unavailable
	return func() tea.Msg {
		res := jobDoneMsg{id: id, open: open, unavailable: unavailable, dests: make([]string, len(items))}
		if !a.begin() {
			return nil
		}
		defer a.work.Done()
		var mu sync.Mutex
		done := 0
		var g errgroup.Group
		g.SetLimit(a.cfg.Concurrency)
		for i, it := range items {
			dest := it.dest
			res.dests[i] = dest
			g.Go(func() error {
				name := filepath.Base(dest)
				mu.Lock()
				n := done
				mu.Unlock()
				a.post(jobProgressMsg{id: id, done: n, total: len(items), name: name})

				skipped, err := a.download(it, dest)

				mu.Lock()
				done++
				n = done
				switch {
				case err != nil:
					res.failed++
					if res.err == nil {
						res.err = err
					}
				case skipped:
					res.skipped++
				default:
					res.downloaded++
				}
				mu.Unlock()
				a.post(jobProgressMsg{id: id, done: n, total: len(items), name: name})
				return nil
			})
		}
		_ = g.Wait()
		return res
	}
}

func (a *app) download(it dlItem, dest string) (bool, error) {
	unlock, err := a.locks.lock(a.ctx, dest)
	if err != nil {
		return false, err
	}
	defer unlock()
	return files.Download(a.ctx, a.client, it.course.ID(), it.topic, dest)
}

// pathLocks serializes writers per destination path across jobs.
type pathLocks struct {
	mu   sync.Mutex
	held map[string]chan struct{}
}

func (p *pathLocks) lock(ctx context.Context, path string) (func(), error) {
	for {
		p.mu.Lock()
		ch, busy := p.held[path]
		if !busy {
			if p.held == nil {
				p.held = map[string]chan struct{}{}
			}
			ch = make(chan struct{})
			p.held[path] = ch
			p.mu.Unlock()
			return func() {
				p.mu.Lock()
				delete(p.held, path)
				p.mu.Unlock()
				close(ch)
			}, nil
		}
		p.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// short shows p relative to the download dir, where every download lands,
// so the status bar keeps the part that differs.
func (a *app) short(p string) string {
	if within(p, a.cfg.DownloadDir) {
		if rel, err := filepath.Rel(a.cfg.DownloadDir, p); err == nil && rel != "." {
			return rel
		}
	}
	return p
}

// commonDir is the deepest directory containing every path.
func commonDir(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	dir := filepath.Dir(paths[0])
	for _, p := range paths[1:] {
		for !within(p, dir) {
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return dir
}

func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
