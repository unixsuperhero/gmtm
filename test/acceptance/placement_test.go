//go:build acceptance

package acceptance

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/inventory"
	"github.com/unixsuperhero/gmtm/internal/launch"
	"github.com/unixsuperhero/gmtm/internal/store"
	"github.com/unixsuperhero/gmtm/internal/tuios"
)

func TestPlacement(t *testing.T) {
	c := newServer(t)
	ctx := tctx(t, 60*time.Second)
	st, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	inv := inventory.New(c)
	l := launch.New(c, inv, st)

	none, err := l.Launch(ctx, domain.LaunchSpec{Kind: domain.KindTerminal, Project: "", Name: "loose"})
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := l.Launch(ctx, domain.LaunchSpec{Kind: domain.KindTerminal, Project: "alpha", Name: "owned"})
	if err != nil {
		t.Fatal(err)
	}
	if none.Target.Session != domain.SessionNoProject || alpha.Target.Session != "alpha" {
		t.Fatalf("placement sessions %q %q", none.Target.Session, alpha.Target.Session)
	}
	for _, x := range []struct{ s, id string }{{domain.SessionNoProject, none.Target.WindowID}, {"alpha", alpha.Target.WindowID}} {
		if _, _, err := window(ctx, c, x.s, x.id); err != nil {
			t.Fatal(err)
		}
	}
	if err := inv.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	for _, w := range inv.Snapshot().Windows {
		switch w.Target.WindowID {
		case none.Target.WindowID:
			if w.Project != "" {
				t.Fatalf("no-project window has project %q", w.Project)
			}
		case alpha.Target.WindowID:
			if w.Project != "alpha" {
				t.Fatalf("alpha window project %q", w.Project)
			}
		}
	}
	ps, err := st.Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Name == domain.SessionNoProject || p.Name == "" {
			t.Fatalf("fake project created: %+v", p)
		}
	}
	if p, _ := st.ProjectByName(ctx, "alpha"); p == nil {
		t.Fatal("submitted project alpha was not created")
	}

	// reassign alpha's window to project beta: same stable window id
	if err := inv.EnsureSession(ctx, "beta"); err != nil {
		t.Fatal(err)
	}
	if err := tuios.MoveWindow(ctx, c, "alpha", alpha.Target.WindowID, "beta"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := window(ctx, c, "beta", alpha.Target.WindowID); err != nil {
		t.Fatalf("moved window lost its id: %v", err)
	}
	if _, _, err := window(ctx, c, "alpha", alpha.Target.WindowID); err == nil {
		t.Fatal("window still in alpha after move")
	}
	if err := inv.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range inv.Snapshot().Windows {
		if w.Target.WindowID == alpha.Target.WindowID {
			found = true
			if w.Project != "beta" || w.Target.Session != "beta" {
				t.Fatalf("after move %+v project %q", w.Target, w.Project)
			}
		}
	}
	if !found {
		t.Fatal("moved window missing from inventory")
	}
}
