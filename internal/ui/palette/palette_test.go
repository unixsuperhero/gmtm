package palette

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/gmtm/internal/actions"
)

func reg() *actions.Registry {
	r := actions.New()
	r.MustRegister(actions.Action{ID: "new.terminal", Verb: "New", Object: "Terminal", Keys: []string{"n t"}})
	r.MustRegister(actions.Action{ID: "new.agent", Verb: "New", Object: "Agent", Keys: []string{"n a"}})
	r.MustRegister(actions.Action{ID: "window.close", Verb: "Close", Object: "Window", NeedsTarget: true, Keys: []string{"x"}})
	r.MustRegister(actions.Action{ID: "window.rename", Verb: "Rename", Object: "Window", NeedsTarget: true})
	r.MustRegister(actions.Action{ID: "help", Verb: "Help", Keys: []string{"?"}})
	return r
}

func k(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func feed(p Model, keys ...string) (Model, tea.Msg) {
	var last tea.Msg
	for _, s := range keys {
		var cmd tea.Cmd
		p, cmd = p.Update(k(s))
		if cmd != nil {
			last = cmd()
		}
	}
	return p, last
}

func TestEscapeRules(t *testing.T) {
	cases := []struct {
		name      string
		keys      []string
		wantClose bool
		query     string
		searching bool
	}{
		{"empty esc closes", []string{"esc"}, true, "", true},
		{"typed esc leaves search", []string{"t", "e", "esc"}, false, "te", false},
		{"typed esc esc closes", []string{"t", "e", "esc", "esc"}, true, "te", false},
		{"reenter search keeps query", []string{"t", "esc", "/", "x"}, false, "tx", true},
		{"motion letters are text", []string{"j", "k"}, false, "jk", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, msg := feed(New(reg(), Options{}), c.keys...)
			_, closed := msg.(CloseMsg)
			if closed != c.wantClose || p.Query() != c.query || p.Searching() != c.searching {
				t.Fatalf("closed=%v q=%q searching=%v", closed, p.Query(), p.Searching())
			}
		})
	}
}

func TestVerbFirst(t *testing.T) {
	p := New(reg(), Options{})
	if got := p.Labels(); !reflect.DeepEqual(got, []string{"New", "Close", "Rename", "Help"}) {
		t.Fatalf("root %v", got)
	}
	p, _ = feed(p, "enter") // New
	if p.Breadcrumb() != "Palette › New" || !reflect.DeepEqual(p.Labels(), []string{"Terminal", "Agent"}) {
		t.Fatalf("level %q %v", p.Breadcrumb(), p.Labels())
	}
	_, msg := feed(p, "enter")
	if msg != (ExecuteMsg{ActionID: "new.terminal"}) {
		t.Fatalf("msg %#v", msg)
	}
	// typed search at root reaches the same action
	_, msg = feed(New(reg(), Options{}), "t", "e", "r", "m", "enter")
	if msg != (ExecuteMsg{ActionID: "new.terminal"}) {
		t.Fatalf("search msg %#v", msg)
	}
}

func TestObjectFirstAndUnbound(t *testing.T) {
	p := New(reg(), Options{TargetID: "srv|local|s|w1", TargetLabel: "api (agent)"})
	if p.Labels()[0] != "api (agent)" {
		t.Fatalf("root %v", p.Labels())
	}
	p, _ = feed(p, "enter")
	if !reflect.DeepEqual(p.Labels(), []string{"Close → Window", "Rename → Window"}) {
		t.Fatalf("target level %v", p.Labels())
	}
	if !strings.Contains(ansi.Strip(p.View()), "unbound") {
		t.Fatal("unbound hint missing")
	}
	_, msg := feed(p, "down", "enter")
	if msg != (ExecuteMsg{ActionID: "window.rename", TargetID: "srv|local|s|w1"}) {
		t.Fatalf("msg %#v", msg)
	}
}

func TestBackPreservesLevelState(t *testing.T) {
	cases := []struct {
		name   string
		keys   []string
		query  string
		cursor int
		search bool
		label  string
	}{
		{"highlight restored", []string{"down", "enter", "w", "backspace", "backspace"}, "", 1, true, "Close"},
		{"query restored", []string{"c", "l", "enter", "backspace"}, "cl", 0, true, "Close"},
		{"left search restored", []string{"down", "down", "esc"}, "", 0, true, ""}, // empty esc closes; checked below
		{"h back from non-search", []string{"down", "enter", "x", "esc", "h"}, "", 1, true, "Close"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, msg := feed(New(reg(), Options{}), c.keys...)
			if c.label == "" {
				if _, ok := msg.(CloseMsg); !ok {
					t.Fatalf("expected close, got %#v", msg)
				}
				return
			}
			if p.Depth() != 1 || p.Query() != c.query || p.Cursor() != c.cursor || p.Searching() != c.search || p.Highlighted() != c.label {
				t.Fatalf("depth=%d q=%q c=%d s=%v hl=%q", p.Depth(), p.Query(), p.Cursor(), p.Searching(), p.Highlighted())
			}
		})
	}
}
