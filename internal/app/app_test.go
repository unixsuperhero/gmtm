package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/actions"
	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/inventory"
	"github.com/unixsuperhero/gmtm/internal/launch"
	"github.com/unixsuperhero/gmtm/internal/live"
	"github.com/unixsuperhero/gmtm/internal/store"
	"github.com/unixsuperhero/gmtm/internal/tuios"
	"github.com/unixsuperhero/gmtm/internal/tuios/tuiostest"
	"github.com/unixsuperhero/gmtm/internal/ui/forms"
	"github.com/unixsuperhero/gmtm/internal/ui/palette"
	"github.com/unixsuperhero/gmtm/internal/ui/pathpicker"
)

// ---- fake daemon state ----

type fakeTUIOS struct {
	mu       sync.Mutex
	sessions []string
	windows  map[string][]map[string]any
	n        int
}

func (f *fakeTUIOS) add(session, id, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !containsStr(f.sessions, session) {
		f.sessions = append(f.sessions, session)
	}
	f.windows[session] = append(f.windows[session], map[string]any{"window_id": id, "custom_name": name, "agent_state": "none", "cwd": "/src"})
}

func (f *fakeTUIOS) install(d *tuiostest.Daemon) {
	d.Handle("list-sessions", func(json.RawMessage) (any, *tuios.Error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		ss := []any{}
		for _, s := range f.sessions {
			ss = append(ss, map[string]any{"name": s, "id": "id-" + s, "window_count": len(f.windows[s])})
		}
		return map[string]any{"sessions": ss}, nil
	})
	d.Handle("list-windows", func(p json.RawMessage) (any, *tuios.Error) {
		var q struct{ Session string }
		_ = json.Unmarshal(p, &q)
		f.mu.Lock()
		defer f.mu.Unlock()
		return map[string]any{"windows": f.windows[q.Session]}, nil
	})
	d.Handle("list-agents", tuiostest.Result(map[string]any{"agents": []any{}}))
	d.Handle("list-hosts", tuiostest.Result(map[string]any{"hosts": []any{}}))
	d.Handle("capture-pane", tuiostest.Result(map[string]any{"content": "$ echo hi\nhi\n"}))
	d.Handle("close-window", tuiostest.Result(map[string]any{}))
	d.Handle("new-session", func(p json.RawMessage) (any, *tuios.Error) {
		var q struct{ Name string }
		_ = json.Unmarshal(p, &q)
		f.mu.Lock()
		defer f.mu.Unlock()
		if containsStr(f.sessions, q.Name) {
			return nil, &tuios.Error{Code: "session_exists", Message: "exists"}
		}
		f.sessions = append(f.sessions, q.Name)
		return map[string]any{"session": q.Name}, nil
	})
	d.Handle("new-window", func(p json.RawMessage) (any, *tuios.Error) {
		var q struct {
			Session string
			Name    string
		}
		_ = json.Unmarshal(p, &q)
		f.mu.Lock()
		f.n++
		id := fmt.Sprintf("new%d", f.n)
		f.mu.Unlock()
		f.add(q.Session, id, q.Name)
		return map[string]any{"window_id": id, "name": q.Name}, nil
	})
}

// ---- client with a fake stream-pane (records lease/input/close order) ----

type evlog struct {
	mu sync.Mutex
	ev []string
}

func (l *evlog) add(f string, a ...any) {
	l.mu.Lock()
	l.ev = append(l.ev, fmt.Sprintf(f, a...))
	l.mu.Unlock()
}
func (l *evlog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ev...)
}

type fstream struct {
	win    string
	log    *evlog
	frames chan tuios.Frame
}

func (s *fstream) Frames() <-chan tuios.Frame { return s.frames }
func (s *fstream) SendInput(_ context.Context, b []byte) error {
	s.log.add("input %s %q", s.win, b)
	return nil
}
func (s *fstream) Lease(_ context.Context, c, r uint16) error {
	s.log.add("lease %s %d %d", s.win, c, r)
	return nil
}
func (s *fstream) Close() error { s.log.add("close %s", s.win); return nil }

type tclient struct {
	tuios.Client
	log *evlog
}

func (c tclient) StreamPane(_ context.Context, session, window string, lc, lr uint16) (tuios.Stream, error) {
	c.log.add("stream %s %d %d", window, lc, lr)
	return &fstream{win: window, log: c.log, frames: make(chan tuios.Frame)}, nil
}

// ---- harness ----

type harness struct {
	t   *testing.T
	a   *App
	d   *tuiostest.Daemon
	f   *fakeTUIOS
	log *evlog
	st  store.Store
}

func newHarness(t *testing.T, windows ...[3]string) *harness {
	t.Helper()
	d := tuiostest.Start(t)
	f := &fakeTUIOS{windows: map[string][]map[string]any{}}
	for _, w := range windows {
		f.add(w[0], w[1], w[2])
	}
	f.install(d)
	base, err := tuios.NewWithOptions("clean-mtm", tuios.Options{SocketPath: d.Path, Environ: func() []string { return []string{"TUIOS_SOCKET=/user/daemon"} }})
	if err != nil {
		t.Fatal(err)
	}
	lg := &evlog{}
	c := tclient{Client: base, log: lg}
	st, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	inv := inventory.New(c)
	l := launch.New(c, inv, st)
	l.LookPath = func(string) (string, error) { return "", fmt.Errorf("absent") }
	a := New(Deps{Client: c, Inv: inv, Store: st, Launch: l, Registry: actions.NewDefault()})
	h := &harness{t: t, a: a, d: d, f: f, log: lg, st: st}
	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	h.run(a.Init())
	return h
}

