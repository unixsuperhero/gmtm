package help

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/gmtm/internal/actions"
)

func reg(t *testing.T) *actions.Registry {
	r := actions.New()
	r.MustRegister(actions.Action{ID: "note.new", Verb: "New", Object: "Note", Modes: []string{actions.ModeGlobal}, Keys: []string{"f4"}})
	r.MustRegister(actions.Action{ID: "new.terminal", Verb: "New", Object: "Terminal", Keys: []string{"n t"}, Description: "Open a terminal in the active project session with a long description that must wrap across lines"})
	r.MustRegister(actions.Action{ID: "window.rename", Verb: "Rename", Object: "Window", NeedsTarget: true})
	r.MustRegister(actions.Action{ID: "live.detach", Verb: "Detach", Modes: []string{actions.ModeLive}, Keys: []string{"ctrl+a d"}})
	r.MustRegister(actions.Action{ID: "jump.open", Verb: "Open", Object: "Jump", Modes: []string{actions.ModeJump}, Keys: []string{"enter"}})
	r.MustRegister(actions.Action{ID: "page.only", Verb: "Filter", Object: "Projects", Page: "projects", Keys: []string{"f"}})
	if err := r.Remap("new.terminal", []string{"T"}); err != nil {
		t.Fatal(err)
	}
	return r
}

func sections(es []Entry) map[string]string {
	out := map[string]string{}
	for _, e := range es {
		out[e.ID] = e.Section
	}
	return out
}

func TestEntries(t *testing.T) {
	cases := []struct {
		ctx  Context
		want map[string]string
	}{
		{Context{Mode: "normal", Page: "panes", Leader: "ctrl+a"}, map[string]string{
			"note.new": "Global", "new.terminal": "Normal mode", "window.rename": "Normal mode",
			"live.detach": "Live mode (leader ctrl+a)", "jump.open": "Other: jump", "page.only": "Other: normal"}},
		{Context{Mode: "live", Leader: "ctrl+a"}, map[string]string{
			"note.new": "Global", "live.detach": "Live mode", "new.terminal": "Other: normal"}},
	}
	for _, c := range cases {
		got := sections(Entries(reg(t), c.ctx))
		for id, s := range c.want {
			if got[id] != s {
				t.Errorf("%v: %s in %q want %q", c.ctx, id, got[id], s)
			}
		}
	}
}

func TestRenderMarksAndWraps(t *testing.T) {
	h := New(reg(t), Context{Mode: "normal", Leader: "ctrl+a"}).SetSize(50, 40)
	out := ansi.Strip(h.View())
	for _, want := range []string{"T *", "(remapped)", "unbound", "f4", "Live mode (leader ctrl+a)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	for _, l := range strings.Split(out, "\n") {
		if ansi.StringWidth(l) > 50 {
			t.Errorf("overwide %d: %q", ansi.StringWidth(l), l)
		}
	}
}

func TestSearchAndScroll(t *testing.T) {
	h := New(reg(t), Context{Mode: "normal"}).SetSize(40, 4)
	h, _ = h.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}) // scroll
	if h.Offset() != 1 {
		t.Fatalf("offset %d", h.Offset())
	}
	h, _ = h.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range "detach" {
		h, _ = h.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(h.Visible()) != 1 || h.Visible()[0].ID != "live.detach" {
		t.Fatalf("visible %v", h.Visible())
	}
	h, cmd := h.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || h.Searching() || h.Query() != "detach" {
		t.Fatal("esc should leave search first")
	}
	_, cmd = h.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := cmd().(CloseMsg); !ok {
		t.Fatal("second esc should close")
	}
}
