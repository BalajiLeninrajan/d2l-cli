// Package tui is the interactive Bubble Tea front end.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/BalajiLeninrajan/d2l-cli/internal/config"
	"github.com/BalajiLeninrajan/d2l-cli/internal/d2l"
)

// app is the state shared by every view and by background commands. Fields
// are set before the program starts; the caches lock themselves.
type app struct {
	cfg    *config.Config
	client *d2l.Client
	ctx    context.Context
	send   func(tea.Msg)
	md     []glamour.TermRendererOption
	tocs   tocCache
	locks  pathLocks
	ids    atomic.Int64

	// work counts goroutines that write files, so Run can let them remove
	// their temp files before the process exits.
	workMu sync.Mutex
	work   sync.WaitGroup
	// expired records that Learn rejected the session at some point.
	expired atomic.Bool
}

func (a *app) nextID() int { return int(a.ids.Add(1)) }

// post sends msg without waiting. Program.Send blocks while a terminal
// opener holds the screen, and progress must not stall downloads.
func (a *app) post(msg tea.Msg) { go a.send(msg) }

// begin registers a file-writing goroutine. It reports false once the
// program is shutting down, and the caller must then do nothing.
func (a *app) begin() bool {
	a.workMu.Lock()
	defer a.workMu.Unlock()
	if a.ctx.Err() != nil {
		return false
	}
	a.work.Add(1)
	return true
}

// shutdown cancels background work and waits a little for writers to
// clean up.
func (a *app) shutdown(cancel context.CancelFunc) {
	a.workMu.Lock()
	cancel()
	a.workMu.Unlock()
	done := make(chan struct{})
	go func() {
		a.work.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}

// view is one screen on the stack. Views are pointers owned by the root
// model and are only touched from Update and View.
type view interface {
	Init() tea.Cmd
	Update(tea.Msg) tea.Cmd
	View() string
	SetSize(w, h int)
}

type (
	pushMsg   struct{ v view }
	popMsg    struct{ v view }
	statusMsg struct {
		text string
		err  error
	}
	openPathMsg struct{ path string }
	openURLMsg  struct{ url string }
	execDoneMsg struct{}
	// quitMsg asks the root to quit; key is what the user pressed.
	quitMsg struct{ key string }
)

// failer is a result message that may carry an error.
type failer interface{ failure() error }

func (m statusMsg) failure() error { return m.err }

func push(v view) tea.Cmd { return func() tea.Msg { return pushMsg{v} } }
func quit(key string) tea.Cmd {
	return func() tea.Msg { return quitMsg{key} }
}
func pop(v view) tea.Cmd { return func() tea.Msg { return popMsg{v} } }
func info(s string) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: s} }
}
func fail(err error) tea.Cmd {
	return func() tea.Msg { return statusMsg{err: err} }
}

func errText(err error) string {
	if errors.Is(err, d2l.ErrUnauthorized) {
		return "Session expired. Quit and run d2l login."
	}
	return err.Error()
}

// isInput reports whether msg belongs only to the focused view. Everything
// else is broadcast so results reach views that are not on top.
// FilterMatchesMsg carries no list identity, so it must not be broadcast.
func isInput(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.KeyMsg, tea.MouseMsg, list.FilterMatchesMsg:
		return true
	}
	return false
}

// Run starts the TUI and blocks until the user quits. It returns
// d2l.ErrUnauthorized if Learn rejected the session along the way.
func Run(cfg *config.Config, client *d2l.Client) error {
	// Query the terminal before Bubble Tea owns it; glamour's auto style
	// would query it again mid-program and garble input.
	style := "light"
	if lipgloss.HasDarkBackground() {
		style = "dark"
	}
	ctx, cancel := context.WithCancel(context.Background())

	a := &app{
		cfg:    cfg,
		client: client,
		ctx:    ctx,
		md: []glamour.TermRendererOption{
			glamour.WithStandardStyle(style),
			glamour.WithColorProfile(lipgloss.ColorProfile()),
		},
	}
	r := &root{app: a}
	r.stack = []view{newCoursesView(a)}
	p := tea.NewProgram(r, tea.WithAltScreen())
	a.send = p.Send
	_, err := p.Run()
	a.shutdown(cancel)
	if err == nil && a.expired.Load() {
		return d2l.ErrUnauthorized
	}
	return err
}

type root struct {
	app       *app
	stack     []view
	w, h      int
	status    string
	statusErr bool
	jobs      jobQueue
	// quitArmed is set after a quit was refused because downloads are
	// running; the next quit goes through.
	quitArmed bool
}

