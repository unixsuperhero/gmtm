package live

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/tuios"
)

type log struct {
	mu sync.Mutex
	ev []string
}

func (l *log) add(f string, a ...any) {
	l.mu.Lock()
	l.ev = append(l.ev, fmt.Sprintf(f, a...))
	l.mu.Unlock()
}
func (l *log) all() []string { l.mu.Lock(); defer l.mu.Unlock(); return append([]string(nil), l.ev...) }

type fakeStream struct {
	win    string
	log    *log
	frames chan tuios.Frame
	mu     sync.Mutex
	input  [][]byte
}

func (f *fakeStream) Frames() <-chan tuios.Frame { return f.frames }
func (f *fakeStream) SendInput(_ context.Context, b []byte) error {
	f.mu.Lock()
	f.input = append(f.input, append([]byte(nil), b...))
	f.mu.Unlock()
	f.log.add("input %s %q", f.win, b)
	return nil
}
func (f *fakeStream) Lease(_ context.Context, c, r uint16) error {
	f.log.add("lease %s %dx%d", f.win, c, r)
	return nil
}
func (f *fakeStream) Close() error { f.log.add("close %s", f.win); return nil }
func (f *fakeStream) inputs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, b := range f.input {
		out = append(out, string(b))
	}
	return out
}

type fakeClient struct {
	log     *log
	streams map[string]*fakeStream
}

func (c *fakeClient) Server() string     { return "clean-mtm" }
func (c *fakeClient) SocketPath() string { return "" }
func (c *fakeClient) Call(_ context.Context, verb string, _ any, _ any) error {
	c.log.add("call %s", verb)
	return nil
}
func (c *fakeClient) Subscribe(context.Context, []string, int64, string) (<-chan tuios.Event, error) {
	return nil, nil
}
func (c *fakeClient) StreamPane(_ context.Context, session, window string, lc, lr uint16) (tuios.Stream, error) {
	c.log.add("open %s %dx%d", window, lc, lr)
	s := &fakeStream{win: window, log: c.log, frames: make(chan tuios.Frame, 8)}
	c.streams[window] = s
	return s, nil
}
func (c *fakeClient) Ping(context.Context) error        { return nil }
func (c *fakeClient) StartServer(context.Context) error { return nil }
func (c *fakeClient) KillServer(context.Context) error  { return nil }
func (c *fakeClient) ChildEnv() []string                { return nil }

func newFake() *fakeClient { return &fakeClient{log: &log{}, streams: map[string]*fakeStream{}} }

func target(w string) domain.Target {
	return domain.Target{Server: "clean-mtm", Session: "work", WindowID: w}
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

var ctrlA = tea.KeyMsg{Type: tea.KeyCtrlA}

func msgOf(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("nil cmd")
	}
	return cmd()
}

func TestLeaderStateMachine(t *testing.T) {
	fc := newFake()
	s, err := Start(context.Background(), fc, target("w1"), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	// leader + d → detach, nothing forwarded
	if ok, cmd := s.HandleKey(ctrlA); !ok || cmd != nil || !s.LeaderPending() {
		t.Fatal("leader not pending")
	}
	ok, cmd := s.HandleKey(runes("d"))
	if _, isDetach := msgOf(t, cmd).(DetachMsg); !ok || !isDetach {
		t.Fatal("expected detach")
	}
	// next/prev/zoom/help
	for k, want := range map[string]tea.Msg{"n": SwitchMsg{Delta: 1}, "p": SwitchMsg{Delta: -1}, "?": LeaderHelpMsg{Leader: "ctrl+a"}, "z": ZoomMsg{Target: target("w1")}} {
		s.HandleKey(ctrlA)
		_, cmd := s.HandleKey(runes(k))
		if got := msgOf(t, cmd); got != want {
			t.Fatalf("%s: got %#v want %#v", k, got, want)
		}
	}
	// unknown combo consumed and reported, never forwarded
	s.HandleKey(ctrlA)
	ok, cmd = s.HandleKey(runes("x"))
	if m, isU := msgOf(t, cmd).(UnknownLeaderMsg); !ok || !isU || m.Key != "x" {
		t.Fatal("expected unknown leader")
	}
	// doubled leader → literal 0x01
	s.HandleKey(ctrlA)
	s.HandleKey(ctrlA)
	// bare escape and ctrl+z forwarded
	s.HandleKey(tea.KeyMsg{Type: tea.KeyEsc})
	s.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlZ})
	s.HandleKey(runes("q"))
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	got := fc.streams["w1"].inputs()
	want := []string{"\x01", "\x1b", "\x1a", "q"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("inputs %q want %q", got, want)
	}
	if ok, _ := s.HandleKey(runes("a")); ok {
		t.Fatal("stopped session consumed key")
	}
}

