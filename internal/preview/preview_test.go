package preview

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/tuios"
)

type fakeClient struct {
	verbs   []string
	replies map[string]any
	errs    map[string]error
}

func (f *fakeClient) Server() string     { return "clean-mtm" }
func (f *fakeClient) SocketPath() string { return "/nope" }
func (f *fakeClient) Call(ctx context.Context, verb string, params any, out any) error {
	f.verbs = append(f.verbs, verb)
	if err := f.errs[verb]; err != nil {
		return err
	}
	b, _ := json.Marshal(f.replies[verb])
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}
func (f *fakeClient) Subscribe(context.Context, []string, int64, string) (<-chan tuios.Event, error) {
	panic("preview must not subscribe")
}
func (f *fakeClient) StreamPane(context.Context, string, string, uint16, uint16) (tuios.Stream, error) {
	panic("preview must not stream/lease")
}
func (f *fakeClient) Ping(context.Context) error        { return nil }
func (f *fakeClient) StartServer(context.Context) error { return nil }
func (f *fakeClient) KillServer(context.Context) error  { return nil }
func (f *fakeClient) ChildEnv() []string                { return nil }

var tgt = domain.Target{Server: "clean-mtm", Host: "", Session: "work", WindowID: "w1"}

func TestSanitizeStripsControlsKeepsSGR(t *testing.T) {
	in := "\x1b[31mred\x1b[0m" +
		"\x1b]0;evil title\x07" +
		"\x1b]2;t2\x1b\\" +
		"\x1b]52;c;aGVsbG8=\x07" +
		"\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\" +
		"\x1bP+q544e\x1b\\" +
		"\x1b[6n\x1b[>4;1m\x1b[2J\x1b[H" +
		"\xc2\x9d0;c1title\x07" +
		" é\x07\x08 \x1b[1;38;2;1;2;3mx"
	got := string(Sanitize([]byte(in)))
	want := "\x1b[31mred\x1b[0mlink é \x1b[1;38;2;1;2;3mx"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestSanitizerStreamingSplit(t *testing.T) {
	s := NewSanitizer(false)
	var out []byte
	for _, c := range []byte("a\x1b]52;c;Zm9v\x07b\x1b[3") {
		out = append(out, s.Write([]byte{c})...)
	}
	out = append(out, s.Write([]byte("2mc"))...)
	if string(out) != "ab\x1b[32mc" {
		t.Fatalf("got %q", out)
	}
}

func TestSanitizerControlModeKeepsCursor(t *testing.T) {
	got := string(NewSanitizer(true).Write([]byte("\x1b[2J\x1b[Hhi\r\n\x1b]0;x\x07\x1b7")))
	if got != "\x1b[2J\x1b[Hhi\r\n\x1b7" {
		t.Fatalf("got %q", got)
	}
}

func TestCaptureRendersAndReadOnly(t *testing.T) {
	fc := &fakeClient{replies: map[string]any{
		"get-window":   map[string]any{"width": 20, "height": 3},
		"capture-pane": map[string]any{"content": "\x1b[31mhello\x1b[0m\x1b]0;T\x07\n\x1b]8;;http://x\x07world\x1b]8;;\x07\n", "history_rows": 0},
	}}
	res := New(fc).CaptureSync(context.Background(), tgt, 7, Opts{Styled: true})
	if res.Err != nil || res.State != StateCurrent || res.Gen != 7 {
		t.Fatalf("res %+v", res)
	}
	if res.Text != "hello\nworld" {
		t.Fatalf("text %q", res.Text)
	}
	if len(res.Rendered) != 2 || !strings.Contains(res.Rendered[0], "hello") || !strings.Contains(res.Rendered[0], "\x1b[") {
		t.Fatalf("rendered %q", res.Rendered)
	}
	for _, l := range res.Rendered {
		if strings.Contains(l, "\x1b]") {
			t.Fatalf("OSC leaked: %q", l)
		}
	}
	for _, v := range fc.verbs {
		if v != "get-window" && v != "capture-pane" {
			t.Fatalf("non-read verb %q", v)
		}
	}
}

func TestCaptureClosedAndUnavailable(t *testing.T) {
	fc := &fakeClient{errs: map[string]error{"get-window": &tuios.Error{Code: "window_not_found", Message: "gone"}}}
	if r := New(fc).CaptureSync(context.Background(), tgt, 1, Opts{}); r.State != StateClosed {
		t.Fatalf("state %s", r.State)
	}
	fc = &fakeClient{errs: map[string]error{"get-window": errors.New("dial: refused")}}
	if r := New(fc).CaptureSync(context.Background(), tgt, 1, Opts{}); r.State != StateUnavailable {
		t.Fatalf("state %s", r.State)
	}
}

func TestTrackerRejectsStale(t *testing.T) {
	var tr Tracker
	g1 := tr.Begin(tgt)
	other := tgt
	other.WindowID = "w2"
	g2 := tr.Begin(other)
	if tr.Accept(ResultMsg{Target: tgt, Gen: g1}) {
		t.Fatal("accepted stale gen")
	}
	if tr.Accept(ResultMsg{Target: tgt, Gen: g2}) {
		t.Fatal("accepted foreign target with current gen")
	}
	if !tr.Accept(ResultMsg{Target: other, Gen: g2}) {
		t.Fatal("rejected current")
	}
}

func TestCacheAndLabel(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now = func() time.Time { return base.Add(3 * time.Second) }
	defer func() { now = time.Now }()
	c := NewCache()
	good := ResultMsg{Target: tgt, Gen: 1, Text: "x", State: StateCurrent, TakenAt: base, Truncated: true}
	c.Resolve(good)
	bad := ResultMsg{Target: tgt, Gen: 2, State: StateUnavailable, Err: errors.New("timeout\nboom"), TakenAt: now()}
	r := c.Resolve(bad)
	if r.State != StateCached || r.Text != "x" || r.Gen != 2 {
		t.Fatalf("resolve %+v", r)
	}
	other := bad
	other.Target.WindowID = "w9"
	if r := c.Resolve(other); r.State != StateUnavailable || r.Text != "" {
		t.Fatalf("other target got cached output: %+v", r)
	}
	h := Labeled(bad, &good)
	want := "clean-mtm/local/work/w1 • captured 3s ago • cached • truncated • error: timeout boom"
	if h != want {
		t.Fatalf("label %q", h)
	}
	if got := FitHeader(h, 10); len([]rune(got)) > 10 {
		t.Fatalf("fit %q", got)
	}
}

func TestCrop(t *testing.T) {
	lines := []string{"one", "\x1b[31mtwotwotwo\x1b[0m", "three"}
	got := Crop(lines, 4, 2)
	if len(got) != 2 || got[1] != "thre" || !strings.HasPrefix(got[0], "\x1b[31mtwot") {
		t.Fatalf("crop %q", got)
	}
	if !TooSmall(5, 5) || TooSmall(40, 3) {
		t.Fatal("too small rule")
	}
}