// run executes cmd and feeds its messages back, bounded in time.
func (h *harness) run(cmd tea.Cmd) { h.drain(cmd, 0) }

func (h *harness) drain(cmd tea.Cmd, depth int) {
	if cmd == nil || depth > 12 {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		switch m := msg.(type) {
		case nil:
		case tea.BatchMsg:
			for _, c := range m {
				h.drain(c, depth+1)
			}
		default:
			if fmt.Sprintf("%T", msg) == "tea.sequenceMsg" {
				return
			}
			_, c := h.a.Update(msg)
			h.drain(c, depth+1)
		}
	case <-time.After(250 * time.Millisecond):
	}
}

func (h *harness) send(msg tea.Msg) {
	_, c := h.a.Update(msg)
	h.run(c)
}

func (h *harness) keys(ks ...string) {
	for _, k := range ks {
		h.send(keyMsg(k))
	}
}

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "f4":
		return tea.KeyMsg{Type: tea.KeyF4}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "ctrl+a":
		return tea.KeyMsg{Type: tea.KeyCtrlA}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

func (h *harness) requests(verb string) []string {
	var out []string
	for _, r := range h.d.Requests() {
		if r.Verb == verb {
			out = append(out, string(r.Params))
		}
	}
	return out
}

func (h *harness) assertFrame(mode string) {
	h.t.Helper()
	v := h.a.View()
	for _, want := range []string{"cmtm", strings.ToUpper(mode), h.a.projectText()} {
		if !strings.Contains(v, want) {
			h.t.Fatalf("frame in %s mode lacks %q:\n%s", mode, want, v)
		}
	}
	if got := h.a.modeText(); got != mode {
		h.t.Fatalf("mode = %q, want %q", got, mode)
	}
}

func (h *harness) enterLive() {
	h.t.Helper()
	h.keys("1", "i")
	if h.a.live == nil {
		h.t.Fatalf("not live; status %q", h.a.status)
	}
}

// ---- tests ----

func TestFrameIndicatorsInEveryMode(t *testing.T) {
	h := newHarness(t, [3]string{"web", "w1", "api"})
	h.assertFrame("normal")
	if h.a.projectText() != domain.SessionNoProject {
		t.Fatalf("initial project %q", h.a.projectText())
	}
	h.keys("/")
	h.assertFrame("insert")
	h.keys("esc", ":")
	h.assertFrame("palette")
	h.keys("esc")
	h.keys("?")
	h.assertFrame("help")
	h.keys("q")
	h.keys("f4")
	h.assertFrame("note")
	h.keys("esc", "esc")
	h.keys("N", "t")
	h.assertFrame("normal") // form in normal mode
	h.keys("esc")
	h.keys("J")
	h.assertFrame("jump")
	h.keys("esc")
	h.enterLive()
	h.assertFrame("live")
}

