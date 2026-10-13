package tui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	colAccent = lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#8B87FF"}
	colText   = lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#DDDDDD"}
	colSubtle = lipgloss.AdaptiveColor{Light: "#7A7A7A", Dark: "#8A8A8A"}
	colDim    = lipgloss.AdaptiveColor{Light: "#B5B0B5", Dark: "#555555"}
	colWarn   = lipgloss.AdaptiveColor{Light: "#C2410C", Dark: "#FB923C"}
	colErr    = lipgloss.AdaptiveColor{Light: "#C92A2A", Dark: "#FF6B6B"}
	colGood   = lipgloss.AdaptiveColor{Light: "#2B8A3E", Dark: "#69DB7C"}

	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	subtleStyle   = lipgloss.NewStyle().Foreground(colSubtle)
	errStyle      = lipgloss.NewStyle().Foreground(colErr)
	statusStyle   = lipgloss.NewStyle().Foreground(colSubtle)
	progressStyle = lipgloss.NewStyle().Foreground(colAccent)
	tabActive     = lipgloss.NewStyle().Bold(true).Foreground(colAccent).Underline(true)
	tabInactive   = lipgloss.NewStyle().Foreground(colSubtle)
	inlineMargin  = lipgloss.NewStyle().Padding(0, 2)

	itemTitle = lipgloss.NewStyle().Foreground(colText)
	itemDesc  = lipgloss.NewStyle().Foreground(colSubtle)
	selBar    = lipgloss.NewStyle().Foreground(colAccent)
)

type tone int

const (
	toneNormal tone = iota
	toneDim
	toneWarn
	toneGood
)

// row is what every list item implements. Items may also implement toned.
type row interface {
	list.Item
	Title() string
	Description() string
}

type toned interface{ tone() tone }

// delegate renders two-line rows with per-item tone. It skips filter match
// highlighting because FilterValue often differs from the title.
type delegate struct{}

func (delegate) Height() int                         { return 2 }
func (delegate) Spacing() int                        { return 1 }
func (delegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (delegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	r, ok := item.(row)
	if !ok || m.Width() <= 2 {
		return
	}
	width := m.Width() - 2
	title := truncate(oneLine(r.Title()), width)
	desc := truncate(oneLine(r.Description()), width)

	t := toneNormal
	if tt, ok := item.(toned); ok {
		t = tt.tone()
	}
	ts, ds := itemTitle, itemDesc
	switch t {
	case toneDim:
		ts, ds = ts.Foreground(colDim), ds.Foreground(colDim)
	case toneWarn:
		ds = ds.Foreground(colWarn)
	case toneGood:
		ds = ds.Foreground(colGood)
	}

	prefix := "  "
	switch {
	case m.FilterState() == list.Filtering && m.FilterValue() == "":
		ts, ds = ts.Foreground(colDim), ds.Foreground(colDim)
	case index == m.Index() && m.FilterState() != list.Filtering:
		prefix = selBar.Render("│") + " "
		if t != toneDim {
			ts = ts.Foreground(colAccent).Bold(true)
		}
	}
	fmt.Fprintf(w, "%s%s\n%s%s", prefix, ts.Render(title), prefix, ds.Render(desc))
}

// newList builds a list with keys that leave d, f, h and left free for the
// screens, and with help listing the screen's own bindings.
func newList(title, singular, plural string, keys ...key.Binding) list.Model {
	l := list.New(nil, delegate{}, 0, 0)
	l.Title = title
	l.Styles.Title = titleStyle
	l.SetStatusBarItemName(singular, plural)
	l.DisableQuitKeybindings()
	l.KeyMap.PrevPage = key.NewBinding(key.WithKeys("pgup", "ctrl+b"), key.WithHelp("pgup", "prev page"))
	l.KeyMap.NextPage = key.NewBinding(key.WithKeys("pgdown", "ctrl+f"), key.WithHelp("pgdn", "next page"))
	l.AdditionalShortHelpKeys = func() []key.Binding { return keys }
	l.AdditionalFullHelpKeys = func() []key.Binding { return keys }
	return l
}

// sizeList sizes l and narrows its help line by the help style's padding,
// which the list leaves out, so the last binding is elided instead of cut.
func sizeList(l *list.Model, w, h int) {
	l.SetSize(w, h)
	l.Help.Width = max(0, w-l.Styles.HelpStyle.GetHorizontalFrameSize())
}

// filterFor carries a FilterMatchesMsg to the list that asked for it.
type filterFor struct {
	l   *list.Model
	msg list.FilterMatchesMsg
}

// setItems replaces l's items. With a filter applied the list refilters in
// a command whose result names no list, and it would reach whichever view
// has focus. setItems addresses that result to l; it is then broadcast and
// updateList delivers it.
func setItems(l *list.Model, items []list.Item) tea.Cmd {
	cmd := l.SetItems(items)
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if m, ok := msg.(list.FilterMatchesMsg); ok {
			return filterFor{l: l, msg: m}
		}
		return msg
	}
}

// updateList passes msg to l. It unwraps a filterFor addressed to l and
// drops one addressed to another list.
func updateList(l *list.Model, msg tea.Msg) tea.Cmd {
	if f, ok := msg.(filterFor); ok {
		if f.l != l {
			return nil
		}
		msg = f.msg
	}
	var cmd tea.Cmd
	*l, cmd = l.Update(msg)
	return cmd
}

// listCaptures reports whether k belongs to the list itself: every key while
// the filter is being typed, and esc while a filter is applied (it clears it).
func listCaptures(l list.Model, k tea.KeyMsg) bool {
	switch l.FilterState() {
	case list.Filtering:
		return true
	case list.FilterApplied:
		return k.String() == "esc"
	}
	return false
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func truncate(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// truncateLeft keeps the end of s, for breadcrumbs.
func truncateLeft(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[1:]
	}
	return "…" + string(r)
}
