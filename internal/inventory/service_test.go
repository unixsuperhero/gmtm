package inventory_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/inventory"
	"github.com/unixsuperhero/gmtm/internal/tuios"
	"github.com/unixsuperhero/gmtm/internal/tuios/tuiostest"
)

// model is the fake daemon's state.
type model struct {
	mu       sync.Mutex
	sessions []string
	windows  map[string][]map[string]any
	agents   []map[string]any
	failAll  bool
}

func newModel() *model { return &model{windows: map[string][]map[string]any{}} }

func (m *model) install(d *tuiostest.Daemon) {
	fail := func() *tuios.Error { return &tuios.Error{Code: "internal", Message: "boom"} }
	d.Handle("list-sessions", func(json.RawMessage) (any, *tuios.Error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.failAll {
			return nil, fail()
		}
		ss := []any{}
		for _, s := range m.sessions {
			ss = append(ss, map[string]any{"name": s, "id": "id-" + s, "window_count": len(m.windows[s])})
		}
		return map[string]any{"sessions": ss}, nil
	})
	d.Handle("list-windows", func(p json.RawMessage) (any, *tuios.Error) {
		var q struct{ Session string }
		_ = json.Unmarshal(p, &q)
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, s := range m.sessions {
			if s == q.Session {
				return map[string]any{"windows": m.windows[s]}, nil
			}
		}
		return nil, &tuios.Error{Code: "session_not_found", Message: q.Session}
	})
	d.Handle("list-agents", func(p json.RawMessage) (any, *tuios.Error) {
		var q struct {
			Session     string
			AllSessions bool `json:"all_sessions"`
		}
		_ = json.Unmarshal(p, &q)
		m.mu.Lock()
		defer m.mu.Unlock()
		out := []any{}
		for _, a := range m.agents {
			if q.AllSessions || a["session"] == q.Session {
				out = append(out, a)
			}
		}
		return map[string]any{"agents": out}, nil
	})
	d.Handle("list-hosts", tuiostest.Fail("internal", "hosts unavailable"))
	d.Handle("new-session", func(p json.RawMessage) (any, *tuios.Error) {
		var q struct{ Name string }
		_ = json.Unmarshal(p, &q)
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, s := range m.sessions {
			if s == q.Name {
				return nil, &tuios.Error{Code: "session_exists", Message: "exists"}
			}
		}
		m.sessions = append(m.sessions, q.Name)
		return map[string]any{"session": q.Name}, nil
	})
}

func (m *model) addWindow(session, id, name, state string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.windows[session] = append(m.windows[session], map[string]any{"window_id": id, "custom_name": name, "agent_state": state, "cwd": "/src", "workspace": 1, "weird": "kept"})
}

func (m *model) removeWindow(session, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws := m.windows[session]
	for i, w := range ws {
		if w["window_id"] == id {
			m.windows[session] = append(ws[:i:i], ws[i+1:]...)
			return
		}
	}
}

func setup(t *testing.T) (*tuiostest.Daemon, *model, *inventory.Service) {
	d := tuiostest.Start(t)
	m := newModel()
	m.install(d)
	c, err := tuios.NewWithOptions("cmt-inv", tuios.Options{SocketPath: d.Path})
	if err != nil {
		t.Fatal(err)
	}
	s := inventory.New(c)
	s.BackoffMin, s.BackoffMax, s.ReadTimeout = 20*time.Millisecond, 100*time.Millisecond, 2*time.Second
	return d, m, s
}

func tctx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func find(snap domain.Snapshot, id string) *domain.Window {
	for i := range snap.Windows {
		if snap.Windows[i].Target.WindowID == id {
			return &snap.Windows[i]
		}
	}
	return nil
}

func waitFor(t *testing.T, s *inventory.Service, what string, cond func(domain.Snapshot) bool) domain.Snapshot {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		snap := s.Snapshot()
		if cond(snap) {
			return snap
		}
		select {
		case <-s.Updates():
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timed out waiting for %s; snapshot %+v", what, snap)
		}
	}
}