func TestF4FromEveryStateNeverReachesLive(t *testing.T) {
	h := newHarness(t, [3]string{"web", "w1", "api"})
	open := func(state string) {
		t.Helper()
		h.keys("f4")
		if _, ok := h.a.note(); !ok {
			t.Fatalf("F4 from %s did not open note", state)
		}
		if top, _ := h.a.modals.Top(); top.ID != "note" {
			t.Fatalf("note not on top from %s", state)
		}
		h.keys("f4") // repeat focuses, never nests
		n := 0
		for _, l := range h.a.modals.Layers() {
			if l.ID == "note" {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("%d note layers", n)
		}
		h.a.removeLayer("note")
	}
	open("normal")
	h.keys("/")
	open("insert")
	h.keys("esc", ":")
	open("palette")
	h.a.removeLayer("palette")
	h.keys("?")
	open("help")
	h.a.removeLayer("help")
	h.keys("x") // confirm modal
	open("modal")
	h.a.removeLayer("confirm:close")
	h.enterLive()
	h.keys("f4", "h", "i")
	if _, ok := h.a.note(); !ok {
		t.Fatal("F4 in live did not open note")
	}
	for _, e := range h.log.all() {
		if strings.HasPrefix(e, "input") {
			t.Fatalf("key leaked to live target: %s", e)
		}
	}
	if n, _ := h.a.note(); n.Text() != "hi" {
		t.Fatalf("note text %q", n.Text())
	}
}

func TestRefreshPreservesSelection(t *testing.T) {
	h := newHarness(t, [3]string{"web", "w1", "a"}, [3]string{"web", "w2", "b"})
	h.keys("j")
	sel := h.a.panes().lst.SelectedID()
	if !strings.HasSuffix(sel, "|w2") {
		t.Fatalf("selected %q", sel)
	}
	h.keys("/", "b", "esc") // query must survive refresh too
	h.f.add("web", "w0", "zzz")
	h.run(h.a.refreshCmd())
	if got := h.a.panes().lst.SelectedID(); got != sel {
		t.Fatalf("selection %q → %q after refresh", sel, got)
	}
	if h.a.panes().lst.Query() != "b" {
		t.Fatal("query lost on refresh")
	}
}

func TestLiveSwitchStopsBeforeStart(t *testing.T) {
	h := newHarness(t, [3]string{"web", "w1", "a"}, [3]string{"web", "w2", "b"})
	h.enterLive()
	cols, rows := h.a.liveSize()
	iw, ih := h.a.frame().Inner()
	if cols > iw || rows >= ih {
		t.Fatalf("live size %dx%d not within detail area of %dx%d", cols, rows, iw, ih)
	}
	h.send(live.SwitchMsg{Delta: 1})
	ev := strings.Join(h.log.all(), "\n")
	iRel, iClose, iNext := strings.Index(ev, "lease w1 0 0"), strings.Index(ev, "close w1"), strings.Index(ev, "stream w2")
	if iRel < 0 || iClose < 0 || iNext < 0 || !(iRel < iClose && iClose < iNext) {
		t.Fatalf("switch order wrong:\n%s", ev)
	}
	if h.a.live == nil || h.a.live.Target().WindowID != "w2" {
		t.Fatal("not live on w2")
	}
	h.keys("ctrl+a", "d")
	if h.a.live != nil || h.a.modeText() != "normal" {
		t.Fatal("detach did not return to normal")
	}
}

func TestQuickCreateUsesNoProjectSession(t *testing.T) {
	h := newHarness(t)
	h.keys("n", "t")
	nw := h.requests("new-window")
	if len(nw) != 1 || !strings.Contains(nw[0], `"session":"no-project"`) || !strings.Contains(nw[0], `"focus":false`) {
		t.Fatalf("new-window: %v (status %q)", nw, h.a.status)
	}
	if ns := h.requests("new-session"); len(ns) == 0 || !strings.Contains(ns[0], "no-project") {
		t.Fatalf("new-session: %v", ns)
	}
	if ps, _ := h.st.Projects(context.Background()); len(ps) != 0 {
		t.Fatalf("fake project created: %v", ps)
	}
}

func TestCloseNeedsConfirm(t *testing.T) {
	h := newHarness(t, [3]string{"web", "w1", "a"})
	h.keys("x")
	if len(h.requests("close-window")) != 0 {
		t.Fatal("closed without confirmation")
	}
	h.keys("n")
	if len(h.requests("close-window")) != 0 {
		t.Fatal("closed after no")
	}
	h.keys("x", "y")
	if cw := h.requests("close-window"); len(cw) != 1 || !strings.Contains(cw[0], `"window":"w1"`) {
		t.Fatalf("close-window: %v", cw)
	}
}

func TestPaletteRunsSameActionAsKey(t *testing.T) {
	h := newHarness(t, [3]string{"web", "w1", "a"}, [3]string{"web", "w2", "b"})
	id := h.a.panes().lst.SelectedID()
	h.keys("s")
	stars, _ := h.st.Stars(context.Background())
	if !stars[id] {
		t.Fatal("key s did not star")
	}
	h.keys(":")
	h.send(palette.ExecuteMsg{ActionID: "star", TargetID: id})
	stars, _ = h.st.Stars(context.Background())
	if stars[id] {
		t.Fatal("palette star did not toggle the same item")
	}
	if !h.a.modals.Empty() {
		t.Fatal("palette still open")
	}
}

func TestNoteSavesWithActiveProject(t *testing.T) {
	h := newHarness(t)
	h.keys("f4", "h", "e", "y", "ctrl+s")
	items, _ := h.st.AllContextItems(context.Background())
	if len(items) != 1 || items[0].Body != "hey" || items[0].Project != "" || items[0].Kind != "note" {
		t.Fatalf("items %+v (status %q)", items, h.a.status)
	}
	if _, ok := h.a.note(); ok {
		t.Fatal("note still open after save")
	}
}

func TestPathPickerRoundTrip(t *testing.T) {
	h := newHarness(t)
	h.keys("N", "t")
	top, _ := h.a.modals.Top()
	f, ok := formOf(top)
	if !ok {
		t.Fatal("no form")
	}
	dir := t.TempDir()
	h.send(forms.PickRequestMsg{FormID: f.ID, FieldID: "directory", Gen: f.Gen("directory"), Mode: forms.PickDir, Base: dir, Title: "Directory"})
	if top, _ := h.a.modals.Top(); top.ID != "picker" || h.a.modeText() != "insert" {
		t.Fatalf("picker not on top: %q", top.ID)
	}
	h.send(pathpicker.ChosenMsg{ID: f.ID + "/directory", Paths: []string{dir}})
	if top, _ := h.a.modals.Top(); top.ID != "form:"+f.ID {
		t.Fatalf("form not restored: %q", top.ID)
	}
	if f.Text("directory") != dir {
		t.Fatalf("directory = %q", f.Text("directory"))
	}
}
