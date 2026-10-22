//go:build acceptance

package acceptance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/tuios"
)

// newServer starts a unique cmt-<8hex> server and registers cleanup that kills
// only that server and removes only its own socket/state dirs, even on failure.
func newServer(t *testing.T) tuios.Client {
	t.Helper()
	var rb [4]byte
	_, _ = rand.Read(rb[:])
	name := domain.TestServerPrefix + hex.EncodeToString(rb[:])
	c, err := tuios.New(name)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.StartServer(ctx); err != nil {
		t.Fatalf("start-server %s: %v", name, err)
	}
	t.Cleanup(func() {
		kctx, kcancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer kcancel()
		_ = c.KillServer(kctx) // may already be dead (isolation test kills it itself)
		if cl, ok := c.(interface{ Close() error }); ok {
			_ = cl.Close()
		}
		removeOwnState(t, name, c.SocketPath())
	})
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("ping %s: %v", name, err)
	}
	t.Logf("server %s socket %s", name, c.SocketPath())
	return c
}

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
	_ = os.RemoveAll(filepath.Join(base, "tuios-servers", name))
}

func tctx(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

// waitFor polls cond every 100ms until it is true or d elapses.
func waitFor(d time.Duration, cond func() bool) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if cond() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return cond()
}

func window(ctx context.Context, c tuios.Client, session, id string) (tuios.WindowInfo, string, error) {
	wl, err := tuios.ListWindows(ctx, c, session)
	if err != nil {
		return tuios.WindowInfo{}, "", err
	}
	for _, w := range wl.Windows {
		if w.WindowID == id {
			return w, wl.FocusedWindowID, nil
		}
	}
	return tuios.WindowInfo{}, wl.FocusedWindowID, fmt.Errorf("window %s not in %s", id, session)
}

// teaModel is the subset of tea.Model the app exposes.
type teaModel interface {
	Update(tea.Msg) (tea.Model, tea.Cmd)
}

// drain runs cmd and feeds resulting messages back into m, each step bounded.
func drain(m teaModel, cmd tea.Cmd, depth int) {
	if cmd == nil || depth > 12 {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		switch v := msg.(type) {
		case nil:
		case tea.BatchMsg:
			for _, c := range v {
				drain(m, c, depth+1)
			}
		default:
			if fmt.Sprintf("%T", msg) == "tea.sequenceMsg" {
				return
			}
			_, c := m.Update(msg)
			drain(m, c, depth+1)
		}
	case <-time.After(3 * time.Second):
	}
}
