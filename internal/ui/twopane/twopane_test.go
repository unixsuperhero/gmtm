package twopane

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type src struct{}

func (src) Root() Level {
	return Level{Title: "Jump", Origin: "clean-mtm · local · website · w1", Items: []Item{
		{ID: "a", Title: "alpha", Kind: "window"},
		{ID: "files", Title: "files", Kind: "mode", Nested: true},
		{ID: "beta", Title: "beta", Kind: "window"},
		{ID: "gone", Title: "gone", Kind: "window", Unavailable: "source disconnected"},
	}}
}

func (src) Children(_ context.Context, it Item) (Level, error) {
	if it.ID != "files" {
		return Level{}, errors.New("no children")
	}
	var items []Item
	for i := 0; i < 30; i++ {
		items = append(items, Item{ID: fmt.Sprint("f", i), Title: fmt.Sprintf("file-%02d-with-a-very-long-name-that-overflows", i), Kind: "file"})
	}
	return Level{Title: "Files", Items: items}, nil
}

func (src) Preview(_ context.Context, it Item) (string, error) {
	return strings.Repeat("preview of "+it.ID+" "+strings.Repeat("x", 200)+"\n", 50), nil
}

func k(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// drive sends a key and feeds resulting async replies back in.
func drive(m *Model, keys ...string) []tea.Msg {
	var out []tea.Msg
	for _, s := range keys {
		c := m.Update(k(s))
		for c != nil {
			msg := c()
			out = append(out, msg)
			c = nil
			switch msg.(type) {
			case PreviewMsg, ChildrenMsg:
				c = m.Update(msg)
			}
		}
	}
	return out
}

func TestPushPopRestoresQueryAndSelection(t *testing.T) {
	m := New(src{}, 80, 20)
	if c := m.Init(); c != nil {
		m.Update(c())
	}
	drive(m, "/", "b", "e", "enter") // root query "be", cursor on beta
	if it, _ := m.Current(); it.ID != "beta" {
		t.Fatalf("query cursor %v", it)
	}
	m = New(src{}, 80, 20)
	drive(m, "j") // cursor -> files
	drive(m, "/", "f", "i", "enter")
	if m.Query() != "fi" {
		t.Fatalf("query %q", m.Query())
	}
	drive(m, "enter") // open nested level
	if m.Depth() != 2 || strings.Join(m.Path(), "/") != "Jump/Files" {
		t.Fatalf("push failed: %v", m.Path())
	}
	drive(m, "j", "j", "/", "0", "3") // query in child level, active
	drive(m, "esc")                   // returns exactly one level even with active query
	if m.Depth() != 1 || m.Query() != "fi" {
		t.Fatalf("pop: depth %d query %q", m.Depth(), m.Query())
	}
	if it, _ := m.Current(); it.ID != "files" {
		t.Fatalf("selection not restored: %v", it)
	}
	if !strings.Contains(m.Preview(), "preview of files") {
		t.Fatal("preview did not follow restored cursor")
	}
	msgs := drive(m, "esc")
	if _, ok := msgs[len(msgs)-1].(CloseMsg); !ok {
		t.Fatalf("root esc: %v", msgs)
	}
}

func TestOpenAndUnavailable(t *testing.T) {
	m := New(src{}, 80, 20)
	msgs := drive(m, "enter")
	if o, ok := msgs[len(msgs)-1].(OpenMsg); !ok || o.Item.ID != "a" || !strings.Contains(o.Origin, "website") {
		t.Fatalf("open %v", msgs)
	}
	drive(m, "G")
	if !strings.HasPrefix(m.Preview(), "unavailable: source disconnected") {
		t.Fatalf("unavailable preview %q", m.Preview())
	}
	if msgs := drive(m, "enter"); len(msgs) != 0 {
		t.Fatal("opened unavailable item")
	}
}

func TestStalePreviewIgnored(t *testing.T) {
	m := New(src{}, 80, 20)
	c1 := m.Update(k("j"))
	c2 := m.Update(k("j"))
	m.Update(c2())
	m.Update(c1()) // stale
	if !strings.Contains(m.Preview(), "preview of beta") {
		t.Fatalf("stale preview applied: %.30q", m.Preview())
	}
}

func TestOverflowCropping(t *testing.T) {
	m := New(src{}, 60, 12)
	drive(m, "j", "enter")
	drive(m, "G")
	v := m.View()
	lines := strings.Split(v, "\n")
	if len(lines) != 12 {
		t.Fatalf("height %d", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > 60 {
			t.Fatalf("line %d width %d: %q", i, w, l)
		}
	}
	for _, want := range []string{"↑ ", "more", "lines below", "…"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing overflow indicator %q:\n%s", want, v)
		}
	}
	if !strings.Contains(v, "> file-29") {
		t.Fatalf("cursor row not visible:\n%s", v)
	}
	drive(m, "J")
	if !strings.Contains(m.View(), "lines above") {
		t.Fatal("preview scroll indicator")
	}
}