func TestStopReleasesBeforeCloseAndSwitchOrder(t *testing.T) {
	fc := newFake()
	s, err := Start(context.Background(), fc, target("w1"), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	s2, err := s.Switch(context.Background(), target("w2"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Stop()
	got := strings.Join(fc.log.all(), "\n")
	want := strings.Join([]string{"open w1 80x24", "lease w1 100x30", "lease w1 0x0", "close w1", "open w2 100x30"}, "\n")
	if got != want {
		t.Fatalf("order:\n%s\nwant:\n%s", got, want)
	}
	if err := s.Resize(10, 10); err == nil {
		t.Fatal("resize after stop leased")
	}
	for _, e := range fc.log.all() {
		if strings.HasPrefix(e, "call ") {
			t.Fatalf("unexpected verb %s", e)
		}
	}
}

func TestRenewLease(t *testing.T) {
	fc := newFake()
	s, err := Start(context.Background(), fc, target("w1"), 40, 10, WithRenewEvery(10*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		n := 0
		for _, e := range fc.log.all() {
			if e == "lease w1 40x10" {
				n++
			}
		}
		if n >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.Stop()
	ev := fc.log.all()
	if ev[len(ev)-2] != "lease w1 0x0" || ev[len(ev)-1] != "close w1" {
		t.Fatalf("tail %q", ev)
	}
	for _, e := range ev[:len(ev)-2] {
		if e == "lease w1 0x0" {
			t.Fatal("early release")
		}
	}
}

func TestFramesRenderAndExit(t *testing.T) {
	fc := newFake()
	s, err := Start(context.Background(), fc, target("w1"), 20, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	st := fc.streams["w1"]
	st.frames <- tuios.Frame{Type: 'S', Cols: 10, Rows: 2, Data: []byte("\x1b]0;title\x07\x1b[32mhi\x1b[0m\r\nthere")}
	st.frames <- tuios.Frame{Type: 'O', Data: []byte("!\x1b]52;c;Zm9v\x07")}
	st.frames <- tuios.Frame{Type: 'E', Data: []byte(`{"code":"x"}`)}
	st.frames <- tuios.Frame{Type: 'X', Data: []byte("exited 0")}
	cmd := s.Init()
	var got []tea.Msg
	for i := 0; i < 10 && cmd != nil; i++ {
		m := cmd()
		if b, ok := m.(tea.BatchMsg); ok {
			got = append(got, b[0]())
			cmd = b[1]
			continue
		}
		if fm, ok := m.(frameMsg); ok {
			cmd = s.Update(fm)
			continue
		}
		got = append(got, m)
		cmd = nil
	}
	if c, r := s.Size(); c != 10 || r != 2 {
		t.Fatalf("size %dx%d", c, r)
	}
	v := s.View(5, 2)
	if !strings.Contains(v, "hi") || !strings.Contains(v, "there") || strings.Contains(v, "\x1b]") {
		t.Fatalf("view %q", v)
	}
	if len(got) < 2 {
		t.Fatalf("msgs %#v", got)
	}
	if _, ok := got[0].(ErrorMsg); !ok {
		t.Fatalf("want ErrorMsg got %#v", got[0])
	}
	if x, ok := got[1].(ExitMsg); !ok || x.Reason != "exited 0" {
		t.Fatalf("want ExitMsg got %#v", got[1])
	}
}

func TestKeyBytes(t *testing.T) {
	cases := []struct {
		k    tea.KeyMsg
		want string
	}{
		{runes("a"), "a"},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x"), Alt: true}, "\x1bx"},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi\n"), Paste: true}, "\x1b[200~hi\n\x1b[201~"},
		{tea.KeyMsg{Type: tea.KeyEnter}, "\r"},
		{tea.KeyMsg{Type: tea.KeyTab}, "\t"},
		{tea.KeyMsg{Type: tea.KeyBackspace}, "\x7f"},
		{tea.KeyMsg{Type: tea.KeyEsc}, "\x1b"},
		{tea.KeyMsg{Type: tea.KeyCtrlC}, "\x03"},
		{tea.KeyMsg{Type: tea.KeyCtrlZ}, "\x1a"},
		{tea.KeyMsg{Type: tea.KeySpace}, " "},
		{tea.KeyMsg{Type: tea.KeyUp}, "\x1b[A"},
		{tea.KeyMsg{Type: tea.KeyLeft, Alt: true}, "\x1b\x1b[D"},
		{tea.KeyMsg{Type: tea.KeyCtrlRight}, "\x1b[1;5C"},
		{tea.KeyMsg{Type: tea.KeyShiftTab}, "\x1b[Z"},
		{tea.KeyMsg{Type: tea.KeyF1}, "\x1bOP"},
		{tea.KeyMsg{Type: tea.KeyF12}, "\x1b[24~"},
		{tea.KeyMsg{Type: tea.KeyPgDown}, "\x1b[6~"},
		{tea.KeyMsg{Type: tea.KeyDelete}, "\x1b[3~"},
	}
	for _, c := range cases {
		if got := string(KeyBytes(c.k)); got != c.want {
			t.Errorf("%s: got %q want %q", c.k, got, c.want)
		}
	}
}
