//go:build acceptance

package acceptance

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/inventory"
	"github.com/unixsuperhero/gmtm/internal/launch"
	"github.com/unixsuperhero/gmtm/internal/preview"
	"github.com/unixsuperhero/gmtm/internal/tuios"
)

// verbLog wraps the real client and records every verb and stream request.
type verbLog struct {
	tuios.Client
	mu    sync.Mutex
	verbs []string
}

func (v *verbLog) Call(ctx context.Context, verb string, p, out any) error {
	v.mu.Lock()
	v.verbs = append(v.verbs, verb)
	v.mu.Unlock()
	return v.Client.Call(ctx, verb, p, out)
}

func (v *verbLog) StreamPane(ctx context.Context, s, w string, lc, lr uint16) (tuios.Stream, error) {
	v.mu.Lock()
	v.verbs = append(v.verbs, "stream-pane")
	v.mu.Unlock()
	return v.Client.StreamPane(ctx, s, w, lc, lr)
}

func TestPreviewSafety(t *testing.T) {
	c := newServer(t)
	ctx := tctx(t, 90*time.Second)
	inv := inventory.New(c)
	l := launch.New(c, inv, nil)
	l.LookPath = func(n string) (string, error) {
		if n == "nvim" {
			return "", exec.ErrNotFound
		}
		return exec.LookPath(n)
	}
	editor := []string{"vim", "-u", "NONE", "-N"}
	if _, err := exec.LookPath("vim"); err != nil {
		editor = []string{"less", "/etc/hosts"}
	}
	shell, err := l.Launch(ctx, domain.LaunchSpec{Kind: domain.KindTerminal, Project: "prev", Name: "sh"})
	if err != nil {
		t.Fatal(err)
	}
	ed, err := l.Launch(ctx, domain.LaunchSpec{Kind: domain.KindTerminal, Project: "prev", Name: "ed", Command: editor})
	if err != nil {
		t.Fatal(err)
	}
	if err := tuios.SendText(ctx, c, "prev", shell.Target.WindowID, "echo preview-marker-$((6*7))", false, true); err != nil {
		t.Fatal(err)
	}

	type geo struct {
		w, h    int
		focused string
	}
	geom := func(id string) geo {
		w, f, err := window(ctx, c, "prev", id)
		if err != nil {
			t.Fatal(err)
		}
		return geo{w.Width, w.Height, f}
	}
	before := map[string]geo{shell.Target.WindowID: geom(shell.Target.WindowID), ed.Target.WindowID: geom(ed.Target.WindowID)}

	vl := &verbLog{Client: c}
	ps := preview.New(vl)
	var shot, edShot preview.ResultMsg
	if !waitFor(10*time.Second, func() bool {
		shot = ps.CaptureSync(ctx, shell.Target, 1, preview.Opts{})
		return strings.Contains(shot.Text, "preview-marker-42")
	}) {
		t.Fatalf("shell preview: state %s err %v text %q", shot.State, shot.Err, shot.Text)
	}
	if !waitFor(10*time.Second, func() bool {
		edShot = ps.CaptureSync(ctx, ed.Target, 2, preview.Opts{})
		return edShot.State == preview.StateCurrent && (edShot.AltScreen || strings.Contains(edShot.Text, "~"))
	}) {
		t.Fatalf("editor preview: state %s alt %v err %v text %q", edShot.State, edShot.AltScreen, edShot.Err, edShot.Text)
	}
	t.Logf("editor %v alt_screen=%v rows=%d cols=%d", editor, edShot.AltScreen, edShot.Rows, edShot.Cols)

	for id, b := range before {
		if a := geom(id); a != b {
			t.Fatalf("window %s changed by preview: %+v -> %+v", id, b, a)
		}
	}
	vl.mu.Lock()
	for _, v := range vl.verbs {
		if v != "get-window" && v != "capture-pane" {
			t.Fatalf("preview issued non-read verb %q", v)
		}
	}
	vl.mu.Unlock()

	// generation guard: a late reply for the previous selection is rejected
	tr := &preview.Tracker{}
	g1 := tr.Begin(shell.Target)
	slow := make(chan preview.ResultMsg, 1)
	go func() { slow <- ps.CaptureSync(ctx, shell.Target, g1, preview.Opts{}) }()
	g2 := tr.Begin(ed.Target)
	fresh := ps.CaptureSync(ctx, ed.Target, g2, preview.Opts{})
	old := <-slow
	if tr.Accept(old) {
		t.Fatal("stale shell capture accepted after selection moved to editor")
	}
	if !tr.Accept(fresh) {
		t.Fatal("current capture rejected")
	}
}
