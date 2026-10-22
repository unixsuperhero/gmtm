//go:build acceptance

package acceptance

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/unixsuperhero/gmtm/internal/actions"
	"github.com/unixsuperhero/gmtm/internal/app"
	"github.com/unixsuperhero/gmtm/internal/inventory"
	"github.com/unixsuperhero/gmtm/internal/launch"
	"github.com/unixsuperhero/gmtm/internal/store"
)

func TestHeadlessFrame(t *testing.T) {
	c := newServer(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, proj := range []string{"", "alpha"} {
		inv := inventory.New(c)
		a := app.New(app.Deps{Client: c, Inv: inv, Store: st, Launch: launch.New(c, inv, st), Registry: actions.NewDefault(), Project: proj})
		drain(a, a.Init(), 0)
		a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		v := ansi.Strip(a.View())
		want := proj
		if want == "" {
			want = "no-project"
		}
		lines := strings.Split(v, "\n")
		if len(lines) != 24 {
			t.Fatalf("80x24 view has %d lines", len(lines))
		}
		for _, s := range []string{"cmtm", "NORMAL", want} {
			if !strings.Contains(lines[0], s) {
				t.Fatalf("top border lacks %q: %q", s, lines[0])
			}
		}
		if !strings.Contains(v, c.Server()) || !strings.Contains(v, "connected") {
			t.Fatalf("server/connection missing:\n%s", v)
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w > 80 {
				t.Fatalf("line %d is %d cells wide", i, w)
			}
		}
		a.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
		small := strings.Join(strings.Fields(ansi.Strip(a.View())), "")
		if !strings.Contains(small, "toosmall") || !strings.Contains(small, "cmtm") {
			t.Fatalf("20x5 lacks too-small state: %q", small)
		}
	}
}
