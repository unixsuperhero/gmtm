package modal

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/unixsuperhero/gmtm/internal/ui/frame"
)

// Component is a layer body.
type Component interface {
	Update(tea.Msg) (Component, tea.Cmd)
	View() string
}

// Layer is one modal on the stack.
type Layer struct {
	ID    string // optional identifier for the orchestrator
	Title string
	Body  Component
	// Width/Height request a box size (0 = fit content, capped to the area).
	Width, Height int
}

// Stack of modal layers (value type).
type Stack struct{ layers []Layer }

// Len is the number of layers.
func (s Stack) Len() int { return len(s.layers) }

// Empty reports no layers.
func (s Stack) Empty() bool { return len(s.layers) == 0 }

// Push adds l on top.
func (s Stack) Push(l Layer) Stack {
	n := make([]Layer, len(s.layers), len(s.layers)+1)
	copy(n, s.layers)
	return Stack{layers: append(n, l)}
}

// Pop removes the top layer.
func (s Stack) Pop() (Stack, Layer, bool) {
	if len(s.layers) == 0 {
		return s, Layer{}, false
	}
	top := s.layers[len(s.layers)-1]
	n := make([]Layer, len(s.layers)-1)
	copy(n, s.layers)
	return Stack{layers: n}, top, true
}

// Top returns the top layer.
func (s Stack) Top() (Layer, bool) {
	if len(s.layers) == 0 {
		return Layer{}, false
	}
	return s.layers[len(s.layers)-1], true
}

// Replace swaps the top layer (pushes when empty).
func (s Stack) Replace(l Layer) Stack {
	if len(s.layers) == 0 {
		return s.Push(l)
	}
	n := make([]Layer, len(s.layers))
	copy(n, s.layers)
	n[len(n)-1] = l
	return Stack{layers: n}
}

// Layers returns a copy of all layers bottom → top.
func (s Stack) Layers() []Layer { return append([]Layer(nil), s.layers...) }

// Path is the titles bottom → top.
func (s Stack) Path() []string {
	out := make([]string, len(s.layers))
	for i, l := range s.layers {
		out[i] = l.Title
	}
	return out
}

// BreadcrumbSep separates breadcrumb titles.
const BreadcrumbSep = " › "

// Breadcrumb is the joined path.
func (s Stack) Breadcrumb() string { return strings.Join(s.Path(), BreadcrumbSep) }

// Update forwards msg to the top layer.
func (s Stack) Update(msg tea.Msg) (Stack, tea.Cmd) {
	top, ok := s.Top()
	if !ok || top.Body == nil {
		return s, nil
	}
	body, cmd := top.Body.Update(msg)
	top.Body = body
	return s.Replace(top), cmd
}

var (
	boxBorder = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	crumb     = lipgloss.NewStyle().Bold(true)
)

// Box draws the top layer as a bordered box no larger than w×h, with the
// breadcrumb in its top border.
func (s Stack) Box(w, h int) string {
	top, ok := s.Top()
	if !ok || w < 4 || h < 3 {
		return ""
	}
	body := ""
	if top.Body != nil {
		body = top.Body.View()
	}
	bw, bh := top.Width, top.Height
	if bw <= 0 || bh <= 0 {
		cw, ch := 0, 0
		for _, l := range strings.Split(body, "\n") {
			cw = max(cw, frame.Width(l))
			ch++
		}
		if bw <= 0 {
			bw = max(cw, frame.Width(s.Breadcrumb())+4) + 2
		}
		if bh <= 0 {
			bh = ch + 2
		}
	}
	bw, bh = min(bw, w), min(bh, h)
	iw, ih := bw-2, bh-2
	title := frame.Truncate(" "+s.Breadcrumb()+" ", max(0, iw-1))
	fill := max(0, iw-1-frame.Width(title))
	var b strings.Builder
	b.WriteString(boxBorder.Render("╭─") + crumb.Render(title) + boxBorder.Render(strings.Repeat("─", fill)+"╮"))
	side := boxBorder.Render("│")
	for _, l := range frame.FitLines(body, iw, ih) {
		b.WriteString("\n" + side + l + side)
	}
	b.WriteString("\n" + boxBorder.Render("╰"+strings.Repeat("─", iw)+"╯"))
	return b.String()
}

// Render centres the top layer's box over base within w×h (the frame's inner
// area). With no layers it returns base fitted to w×h.
func (s Stack) Render(base string, w, h int) string {
	if s.Empty() {
		return strings.Join(frame.FitLines(base, w, h), "\n")
	}
	return frame.Overlay(base, s.Box(w, h), w, h)
}

// Wrap adapts a value component with a concretely typed Update.
func Wrap[T interface {
	Update(tea.Msg) (T, tea.Cmd)
	View() string
}](c T) Component {
	return wrapped[T]{c}
}

type wrapped[T interface {
	Update(tea.Msg) (T, tea.Cmd)
	View() string
}] struct{ c T }

func (w wrapped[T]) Update(msg tea.Msg) (Component, tea.Cmd) {
	c, cmd := w.c.Update(msg)
	return wrapped[T]{c}, cmd
}
func (w wrapped[T]) View() string { return w.c.View() }

// Unwrap returns the concrete component inside a Wrap'd Component.
func Unwrap[T interface {
	Update(tea.Msg) (T, tea.Cmd)
	View() string
}](c Component) (T, bool) {
	w, ok := c.(wrapped[T])
	return w.c, ok
}
