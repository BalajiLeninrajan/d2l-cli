package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
)

// detailView shows a title, a subtitle and a markdown body in a scrolling
// viewport. handle lets the opener add keys, checked before scrolling.
type detailView struct {
	app             *app
	title, subtitle string
	markdown        string
	keys            []key.Binding
	handle          func(tea.KeyMsg) (tea.Cmd, bool)
	vp              viewport.Model
	w, h            int
	renderedWidth   int
}

func newDetail(a *app, title, subtitle, markdown string) *detailView {
	return &detailView{app: a, title: title, subtitle: subtitle, markdown: markdown, vp: viewport.New(0, 0)}
}

func (d *detailView) Init() tea.Cmd { return nil }

const detailChrome = 4 // title, subtitle, blank line, footer

func (d *detailView) SetSize(w, h int) {
	d.w, d.h = w, h
	d.vp.Width = w
	d.vp.Height = max(1, h-detailChrome)
	if w != d.renderedWidth && w > 0 {
		d.render()
	}
}

// render runs glamour at the current width. Rendering is CPU only, so it is
// fine on the update loop.
func (d *detailView) render() {
	d.renderedWidth = d.w
	opts := append(slices.Clone(d.app.md), glamour.WithWordWrap(max(20, d.w-4)))
	out := d.markdown
	if r, err := glamour.NewTermRenderer(opts...); err == nil {
		if s, err := r.Render(d.markdown); err == nil {
			out = s
		}
	}
	if strings.TrimSpace(d.markdown) == "" {
		out = "\n  " + subtleStyle.Render("No content.")
	}
	d.vp.SetContent(out)
}

func (d *detailView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if d.handle != nil {
			if cmd, ok := d.handle(msg); ok {
				return cmd
			}
		}
		if key.Matches(msg, keyDetailBk) {
			return pop(d)
		}
	case tea.MouseMsg:
	default:
		return nil
	}
	var cmd tea.Cmd
	d.vp, cmd = d.vp.Update(msg)
	return cmd
}

func (d *detailView) View() string {
	head := "  " + titleStyle.Render(truncate(oneLine(d.title), max(0, d.w-4))) + "\n" +
		"  " + subtleStyle.Render(truncate(oneLine(d.subtitle), max(0, d.w-4)))
	help := []string{fmt.Sprintf("%3.f%%", d.vp.ScrollPercent()*100), "↑/↓ scroll", "esc back"}
	for _, k := range d.keys {
		h := k.Help()
		help = append(help, h.Key+" "+h.Desc)
	}
	foot := "  " + subtleStyle.Render(truncate(strings.Join(help, " · "), max(0, d.w-4)))
	return head + "\n\n" + d.vp.View() + "\n" + foot
}
