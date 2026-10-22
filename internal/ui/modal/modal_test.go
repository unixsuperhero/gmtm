package modal

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type counter struct{ n int }

func (c counter) Update(msg tea.Msg) (counter, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		c.n++
	}
	return c, nil
}
func (c counter) View() string { return strings.Repeat("x", c.n) + "body" }

func TestStack(t *testing.T) {
	var s Stack
	a := s.Push(Layer{Title: "Palette", Body: Wrap(counter{})})
	b := a.Push(Layer{Title: "New", Body: Wrap(counter{})})
	if a.Len() != 1 || b.Len() != 2 || b.Breadcrumb() != "Palette › New" {
		t.Fatalf("push: %d %d %q", a.Len(), b.Len(), b.Breadcrumb())
	}
	b, _ = b.Update(tea.KeyMsg{Type: tea.KeyEnter})
	b, _ = b.Update(tea.KeyMsg{Type: tea.KeyEnter})
	top, _ := b.Top()
	if c, ok := Unwrap[counter](top.Body); !ok || c.n != 2 {
		t.Fatalf("top state %v", c)
	}
	b2, popped, ok := b.Pop()
	if !ok || popped.Title != "New" || b2.Len() != 1 {
		t.Fatal("pop")
	}
	under, _ := b2.Top()
	if c, _ := Unwrap[counter](under.Body); c.n != 0 {
		t.Fatal("parent state disturbed")
	}
	if _, _, ok := (Stack{}).Pop(); ok {
		t.Fatal("pop empty")
	}
}

func TestRenderInside(t *testing.T) {
	s := Stack{}.Push(Layer{Title: "A very long breadcrumb title here", Body: Wrap(counter{n: 100})})
	base := strings.Repeat(strings.Repeat(".", 30)+"\n", 8)
	out := ansi.Strip(s.Render(base, 30, 8))
	lines := strings.Split(out, "\n")
	if len(lines) != 8 {
		t.Fatalf("lines %d", len(lines))
	}
	for _, l := range lines {
		if ansi.StringWidth(l) != 30 {
			t.Fatalf("width %q", l)
		}
	}
	if !strings.Contains(out, "╭─ A very") {
		t.Fatalf("no breadcrumb:\n%s", out)
	}
}
