package keys

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "f4":
		return tea.KeyMsg{Type: tea.KeyF4}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "ctrl+f":
		return tea.KeyMsg{Type: tea.KeyCtrlF}
	case "f5":
		return tea.KeyMsg{Type: tea.KeyF5}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestRoute(t *testing.T) {
	type step struct {
		key  string
		kind Kind
		nav  Nav
		got  string // Key or Action
	}
	cases := []struct {
		name  string
		mode  Mode
		steps []step
	}{
		{"f4 global in normal", Normal, []step{{"f4", Global, 0, "note.new"}}},
		{"f4 global in insert", Insert, []step{{"f4", Global, 0, "note.new"}}},
		{"f4 global in live", Live, []step{{"f4", Global, 0, "note.new"}}},
		{"j nav in normal", Normal, []step{{"j", Navigate, Down, "j"}}},
		{"j text in insert", Insert, []step{{"j", Pass, 0, "j"}, {"G", Pass, 0, "G"}, {"ctrl+f", Pass, 0, "ctrl+f"}}},
		{"motion text in palette", Palette, []step{{"k", Pass, 0, "k"}}},
		{"motion text in note", Note, []step{{"d", Pass, 0, "d"}}},
		{"live passes", Live, []step{{"j", Pass, 0, "j"}}},
		{"nav keys", Normal, []step{{"G", Navigate, Bottom, "G"}, {"g", Navigate, Top, "g"}, {"{", Navigate, PrevSection, "{"},
			{"}", Navigate, NextSection, "}"}, {"ctrl+f", Navigate, PageDown, "ctrl+f"}, {"pgdown", Navigate, PageDown, "pgdown"},
			{"down", Navigate, Down, "down"}, {"u", Navigate, HalfUp, "u"}, {"h", Navigate, Left, "h"}}},
		{"help nav", Help, []step{{"j", Navigate, Down, "j"}}},
		{"sequence", Normal, []step{{"n", Pending, 0, "n"}, {"t", Sequence, 0, "n t"}}},
		{"sequence uppercase", Normal, []step{{"N", Pending, 0, "N"}, {"a", Sequence, 0, "N a"}}},
		{"sequence esc cancels", Normal, []step{{"n", Pending, 0, "n"}, {"esc", Cancelled, 0, "esc"}, {"t", Pass, 0, "t"}}},
		{"sequence mismatch reroutes", Normal, []step{{"n", Pending, 0, "n"}, {"j", Navigate, Down, "j"}}},
		{"sequence not in insert", Insert, []step{{"n", Pass, 0, "n"}, {"t", Pass, 0, "t"}}},
		{"unbound passes", Normal, []step{{"x", Pass, 0, "x"}, {"f5", Pass, 0, "f5"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := NewRouter()
			r.AddSequences("n t", "N a", "x")
			for i, s := range c.steps {
				res := r.Route(c.mode, key(s.key))
				got := res.Key
				if res.Kind == Global {
					got = res.Action
				}
				if res.Kind != s.kind || res.Nav != s.nav || got != s.got {
					t.Fatalf("step %d %q: got %v/%v/%q want %v/%v/%q", i, s.key, res.Kind, res.Nav, got, s.kind, s.nav, s.got)
				}
			}
		})
	}
}

func TestSequenceTimeout(t *testing.T) {
	now := time.Unix(0, 0)
	r := NewRouter()
	r.Now = func() time.Time { return now }
	r.AddSequences("n t")
	if res := r.Route(Normal, key("n")); res.Kind != Pending {
		t.Fatal(res)
	}
	now = now.Add(2 * time.Second)
	if res := r.Route(Normal, key("t")); res.Kind != Pass {
		t.Fatalf("expired sequence should not complete: %v", res)
	}
	// TimeoutMsg path
	r.Route(Normal, key("n"))
	gen := r.gen
	if res := r.HandleTimeout(TimeoutMsg{Gen: gen - 1}); res.Kind != Pass {
		t.Fatal("stale timeout cancelled")
	}
	if res := r.HandleTimeout(TimeoutMsg{Gen: gen}); res.Kind != Cancelled || r.PendingKeys() != "" {
		t.Fatal("timeout did not cancel")
	}
}

func TestConfigurableGlobal(t *testing.T) {
	r := NewRouter()
	r.SetGlobal("f4", "")
	r.SetGlobal("f5", "note.new")
	if res := r.Route(Insert, key("f4")); res.Kind != Pass {
		t.Fatal("f4 still global")
	}
	if res := r.Route(Insert, key("f5")); res.Kind != Global || res.Action != "note.new" {
		t.Fatal("f5 not global")
	}
}

func TestModeNames(t *testing.T) {
	for m := Normal; m <= Note; m++ {
		p, ok := ParseMode(m.String())
		if !ok || p != m {
			t.Errorf("roundtrip %v", m)
		}
	}
}
