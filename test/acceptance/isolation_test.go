//go:build acceptance

package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/actions"
	"github.com/unixsuperhero/gmtm/internal/app"
	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/inventory"
	"github.com/unixsuperhero/gmtm/internal/launch"
	"github.com/unixsuperhero/gmtm/internal/store"
)

// defaultSessions lists the default daemon's session names with a clean env.
// ok=false means no default daemon is running (exit 3) or tuios failed.
func defaultSessions(t *testing.T) (names []string, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tuios", "ls", "--json")
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TUIOS_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		t.Logf("default daemon listing unavailable: %v", err)
		return nil, false
	}
	var rows []map[string]any
	if err := json.Unmarshal(out, &rows); err != nil {
		var wrap struct {
			Sessions []map[string]any `json:"sessions"`
		}
		if err2 := json.Unmarshal(out, &wrap); err2 != nil {
			t.Logf("default daemon listing not JSON: %v", err)
			return nil, false
		}
		rows = wrap.Sessions
	}
	for _, r := range rows {
		if saved, _ := r["saved"].(bool); saved {
			return nil, false
		}
		n, _ := r["name"].(string)
		names = append(names, n)
	}
	sort.Strings(names)
	return names, true
}

func TestIsolation(t *testing.T) {
	before, haveDefault := defaultSessions(t)
	bogus := filepath.Join(t.TempDir(), "user-daemon.sock")
	t.Setenv("TUIOS_SOCKET", bogus)
	t.Setenv("TUIOS_SESSION", "users-own-session")
	t.Setenv("TUIOS_PANE_ID", "pane-bogus")

	c := newServer(t)
	name := c.Server()
	sock := c.SocketPath()
	if sock == bogus || filepath.Base(filepath.Dir(sock)) != name || filepath.Base(filepath.Dir(filepath.Dir(sock))) != "servers" {
		t.Fatalf("socket %q is not the named server's (bogus %q)", sock, bogus)
	}
	for _, kv := range c.ChildEnv() {
		if strings.HasPrefix(kv, "TUIOS_SOCKET=") || strings.HasPrefix(kv, "TUIOS_SESSION=") || strings.HasPrefix(kv, "TUIOS_PANE_ID=") {
			t.Fatalf("child env leaks %s", kv)
		}
	}
	ctx := tctx(t, 60*time.Second)

	inv := inventory.New(c)
	l := launch.New(c, inv, nil)
	res, err := l.Launch(ctx, domain.LaunchSpec{Kind: domain.KindTerminal, Name: "iso"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Target.Server != name {
		t.Fatalf("target server %q", res.Target.Server)
	}
	if err := inv.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	snap := inv.Snapshot()
	if snap.Connection.State != domain.ConnConnected || snap.Connection.Server != name {
		t.Fatalf("connection %+v", snap.Connection)
	}
	if len(snap.Sessions) != 1 || snap.Sessions[0].Name != domain.SessionNoProject {
		t.Fatalf("named server sessions %+v", snap.Sessions)
	}

	if haveDefault {
		after, _ := defaultSessions(t)
		if strings.Join(before, ",") != strings.Join(after, ",") {
			t.Fatalf("default daemon sessions changed: %v -> %v", before, after)
		}
		t.Logf("default daemon untouched: %d sessions before and after", len(after))
		for _, n := range before {
			if n == domain.SessionNoProject {
				t.Logf("note: default daemon has its own no-project session")
			}
		}
	} else {
		t.Log("SKIP sub-assert: no default daemon running; default-session comparison not performed")
	}

	// lose the dedicated server: disconnected, stale data kept, no fallback
	kctx, kcancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer kcancel()
	if err := c.KillServer(kctx); err != nil {
		t.Fatal(err)
	}
	if !waitFor(10*time.Second, func() bool { return c.Ping(ctx) != nil }) {
		t.Fatal("server still answers after kill-server")
	}
	if err := inv.Refresh(ctx); err == nil {
		t.Fatal("refresh succeeded against a dead server (fallback?)")
	}
	snap = inv.Snapshot()
	if snap.Connection.State != domain.ConnDisconnected {
		t.Fatalf("state after kill %q", snap.Connection.State)
	}
	for _, s := range snap.Sessions {
		if s.Name != domain.SessionNoProject || !s.Stale {
			t.Fatalf("unexpected session after disconnect (fallback?): %+v", s)
		}
	}
	for _, w := range snap.Windows {
		if w.Target.Server != name {
			t.Fatalf("window from another server %+v", w.Target)
		}
	}

	// a fresh app on the dead server reports disconnected and shows no sessions
	st, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	inv2 := inventory.New(c)
	a := app.New(app.Deps{Client: c, Inv: inv2, Store: st, Launch: launch.New(c, inv2, st), Registry: actions.NewDefault()})
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	drain(a, a.Init(), 0)
	v := a.View()
	if !strings.Contains(v, "disconnected") || !strings.Contains(v, name) {
		t.Fatalf("app view lacks disconnected/server:\n%s", v)
	}
	if s := inv2.Snapshot(); len(s.Sessions) != 0 || len(s.Windows) != 0 {
		t.Fatalf("sessions appeared without a server: %+v", s.Sessions)
	}
	if haveDefault {
		after, _ := defaultSessions(t)
		if strings.Join(before, ",") != strings.Join(after, ",") {
			t.Fatalf("default daemon sessions changed after kill: %v -> %v", before, after)
		}
	}
}