func TestRefreshBuildsSnapshot(t *testing.T) {
	_, m, s := setup(t)
	m.sessions = []string{"website", "no-project", "mtm-only"}
	m.addWindow("website", "w1", "shell", "none")
	m.addWindow("website", "w2", "claude", "working")
	m.addWindow("no-project", "w3", "edit", "none")
	m.addWindow("mtm-only", "w4", "ui", "none")
	m.agents = []map[string]any{
		{"session": "website", "window_id": "w2", "harness_id": "claude", "state": "working", "unread": 3, "queued": 1, "subagents": 2, "blocked_by": "approval", "needs_you": true},
		{"session": "no-project", "window_id": "w3", "state": "none", "foreground": "nvim"},
	}
	if err := s.Refresh(tctx(t)); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if snap.Connection.State != domain.ConnConnected || snap.Stale || snap.Connection.Server != "cmt-inv" || snap.Connection.LastSuccess.IsZero() {
		t.Fatalf("conn %+v stale %v", snap.Connection, snap.Stale)
	}
	if len(snap.Sessions) != 3 || len(snap.Windows) != 4 {
		t.Fatalf("%d sessions %d windows", len(snap.Sessions), len(snap.Windows))
	}
	w1, w2, w3, w4 := find(snap, "w1"), find(snap, "w2"), find(snap, "w3"), find(snap, "w4")
	if w1.Project != "website" || w1.Kind != domain.KindTerminal || w1.Target != (domain.Target{Server: "cmt-inv", Host: "local", Session: "website", WindowID: "w1"}) {
		t.Fatalf("w1 %+v", w1)
	}
	if w1.Raw["weird"] != "kept" {
		t.Fatal("raw not preserved")
	}
	want := domain.Flags{Working: true, Blocked: true, NeedsInput: true, Unread: 3, Queued: 1, Subagents: 2}
	if w2.Kind != domain.KindAgent || w2.Harness != "claude" || w2.State != domain.StateWorking || w2.Flags != want {
		t.Fatalf("w2 %+v", w2)
	}
	if w3.Project != "" || w3.Kind != domain.KindVim {
		t.Fatalf("w3 %+v", w3)
	}
	if w4.Project != "" {
		t.Fatalf("w4 %+v", w4)
	}
	// list-hosts failed: tolerated, local host still present
	if len(snap.Hosts) != 1 || snap.Hosts[0].Name != "local" {
		t.Fatalf("hosts %+v", snap.Hosts)
	}
}

func TestDerivations(t *testing.T) {
	cases := []struct {
		h, st string
		cmds  []string
		want  domain.Kind
	}{
		{"", "none", nil, domain.KindTerminal},
		{"", "", []string{"/usr/bin/nvim file"}, domain.KindVim},
		{"claude", "none", []string{"vim"}, domain.KindVim},
		{"codex", "none", nil, domain.KindAgent},
		{"", "idle", nil, domain.KindAgent},
	}
	for _, c := range cases {
		if got := inventory.DeriveKind(c.h, c.st, c.cmds...); got != c.want {
			t.Errorf("%+v → %s", c, got)
		}
	}
	if inventory.MapState("frobnicating") != domain.StateUnknown || inventory.MapState("") != domain.StateUnknown || inventory.MapState("done") != domain.StateDone {
		t.Fatal("state mapping")
	}
	if inventory.PlacementSession("") != "no-project" || inventory.PlacementSession("web") != "web" {
		t.Fatal("placement")
	}
	if inventory.ProjectForSession("mtm-only") != "" || inventory.ProjectForSession("no-project") != "" || inventory.ProjectForSession("api") != "api" {
		t.Fatal("project")
	}
}

func TestUnfamiliarStatePreserved(t *testing.T) {
	_, m, s := setup(t)
	m.sessions = []string{"web"}
	m.addWindow("web", "w1", "a", "pondering")
	if err := s.Refresh(tctx(t)); err != nil {
		t.Fatal(err)
	}
	w := find(s.Snapshot(), "w1")
	if w.State != domain.StateUnknown || w.RawState != "pondering" || w.Kind != domain.KindAgent {
		t.Fatalf("%+v", w)
	}
}

func TestFailedRefreshRetainsStale(t *testing.T) {
	_, m, s := setup(t)
	m.sessions = []string{"web"}
	m.addWindow("web", "w1", "a", "none")
	if err := s.Refresh(tctx(t)); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.failAll = true
	m.mu.Unlock()
	if err := s.Refresh(tctx(t)); err == nil {
		t.Fatal("expected error")
	}
	snap := s.Snapshot()
	w := find(snap, "w1")
	if w == nil || !w.Unavailable || w.Closed || !w.Stale || !snap.Stale {
		t.Fatalf("not retained as stale/unavailable: %+v", snap)
	}
	if snap.Connection.State != domain.ConnDisconnected || snap.Connection.LastError == "" || snap.Connection.LastSuccess.IsZero() {
		t.Fatalf("conn %+v", snap.Connection)
	}
}

func TestEnsureSessionIdempotent(t *testing.T) {
	d, m, s := setup(t)
	ctx := tctx(t)
	for i := 0; i < 2; i++ {
		if err := s.EnsureSession(ctx, "no-project"); err != nil {
			t.Fatal(err)
		}
	}
	if d.Count("new-session") != 2 || len(m.sessions) != 1 {
		t.Fatalf("sessions %v", m.sessions)
	}
	var p map[string]any
	for _, r := range d.Requests() {
		if r.Verb == "new-session" {
			_ = json.Unmarshal(r.Params, &p)
		}
	}
	if p["window"] != false {
		t.Fatalf("params %v", p)
	}
	snap := s.Snapshot()
	if len(snap.Sessions) != 1 || snap.Sessions[0].Name != "no-project" || snap.Sessions[0].Project != "" {
		t.Fatalf("%+v", snap.Sessions)
	}
}

