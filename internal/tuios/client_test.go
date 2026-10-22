package tuios_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unixsuperhero/gmtm/internal/tuios"
	"github.com/unixsuperhero/gmtm/internal/tuios/tuiostest"
)

func newClient(t *testing.T, d *tuiostest.Daemon) tuios.Client {
	t.Helper()
	c, err := tuios.NewWithOptions("cmt-unit", tuios.Options{SocketPath: d.Path})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func tctx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestCallResultAndHelloOncePerConnection(t *testing.T) {
	d := tuiostest.Start(t)
	d.Handle("list-sessions", tuiostest.Result(map[string]any{"type": "session_list", "sessions": []any{
		map[string]any{"name": "web", "id": "s1", "window_count": 2, "extra": "kept"},
	}}))
	c := newClient(t, d)
	ctx := tctx(t)
	for i := 0; i < 3; i++ {
		ss, err := tuios.ListSessions(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 || ss[0].Name != "web" || ss[0].WindowCount != 2 || ss[0].Raw["extra"] != "kept" {
			t.Fatalf("%+v", ss)
		}
	}
	if n := d.Count("hello"); n != 1 {
		t.Fatalf("hello sent %d times for one pooled connection", n)
	}
	reqs := d.Requests()
	var hp map[string]any
	_ = json.Unmarshal(reqs[0].Params, &hp)
	if hp["client"] != "mtm" {
		t.Fatalf("hello params %v", hp)
	}
}

func TestCallDaemonError(t *testing.T) {
	d := tuiostest.Start(t)
	d.Handle("new-session", tuiostest.Fail("session_exists", "session p already exists"))
	c := newClient(t, d)
	_, err := tuios.NewSession(tctx(t), c, "p", false, "")
	var de *tuios.Error
	if !errors.As(err, &de) || de.Code != "session_exists" || !tuios.IsCode(err, tuios.CodeSessionExists) {
		t.Fatalf("err %v", err)
	}
	// connection stays usable after a daemon error
	d.Handle("list-hosts", tuiostest.Result(map[string]any{"hosts": []any{map[string]any{"name": "build", "status": "up"}}}))
	hs, err := tuios.ListHosts(tctx(t), c)
	if err != nil || len(hs) != 1 || hs[0].Name != "build" || hs[0].Status != "up" {
		t.Fatalf("%v %v", hs, err)
	}
	if d.Count("hello") != 1 {
		t.Fatal("daemon error dropped the connection")
	}
}

func TestCallParamsOnWire(t *testing.T) {
	d := tuiostest.Start(t)
	d.Handle("new-window", tuiostest.Result(map[string]any{"window_id": "w1", "type": "window_created"}))
	c := newClient(t, d)
	w, err := tuios.NewWindow(tctx(t), c, tuios.NewWindowParams{Session: "web", Name: "build", Command: []string{"nvim"}})
	if err != nil || w.WindowID != "w1" {
		t.Fatal(w, err)
	}
	reqs := d.Requests()
	last := reqs[len(reqs)-1]
	var p map[string]any
	_ = json.Unmarshal(last.Params, &p)
	if p["focus"] != false || p["session"] != "web" || p["name"] != "build" {
		t.Fatalf("params %s", last.Params)
	}
	if _, ok := p["workspace"]; ok {
		t.Fatalf("zero workspace sent: %s", last.Params)
	}
}

func TestCallRespectsDeadline(t *testing.T) {
	d := tuiostest.Start(t)
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	d.Handle("slow", func(json.RawMessage) (any, *tuios.Error) { <-block; return nil, nil })
	c := newClient(t, d)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := c.Call(ctx, "slow", nil, nil)
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline error, got %v", err)
	}
}

func TestCallRetriesClosedPooledConn(t *testing.T) {
	d := tuiostest.Start(t)
	d.Handle("list-sessions", tuiostest.Result(map[string]any{"sessions": []any{}}))
	c := newClient(t, d)
	if _, err := tuios.ListSessions(tctx(t), c); err != nil {
		t.Fatal(err)
	}
	d.DropAll()
	time.Sleep(50 * time.Millisecond)
	if _, err := tuios.ListSessions(tctx(t), c); err != nil {
		t.Fatalf("stale pooled conn not retried: %v", err)
	}
}

func TestUnreachable(t *testing.T) {
	c, _ := tuios.NewWithOptions("cmt-none", tuios.Options{SocketPath: "/tmp/does-not-exist-mtm.sock"})
	if err := c.Ping(tctx(t)); err == nil || !strings.Contains(err.Error(), "cmt-none") {
		t.Fatalf("err %v", err)
	}
}

func TestSubscribe(t *testing.T) {
	d := tuiostest.Start(t)
	c := newClient(t, d)
	ctx, cancel := context.WithCancel(tctx(t))
	ch, err := c.Subscribe(ctx, []string{"window-created"}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	first := <-ch
	if first.Type != "subscribed" || first.BootID != "fakeboot" {
		t.Fatalf("first %+v", first)
	}
	d.Emit(map[string]any{"type": "window-created", "session": "web", "window": "w9", "title": "x"})
	select {
	case ev := <-ch:
		if ev.Type != "window-created" || ev.Session != "web" || ev.Window != "w9" || !bytes.Contains(ev.Raw, []byte(`"title":"x"`)) {
			t.Fatalf("ev %+v raw %s", ev, ev.Raw)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event")
	}
	reqs := d.Requests()
	var sp map[string]any
	for _, r := range reqs {
		if r.Verb == "subscribe" {
			_ = json.Unmarshal(r.Params, &sp)
		}
	}
	if _, ok := sp["after_seq"]; ok {
		t.Fatalf("after_seq sent for live subscribe: %v", sp)
	}
	cancel()
	select {
	case _, ok := <-ch:
		for ok {
			_, ok = <-ch
		}
	case <-time.After(3 * time.Second):
		t.Fatal("channel not closed on cancel")
	}
}

func TestSubscribeResumeParamsAndDisconnect(t *testing.T) {
	d := tuiostest.Start(t)
	c := newClient(t, d)
	ch, err := c.Subscribe(tctx(t), nil, 41, "b1")
	if err != nil {
		t.Fatal(err)
	}
	<-ch
	var sp map[string]any
	for _, r := range d.Requests() {
		if r.Verb == "subscribe" {
			_ = json.Unmarshal(r.Params, &sp)
		}
	}
	if sp["after_seq"] != float64(41) || sp["boot_id"] != "b1" {
		t.Fatalf("params %v", sp)
	}
	d.DropSubscribers()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("unexpected event")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect not reported by channel close")
	}
}

func TestStreamPaneFrames(t *testing.T) {
	d := tuiostest.Start(t)
	type got struct {
		typ byte
		p   []byte
	}
	recv := make(chan got, 8)
	d.OnStream(func(_ json.RawMessage, sc *tuiostest.StreamConn) {
		_ = sc.Snapshot(5, 80, 24, []byte("hello"))
		_ = sc.Output(9, []byte("abc"))
		var rp [12]byte
		binary.BigEndian.PutUint64(rp[0:8], 10)
		binary.BigEndian.PutUint16(rp[8:10], 100)
		binary.BigEndian.PutUint16(rp[10:12], 30)
		_ = sc.WriteFrame('R', rp[:])
		_ = sc.WriteFrame('Z', []byte("unknown"))
		for i := 0; i < 2; i++ {
			typ, p, err := sc.ReadFrame()
			if err != nil {
				return
			}
			recv <- got{typ, p}
		}
		_ = sc.WriteFrame('X', []byte("exited 0"))
		sc.Close()
	})
	c := newClient(t, d)
	ctx := tctx(t)
	s, err := c.StreamPane(ctx, "web", "w1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var sp map[string]any
	for _, r := range d.Requests() {
		if r.Verb == "stream-pane" {
			_ = json.Unmarshal(r.Params, &sp)
		}
	}
	if _, ok := sp["lease_cols"]; ok {
		t.Fatalf("lease sent for 0,0: %v", sp)
	}
	if ps, ok := s.(*tuios.PaneStream); !ok || ps.Info().BootID != "fakeboot" {
		t.Fatalf("info %+v", s)
	}
	f := <-s.Frames()
	if f.Type != 'S' || f.Seq != 5 || f.Cols != 80 || f.Rows != 24 || string(f.Data) != "hello" {
		t.Fatalf("S %+v", f)
	}
	f = <-s.Frames()
	if f.Type != 'O' || f.Seq != 9 || string(f.Data) != "abc" {
		t.Fatalf("O %+v", f)
	}
	f = <-s.Frames()
	if f.Type != 'R' || f.Seq != 10 || f.Cols != 100 || f.Rows != 30 {
		t.Fatalf("R %+v", f)
	}
	f = <-s.Frames()
	if f.Type != 'Z' || string(f.Data) != "unknown" {
		t.Fatalf("unknown %+v", f)
	}
	if err := s.SendInput(ctx, []byte("ls\n")); err != nil {
		t.Fatal(err)
	}
	if err := s.Lease(ctx, 120, 40); err != nil {
		t.Fatal(err)
	}
	in := <-recv
	if in.typ != 'I' || string(in.p) != "ls\n" {
		t.Fatalf("I %+v", in)
	}
	l := <-recv
	if l.typ != 'L' || binary.BigEndian.Uint16(l.p[0:2]) != 120 || binary.BigEndian.Uint16(l.p[2:4]) != 40 {
		t.Fatalf("L %+v", l)
	}
	f = <-s.Frames()
	if f.Type != 'X' || string(f.Data) != "exited 0" {
		t.Fatalf("X %+v", f)
	}
	if _, ok := <-s.Frames(); ok {
		t.Fatal("frames not closed after exit")
	}
}

func TestStreamPaneLeaseParamsAndInputSplit(t *testing.T) {
	d := tuiostest.Start(t)
	var mu sync.Mutex
	var sizes []int
	done := make(chan struct{})
	d.OnStream(func(_ json.RawMessage, sc *tuiostest.StreamConn) {
		total := 0
		for total < tuios.MaxInputFrame+10 {
			_, p, err := sc.ReadFrame()
			if err != nil {
				break
			}
			mu.Lock()
			sizes = append(sizes, len(p))
			mu.Unlock()
			total += len(p)
		}
		close(done)
	})
	c := newClient(t, d)
	s, err := c.StreamPane(tctx(t), "web", "w1", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SendInput(tctx(t), make([]byte, tuios.MaxInputFrame+10)); err != nil {
		t.Fatal(err)
	}
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(sizes) != 2 || sizes[0] != tuios.MaxInputFrame || sizes[1] != 10 {
		t.Fatalf("sizes %v", sizes)
	}
	var sp map[string]any
	for _, r := range d.Requests() {
		if r.Verb == "stream-pane" {
			_ = json.Unmarshal(r.Params, &sp)
		}
	}
	if sp["lease_cols"] != float64(80) || sp["lease_rows"] != float64(24) {
		t.Fatalf("lease params %v", sp)
	}
}

func TestStreamPaneError(t *testing.T) {
	d := tuiostest.Start(t)
	d.Handle("stream-pane", tuiostest.Fail("window_not_found", "nope"))
	c := newClient(t, d)
	if _, err := c.StreamPane(tctx(t), "web", "zz", 0, 0); !tuios.IsCode(err, tuios.CodeWindowNotFound) {
		t.Fatalf("err %v", err)
	}
}

// StartServer/KillServer exec the binary with a scrubbed env and --server <name>.
func TestStartKillServerExecScrubbedEnv(t *testing.T) {
	d := tuiostest.Start(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	script := filepath.Join(dir, "fake-tuios")
	body := "#!/bin/sh\n{ echo \"ARGS:$*\"; env; } >> " + out + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	environ := func() []string {
		return []string{"PATH=/usr/bin:/bin", "TUIOS_SOCKET=/evil", "TUIOS_SESSION=s", "TUIOS_PANE_ID=p", "TUIOS_WINDOW_ID=w", "TUIOS_PANE_TOKEN=t", "TUIOS_PANE_GRANTS=g", "TUIOS_SERVER=default"}
	}
	c, err := tuios.NewWithOptions("cmt-unit", tuios.Options{SocketPath: d.Path, Binary: script, Environ: environ})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.StartServer(tctx(t)); err != nil {
		t.Fatal(err)
	}
	if err := c.KillServer(tctx(t)); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	s := string(b)
	for _, want := range []string{"ARGS:--server cmt-unit start-server", "ARGS:--server cmt-unit kill-server", "TUIOS_SERVER=cmt-unit"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	for _, bad := range []string{"TUIOS_SOCKET", "TUIOS_SESSION", "TUIOS_PANE_ID", "TUIOS_WINDOW_ID", "TUIOS_PANE_TOKEN", "TUIOS_PANE_GRANTS", "TUIOS_SERVER=default"} {
		if strings.Contains(s, bad) {
			t.Errorf("leaked %q", bad)
		}
	}
	env := c.ChildEnv()
	if env[len(env)-1] != "TUIOS_SERVER=cmt-unit" {
		t.Fatalf("child env %v", env)
	}
}

func TestStartServerFailureReportsOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-tuios")
	_ = os.WriteFile(script, []byte("#!/bin/sh\necho boom >&2\nexit 3\n"), 0o755)
	c, _ := tuios.NewWithOptions("cmt-unit", tuios.Options{SocketPath: "/tmp/none-mtm.sock", Binary: script})
	err := c.StartServer(tctx(t))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err %v", err)
	}
}
