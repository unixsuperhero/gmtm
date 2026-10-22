package tuios

import (
	"slices"
	"strings"
	"testing"
)

func TestValidateServerName(t *testing.T) {
	for _, ok := range []string{"clean-mtm", "a", "cmt-0123abcd", "A_b-9", strings.Repeat("x", 14)} {
		if err := ValidateServerName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", strings.Repeat("x", 15), "a/b", "a b", "ä", "clean.mtm"} {
		if err := ValidateServerName(bad); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}
}

func TestSocketBase(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	yes := func(string) bool { return true }
	no := func(string) bool { return false }
	if got := SocketBase(env(map[string]string{"XDG_RUNTIME_DIR": "/x"}), 7, yes); got != "/x/tuios" {
		t.Errorf("xdg: %s", got)
	}
	if got := SocketBase(env(nil), 7, yes); got != "/run/user/7/tuios" {
		t.Errorf("run: %s", got)
	}
	if got := SocketBase(env(nil), 501, no); got != "/tmp/tuios-501" {
		t.Errorf("tmp: %s", got)
	}
	// TUIOS_SOCKET is never consulted.
	if got := SocketBase(env(map[string]string{"TUIOS_SOCKET": "/evil.sock"}), 501, no); got != "/tmp/tuios-501" {
		t.Errorf("tuios_socket leaked: %s", got)
	}
	if got := ServerSocketPath("/tmp/tuios-501", "clean-mtm"); got != "/tmp/tuios-501/servers/clean-mtm/tuios.sock" {
		t.Errorf("path: %s", got)
	}
}

func TestResolveIgnoresTuiosSocket(t *testing.T) {
	t.Setenv("TUIOS_SOCKET", "/evil/tuios.sock")
	t.Setenv("XDG_RUNTIME_DIR", "/xdg")
	p, err := ResolveSocketPath("clean-mtm")
	if err != nil || p != "/xdg/tuios/servers/clean-mtm/tuios.sock" {
		t.Fatalf("%s %v", p, err)
	}
	if _, err := New(""); err == nil {
		t.Fatal("empty server name accepted")
	}
}

func TestScrubEnv(t *testing.T) {
	in := []string{"PATH=/bin", "TUIOS_SOCKET=/s", "TUIOS_SESSION=me", "TUIOS_PANE_ID=p", "TUIOS_WINDOW_ID=w",
		"TUIOS_PANE_TOKEN=t", "TUIOS_PANE_GRANTS=g", "TUIOS_SERVER=other", "TUIOS_THEME=keep", "HOME=/h"}
	got := ScrubEnv(in, "clean-mtm")
	want := []string{"PATH=/bin", "TUIOS_THEME=keep", "HOME=/h", "TUIOS_SERVER=clean-mtm"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestExplicitDefaultDaemon(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/xdg")
	t.Setenv("TUIOS_SOCKET", "/wrong/socket")
	t.Setenv("TUIOS_SERVER", "other")
	c, err := New(DefaultDaemon)
	if err != nil {
		t.Fatal(err)
	}
	if c.SocketPath() != "/xdg/tuios/tuios.sock" {
		t.Fatalf("default socket %q", c.SocketPath())
	}
	if ServerSocketPath("/xdg/tuios", "default") == c.SocketPath() {
		t.Fatal("named default server collided with unnamed daemon")
	}
	env := ScrubEnv([]string{"PATH=/bin", "TUIOS_SERVER=other", "TUIOS_SOCKET=/wrong/socket"}, DefaultDaemon)
	if !slices.Equal(env, []string{"PATH=/bin", "TUIOS_SERVER="}) {
		t.Fatalf("default daemon inherited routing: %v", env)
	}
}
