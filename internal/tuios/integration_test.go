package tuios_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/inventory"
	"github.com/unixsuperhero/gmtm/internal/tuios"
)

// TestIntegrationThrowawayServer runs against a real daemon on a unique server
// cmt-<8hex>. Enable with MTM_TUIOS_IT=1. It stops only its own server.
func TestIntegrationThrowawayServer(t *testing.T) {
	if os.Getenv("MTM_TUIOS_IT") != "1" {
		t.Skip("set MTM_TUIOS_IT=1 to run against a real tuios daemon")
	}
	var rb [4]byte
	_, _ = rand.Read(rb[:])
	name := domain.TestServerPrefix + hex.EncodeToString(rb[:])
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c, err := tuios.New(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("server %s socket %s", name, c.SocketPath())
	if err := c.StartServer(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		kctx, kcancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer kcancel()
		if err := c.KillServer(kctx); err != nil {
			t.Errorf("kill-server: %v", err)
		}
		removeOwnState(t, name, c.SocketPath())
	}()

	if err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := tuios.NewSession(ctx, c, "itproj", false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := tuios.NewSession(ctx, c, "itproj", false, ""); !tuios.IsCode(err, tuios.CodeSessionExists) {
		t.Fatalf("duplicate session: %v", err)
	}

	sub, err := c.Subscribe(ctx, []string{"window-created"}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if first := <-sub; first.Type != "subscribed" || first.BootID == "" {
		t.Fatalf("subscribe reply %+v", first)
	}

	w, err := tuios.NewWindow(ctx, c, tuios.NewWindowParams{Session: "itproj", Name: "it-shell"})
	if err != nil {
		t.Fatal(err)
	}
	if w.WindowID == "" {
		t.Fatal("no window id")
	}
	select {
	case ev := <-sub:
		if ev.Type != "window-created" || ev.Window != w.WindowID || ev.Session != "itproj" {
			t.Fatalf("event %+v", ev)
		}
	case <-ctx.Done():
		t.Fatal("no window-created event")
	}

	ss, err := tuios.ListSessions(ctx, c)
	if err != nil || len(ss) != 1 || ss[0].Name != "itproj" {
		t.Fatalf("sessions %+v %v", ss, err)
	}
	wl, err := tuios.ListWindows(ctx, c, "itproj")
	if err != nil || len(wl.Windows) != 1 || wl.Windows[0].WindowID != w.WindowID || wl.Windows[0].CustomName != "it-shell" {
		t.Fatalf("windows %+v %v", wl, err)
	}
	if wl.Windows[0].Focused {
		t.Fatal("new window stole focus")
	}
	if _, err := tuios.ListAgents(ctx, c, tuios.ListAgentsParams{AllSessions: true, All: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := tuios.ListHosts(ctx, c); err != nil {
		t.Fatal(err)
	}

	// stream without a lease: snapshot, then our input echoes back
	st, err := c.StreamPane(ctx, "itproj", w.WindowID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	f := <-st.Frames()
	if f.Type != tuios.FrameSnapshot || f.Cols == 0 || f.Rows == 0 {
		t.Fatalf("first frame %+v", f)
	}
	if err := st.SendInput(ctx, []byte("echo mtm-it-$((40+2))\n")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	for !bytes.Contains(out.Bytes(), []byte("mtm-it-42")) {
		select {
		case f, ok := <-st.Frames():
			if !ok {
				t.Fatalf("stream closed; got %q", out.String())
			}
			if f.Type == tuios.FrameOutput {
				out.Write(f.Data)
			}
		case <-ctx.Done():
			t.Fatalf("no echoed output; got %q", out.String())
		}
	}

	var text string
	for {
		text, err = tuios.CapturePane(ctx, c, "itproj", w.WindowID, "visible", 0, false)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "mtm-it-42") || ctx.Err() != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(text, "mtm-it-42") {
		t.Fatalf("capture %q", text)
	}

	// inventory over the same server
	inv := inventory.New(c)
	if err := inv.EnsureSession(ctx, domain.SessionNoProject); err != nil {
		t.Fatal(err)
	}
	if err := inv.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	snap := inv.Snapshot()
	var found *domain.Window
	for i := range snap.Windows {
		if snap.Windows[i].Target.WindowID == w.WindowID {
			found = &snap.Windows[i]
		}
	}
	if found == nil || found.Project != "itproj" || found.Kind != domain.KindTerminal || found.Target.Server != name {
		t.Fatalf("inventory row %+v", found)
	}
	if len(snap.Sessions) != 2 || snap.Connection.State != domain.ConnConnected {
		t.Fatalf("snapshot %+v", snap)
	}

	if err := tuios.CloseWindow(ctx, c, "itproj", w.WindowID); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"itproj", domain.SessionNoProject} {
		if err := tuios.KillSession(ctx, c, s); err != nil {
			t.Errorf("kill-session %s: %v", s, err)
		}
	}
}

// removeOwnState deletes the saved state and socket dir of this test's own server only.
func removeOwnState(t *testing.T, name, sock string) {
	if !strings.HasPrefix(name, domain.TestServerPrefix) {
		return
	}
	if d := filepath.Dir(sock); filepath.Base(d) == name {
		_ = os.RemoveAll(d)
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "tuios-servers", name)
	if err := os.RemoveAll(dir); err != nil {
		t.Logf("state cleanup %s: %v", dir, err)
	}
}
