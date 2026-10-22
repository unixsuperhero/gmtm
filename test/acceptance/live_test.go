//go:build acceptance

package acceptance

import (
	"context"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/inventory"
	"github.com/unixsuperhero/gmtm/internal/launch"
	"github.com/unixsuperhero/gmtm/internal/live"
	"github.com/unixsuperhero/gmtm/internal/tuios"
)

// pump consumes a live session's frames in the background until it stops.
func pump(s *live.Session) {
	go func() {
		cmd := s.Init()
		for cmd != nil {
			msg := cmd()
			if msg == nil {
				return
			}
			if _, ok := msg.(live.ExitMsg); ok {
				return
			}
			cmd = s.Update(msg)
		}
	}()
}

// observer is a lease-free stream (like a viewer) that tracks the pane's real
// size from S/R frames. list-windows reports the client-requested size, not
// the leased PTY size, so the stream is where a lease is observable.
type observer struct {
	mu         sync.Mutex
	cols, rows uint16
	st         tuios.Stream
}

func observe(t *testing.T, ctx context.Context, c tuios.Client, tg domain.Target) *observer {
	t.Helper()
	st, err := c.StreamPane(ctx, tg.Session, tg.WindowID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	o := &observer{st: st}
	t.Cleanup(func() { st.Close() })
	go func() {
		for f := range st.Frames() {
			if f.Type == tuios.FrameSnapshot || f.Type == tuios.FrameResize {
				o.mu.Lock()
				o.cols, o.rows = f.Cols, f.Rows
				o.mu.Unlock()
			}
		}
	}()
	if !waitFor(5*time.Second, func() bool { c, _ := o.size(); return c > 0 }) {
		t.Fatal("observer got no snapshot")
	}
	return o
}

func (o *observer) size() (int, int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return int(o.cols), int(o.rows)
}

func TestLiveLeaseCleanup(t *testing.T) {
	c := newServer(t)
	ctx := tctx(t, 90*time.Second)
	inv := inventory.New(c)
	l := launch.New(c, inv, nil)
	var targets []domain.Target
	for _, n := range []string{"A", "B"} {
		r, err := l.Launch(ctx, domain.LaunchSpec{Kind: domain.KindTerminal, Project: "lv", Name: n})
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, r.Target)
	}
	a, b := targets[0], targets[1]
	listed := func(tg domain.Target) [2]int {
		w, _, err := window(ctx, c, tg.Session, tg.WindowID)
		if err != nil {
			t.Fatal(err)
		}
		return [2]int{w.Width, w.Height}
	}
	la, lb := listed(a), listed(b)
	oa, ob := observe(t, ctx, c, a), observe(t, ctx, c, b)
	aw, ah := oa.size()
	bw, bh := ob.size()
	t.Logf("initial pane A %dx%d B %dx%d (list-windows A %v B %v)", aw, ah, bw, bh, la, lb)
	if aw <= 40 || ah <= 10 {
		t.Fatalf("initial pane %dx%d already within the lease; cannot observe it", aw, ah)
	}
	held := func(o *observer) bool { w, h := o.size(); return w <= 40 && h <= 10 }
	is := func(o *observer, w, h int) func() bool {
		return func() bool { x, y := o.size(); return x == w && y == h }
	}

	s, err := live.Start(ctx, c, a, 40, 10)
	if err != nil {
		t.Fatal(err)
	}
	pump(s)
	if !waitFor(5*time.Second, func() bool { return held(oa) }) {
		w, h := oa.size()
		t.Fatalf("A not held by lease: %dx%d", w, h)
	}
	if held(ob) {
		t.Fatal("B leased while only A is live")
	}
	// literal leader: ctrl+a ctrl+a forwards one ctrl+a; leader alone sends nothing
	if _, cmd := s.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlA}); cmd != nil {
		t.Fatal("leader alone produced a command")
	}
	if !s.LeaderPending() {
		t.Fatal("leader not pending")
	}
	s.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlA})
	if s.LeaderPending() {
		t.Fatal("doubled leader left pending state")
	}

	s2, err := s.Switch(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	pump(s2)
	if !waitFor(5*time.Second, is(oa, aw, ah)) {
		w, h := oa.size()
		t.Fatalf("A not restored after switch: %dx%d want %dx%d", w, h, aw, ah)
	}
	if !waitFor(5*time.Second, func() bool { return held(ob) }) {
		w, h := ob.size()
		t.Fatalf("B not held while live: %dx%d", w, h)
	}
	if err := s2.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !waitFor(5*time.Second, is(ob, bw, bh)) {
		w, h := ob.size()
		t.Fatalf("B not restored after stop: %dx%d want %dx%d", w, h, bw, bh)
	}
	if listed(a) != la || listed(b) != lb {
		t.Fatalf("live changed requested window sizes: A %v->%v B %v->%v", la, listed(a), lb, listed(b))
	}
}