func TestRunEventsIncrementalAndReconnect(t *testing.T) {
	d, m, s := setup(t)
	m.sessions = []string{"web", "api"}
	m.addWindow("web", "w1", "a", "none")
	m.addWindow("api", "a1", "x", "none")
	ctx, cancel := context.WithCancel(tctx(t))
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitFor(t, s, "connected", func(sn domain.Snapshot) bool {
		return sn.Connection.State == domain.ConnConnected && find(sn, "w1") != nil
	})
	waitFor(t, s, "subscriber", func(domain.Snapshot) bool { return d.Subscribers() == 1 })

	before := d.Count("list-sessions")
	apiReads := func() int {
		n := 0
		for _, r := range d.Requests() {
			var q struct{ Session string }
			_ = json.Unmarshal(r.Params, &q)
			if r.Verb == "list-windows" && q.Session == "api" {
				n++
			}
		}
		return n
	}
	apiBefore := apiReads()

	// window-created re-reads only the affected session
	m.addWindow("web", "w2", "b", "none")
	d.Emit(map[string]any{"type": "window-created", "session": "web", "window": "w2"})
	waitFor(t, s, "w2", func(sn domain.Snapshot) bool { return find(sn, "w2") != nil })
	if d.Count("list-sessions") != before || apiReads() != apiBefore {
		t.Fatal("event triggered a full poll")
	}

	// agent-state updates state
	m.mu.Lock()
	m.windows["web"][0]["agent_state"] = "needs_input"
	m.mu.Unlock()
	d.Emit(map[string]any{"type": "agent-state", "session": "web", "window": "w1"})
	waitFor(t, s, "needs_input", func(sn domain.Snapshot) bool {
		w := find(sn, "w1")
		return w != nil && w.State == domain.StateNeedsInput && w.Flags.NeedsInput
	})

	// window-closed: closed (not unavailable), retained
	m.removeWindow("web", "w2")
	d.Emit(map[string]any{"type": "window-closed", "session": "web", "window": "w2"})
	waitFor(t, s, "w2 closed", func(sn domain.Snapshot) bool {
		w := find(sn, "w2")
		return w != nil && w.Closed && !w.Unavailable
	})

	// session-closed closes its windows
	m.mu.Lock()
	m.sessions = []string{"web"}
	m.mu.Unlock()
	d.Emit(map[string]any{"type": "session-closed", "session": "api"})
	waitFor(t, s, "api closed", func(sn domain.Snapshot) bool {
		w := find(sn, "a1")
		return w != nil && w.Closed && len(sn.Sessions) == 1
	})

	// disconnect: stale + unavailable retained, then reconnect resynchronizes
	m.mu.Lock()
	m.failAll = true
	m.mu.Unlock()
	d.DropSubscribers()
	snap := waitFor(t, s, "disconnected", func(sn domain.Snapshot) bool { return sn.Connection.State == domain.ConnDisconnected })
	if w := find(snap, "w1"); w == nil || !w.Unavailable || !snap.Stale {
		t.Fatalf("not retained: %+v", snap)
	}
	m.mu.Lock()
	m.failAll = false
	m.mu.Unlock()
	snap = waitFor(t, s, "reconnected", func(sn domain.Snapshot) bool { return sn.Connection.State == domain.ConnConnected && !sn.Stale })
	if w := find(snap, "w1"); w == nil || w.Unavailable || w.Stale {
		t.Fatalf("not resynced: %+v", w)
	}
	if find(snap, "a1") != nil {
		t.Fatal("closed window survived a successful full refresh")
	}

	// gap forces a full resync
	waitFor(t, s, "resubscribed", func(domain.Snapshot) bool { return d.Subscribers() == 1 })
	before = d.Count("list-sessions")
	d.Emit(map[string]any{"type": "gap", "reason": "overflow"})
	waitFor(t, s, "gap resync", func(domain.Snapshot) bool { return d.Count("list-sessions") > before })

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestSnapshotIsCopy(t *testing.T) {
	_, m, s := setup(t)
	m.sessions = []string{"web"}
	m.addWindow("web", "w1", "a", "none")
	_ = s.Refresh(tctx(t))
	a := s.Snapshot()
	a.Windows[0].Name = "mutated"
	if s.Snapshot().Windows[0].Name == "mutated" {
		t.Fatal("snapshot aliases internal state")
	}
}