func (r *root) Init() tea.Cmd { return r.top().Init() }

func (r *root) top() view { return r.stack[len(r.stack)-1] }

func (r *root) bodySize() (int, int) { return r.w, max(0, r.h-1) }

func (r *root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if f, ok := msg.(failer); ok && errors.Is(f.failure(), d2l.ErrUnauthorized) {
		r.app.expired.Store(true)
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		r.w, r.h = msg.Width, msg.Height
		for _, v := range r.stack {
			v.SetSize(r.bodySize())
		}
		return r, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return r, r.quit(msg.String())
		}
		r.quitArmed = false
		return r, r.top().Update(msg)
	case quitMsg:
		return r, r.quit(msg.key)
	case pushMsg:
		msg.v.SetSize(r.bodySize())
		r.stack = append(r.stack, msg.v)
		return r, msg.v.Init()
	case popMsg:
		if len(r.stack) > 1 && r.top() == msg.v {
			r.stack = r.stack[:len(r.stack)-1]
		}
		return r, nil
	case statusMsg:
		r.setStatus(msg)
		return r, nil
	case openPathMsg:
		return r, r.open(msg.path)
	case openURLMsg:
		return r, r.open(msg.url)
	case downloadMsg:
		if len(msg.items) == 0 {
			text := "No files to download."
			if msg.unavailable > 0 {
				text = fmt.Sprintf("No files to download. %d are not available yet.", msg.unavailable)
			}
			r.setStatus(statusMsg{text: text})
			return r, nil
		}
		return r, r.jobs.add(r.app, msg)
	case jobProgressMsg:
		r.jobs.progress(msg)
		return r, nil
	case jobDoneMsg:
		cmds := []tea.Cmd{r.jobDone(msg)}
		for _, v := range r.stack {
			cmds = append(cmds, v.Update(msg))
		}
		return r, tea.Batch(cmds...)
	}
	if isInput(msg) {
		return r, r.top().Update(msg)
	}
	var cmds []tea.Cmd
	for _, v := range r.stack {
		cmds = append(cmds, v.Update(msg))
	}
	return r, tea.Batch(cmds...)
}

// quit exits unless downloads are running, in which case the first try
// only warns.
func (r *root) quit(key string) tea.Cmd {
	if r.jobs.active() && !r.quitArmed {
		r.quitArmed = true
		r.setStatus(statusMsg{text: "Downloads are still running. Press " + key + " again to stop them and quit."})
		return nil
	}
	return tea.Quit
}

func (r *root) setStatus(m statusMsg) {
	if m.err != nil {
		r.status, r.statusErr = errText(m.err), true
		return
	}
	r.status, r.statusErr = m.text, false
}

func (r *root) View() string {
	w, h := r.bodySize()
	body := lipgloss.NewStyle().Height(h).MaxWidth(w).MaxHeight(h).Render(r.top().View())
	return body + "\n" + r.statusView()
}

func (r *root) statusView() string {
	right := r.jobs.view()
	left := strings.ReplaceAll(r.status, "\n", " ")
	room := r.w - lipgloss.Width(right) - 1
	left = truncate(left, max(0, room))
	gap := max(1, r.w-lipgloss.Width(left)-lipgloss.Width(right))
	ls := statusStyle
	if r.statusErr {
		ls = errStyle
	}
	return ls.Render(left) + strings.Repeat(" ", gap) + progressStyle.Render(right)
}

// tocCache keeps tables of contents so search and bulk download reuse what
// the content tab already fetched.
type tocCache struct {
	mu sync.Mutex
	m  map[int]*d2l.TOC
}

func (a *app) toc(orgUnit int, force bool) (*d2l.TOC, error) {
	a.tocs.mu.Lock()
	t := a.tocs.m[orgUnit]
	a.tocs.mu.Unlock()
	if t != nil && !force {
		return t, nil
	}
	t, err := a.client.TOC(a.ctx, orgUnit)
	if err != nil {
		return nil, err
	}
	a.tocs.mu.Lock()
	if a.tocs.m == nil {
		a.tocs.m = map[int]*d2l.TOC{}
	}
	a.tocs.m[orgUnit] = t
	a.tocs.mu.Unlock()
	return t, nil
}

func (a *app) clearTOCs() {
	a.tocs.mu.Lock()
	a.tocs.m = nil
	a.tocs.mu.Unlock()
}
