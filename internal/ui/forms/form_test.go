package forms

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/ui/pathpicker"
)

func key(s string) tea.KeyMsg {
	m := map[string]tea.KeyType{"esc": tea.KeyEsc, "enter": tea.KeyEnter, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
		"ctrl+s": tea.KeyCtrlS, "ctrl+n": tea.KeyCtrlN, "ctrl+p": tea.KeyCtrlP, "ctrl+o": tea.KeyCtrlO, "backspace": tea.KeyBackspace}
	if t, ok := m[s]; ok {
		return tea.KeyMsg{Type: t}
	}
	if s == " " {
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// send feeds keys and returns every message produced by resulting commands.
func send(f *Form, keys ...string) []tea.Msg {
	var out []tea.Msg
	for _, k := range keys {
		out = append(out, run(f.Update(key(k)))...)
	}
	return out
}

func run(c tea.Cmd) []tea.Msg {
	if c == nil {
		return nil
	}
	m := c()
	if b, ok := m.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, run(c)...)
		}
		return out
	}
	if m == nil {
		return nil
	}
	return []tea.Msg{m}
}

func typeText(f *Form, s string) []tea.Msg {
	var out []tea.Msg
	for _, r := range s {
		out = append(out, send(f, string(r))...)
	}
	return out
}

func submit(t *testing.T, f *Form) (SubmitMsg, bool) {
	t.Helper()
	for _, m := range run(f.Submit()) {
		if s, ok := m.(SubmitMsg); ok {
			return s, true
		}
	}
	return SubmitMsg{}, false
}

var noProject = &Option{Value: "no-project", Label: "no-project"}

func mustNew(t *testing.T, fields ...Field) *Form {
	t.Helper()
	f, err := New("t", "Test", fields...)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestExtensibleStagesUnmatchedOnlyOnSubmit(t *testing.T) {
	f := mustNew(t, Field{ID: "tags", Label: "Tags", Kind: Extensible, Multi: true, Options: []Option{{Value: "backend"}, {Value: "api"}}})
	send(f, "i")
	typeText(f, "back")
	send(f, "ctrl+n") // editable suggestion fills "backend"
	if f.Text("tags") != "backend" {
		t.Fatalf("suggestion fill = %q", f.Text("tags"))
	}
	send(f, "enter")
	typeText(f, "xyz")
	if !strings.Contains(f.View(), "+ new: xyz") {
		t.Fatalf("staged value not visible:\n%s", f.View())
	}
	send(f, "enter")
	send(f, "enter") // empty enter never stages
	s, ok := submit(t, f)
	if !ok {
		t.Fatal("submit failed")
	}
	if got := s.Values["tags"].([]string); strings.Join(got, ",") != "backend,xyz" {
		t.Fatalf("values %v", got)
	}
	if got := s.Staged["tags"]; len(got) != 1 || got[0] != "xyz" {
		t.Fatalf("staged %v", got)
	}
}

func TestExtensibleSingleExactNotStagedEmptyNever(t *testing.T) {
	f := mustNew(t, Field{ID: "p", Label: "Project", Kind: Extensible, Options: []Option{{Value: "website"}}})
	s, _ := submit(t, f)
	if len(s.Staged) != 0 || s.Values["p"] != nil {
		t.Fatalf("empty must not stage: %+v", s)
	}
	send(f, "i")
	typeText(f, "Website")
	s, _ = submit(t, f)
	if s.Values["p"] != "website" || len(s.Staged) != 0 {
		t.Fatalf("exact match staged: %+v", s)
	}
	send(f, "ctrl+u")
	for range "Website" {
		send(f, "backspace")
	}
	typeText(f, "web") // prefix match only: still a new value
	s, _ = submit(t, f)
	if s.Staged["p"][0] != "web" {
		t.Fatalf("expected staged web: %+v", s)
	}
}

func TestChoiceClosedRejectsUnmatched(t *testing.T) {
	f := mustNew(t, Field{ID: "h", Label: "Harness", Kind: Choice, Required: true, Options: []Option{{Value: "claude"}, {Value: "codex"}}})
	send(f, "i")
	typeText(f, "gemini")
	if _, ok := submit(t, f); ok {
		t.Fatal("unmatched closed choice submitted")
	}
	if !strings.Contains(f.FieldError("h"), "not one of the allowed") {
		t.Fatalf("err %q", f.FieldError("h"))
	}
	if f.Text("h") != "gemini" {
		t.Fatal("failed submit lost draft")
	}
	if strings.Contains(f.View(), "+ new") {
		t.Fatal("closed choice must never stage")
	}
	send(f, "esc", "l") // normal mode cycling
	s, ok := submit(t, f)
	if !ok || s.Values["h"] != "claude" || len(s.Staged) != 0 {
		t.Fatalf("cycle submit %+v %v", s, ok)
	}
}

func TestRequiredVsExplicitNoValue(t *testing.T) {
	f := mustNew(t, Field{ID: "project", Label: "Project", Kind: Extensible, Required: true, NoValue: noProject, Options: []Option{{Value: "website"}}})
	if _, ok := submit(t, f); ok {
		t.Fatal("unanswered required submitted")
	}
	if !strings.Contains(f.FieldError("project"), "no-project") {
		t.Fatalf("error should offer no-project: %q", f.FieldError("project"))
	}
	send(f, "l") // first cycled option is the explicit no-value choice
	s, ok := submit(t, f)
	if !ok {
		t.Fatal("explicit no-project rejected")
	}
	if v, isNone := s.Values["project"].(None); !isNone || v.Value != "no-project" || len(s.Staged) != 0 {
		t.Fatalf("got %#v staged %v", s.Values["project"], s.Staged)
	}
	if !strings.Contains(f.View(), "explicit choice: no-project") {
		t.Fatal("explicit choice not shown")
	}
}

func TestNoValueSurvivesDefault(t *testing.T) {
	f := mustNew(t, Field{ID: "project", Label: "Project", Kind: Extensible, Required: true, NoValue: noProject, Default: "website", Options: []Option{{Value: "website"}}})
	send(f, "x") // clear default
	send(f, "i")
	typeText(f, "no-project")
	d := f.Draft()
	g := mustNew(t, Field{ID: "project", Label: "Project", Kind: Extensible, Required: true, NoValue: noProject, Default: "website", Options: []Option{{Value: "website"}}})
	g.Restore(d)
	s, _ := submit(t, g)
	if _, ok := s.Values["project"].(None); !ok {
		t.Fatalf("default overwrote explicit no-value: %#v", s.Values)
	}
}

func linkedForm(t *testing.T) *Form {
	return mustNew(t,
		Field{ID: "host", Label: "Host", Kind: Choice, Options: []Option{{Value: "local"}, {Value: "vps2"}}},
		Field{ID: "sess", Label: "Session", Kind: Linked, ParentID: "host", Options: []Option{
			{Value: "a", Label: "alpha", Parent: "local"}, {Value: "b", Label: "beta", Parent: "vps2"}}},
	)
}

func TestLinkedChildFillsParentAndParentClearsChild(t *testing.T) {
	f := linkedForm(t)
	send(f, "2", "i")
	typeText(f, "beta")
	if f.Text("host") != "vps2" {
		t.Fatalf("child did not fill parent: %q", f.Text("host"))
	}
	send(f, "esc", "1", "i")
	for range "vps2" {
		send(f, "backspace")
	}
	typeText(f, "local")
	if f.Text("sess") != "" {
		t.Fatalf("incompatible child kept: %q", f.Text("sess"))
	}
	if n := f.FieldNote("sess"); !strings.Contains(n, "cleared") || !strings.Contains(n, "beta") {
		t.Fatalf("no explanation: %q", n)
	}
	// parent explicitly chosen filters children suggestions
	send(f, "esc", "2", "i")
	send(f, "ctrl+n")
	if f.Text("sess") != "alpha" {
		t.Fatalf("filtered suggestion = %q", f.Text("sess"))
	}
	send(f, "ctrl+n") // only one option in scope
	if f.Text("sess") != "alpha" {
		t.Fatalf("scope leak: %q", f.Text("sess"))
	}
}

func TestStaleCompletionIgnored(t *testing.T) {
	calls := 0
	f := mustNew(t, Field{ID: "cwd", Label: "Dir", Kind: Text, Complete: func(ctx context.Context, q string) ([]Option, error) {
		calls++
		return []Option{{Value: q + "-done"}}, nil
	}})
	send(f, "i")
	first := send(f, "a")
	second := send(f, "b")
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected completion cmds, got %v %v", first, second)
	}
	f.Update(first[0]) // stale: gen of "a"
	if strings.Contains(f.View(), "a-done") {
		t.Fatal("stale completion applied")
	}
	f.Update(CompletionMsg{FieldID: "other", Gen: f.Gen("cwd"), Options: []Option{{Value: "zzz"}}})
	f.Update(second[0])
	if !strings.Contains(f.View(), "ab-done") || strings.Contains(f.View(), "zzz") {
		t.Fatalf("fresh completion missing:\n%s", f.View())
	}
}

func TestPathPickerLocalAndRemote(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := mustNew(t,
		Field{ID: "files", Label: "Files", Kind: Path, Path: PathSpec{Mode: PickFile, Multiple: true}},
		Field{ID: "rdir", Label: "Remote dir", Kind: Path, Path: PathSpec{Mode: PickDir, Host: "vps2"}},
		Field{ID: "out", Label: "Output", Kind: Path, Path: PathSpec{Mode: PickSave}, Default: filepath.Join(dir, "missing.json")},
	)
	msgs := send(f, "ctrl+o")
	if len(msgs) != 1 {
		t.Fatalf("no pick request: %v", msgs)
	}
	req, ok := msgs[0].(PickRequestMsg)
	if !ok || req.FieldID != "files" || req.FormID != f.ID || !req.Multiple || req.Mode != PickFile || req.Base != home || req.Gen != f.Gen("files") {
		t.Fatalf("request %+v", msgs[0])
	}
	if o := req.PickerOptions(); o.Mode != pathpicker.Files || !o.Multiple || o.Base != home || o.ID != f.ID+"/files" {
		t.Fatalf("picker options %+v", o)
	}
	f.Update(req.Result([]string{"/tmp/x", "/tmp/y"}, nil))
	s, _ := submit(t, f)
	if strings.Join(s.Values["files"].([]string), ",") != "/tmp/x,/tmp/y" {
		t.Fatalf("picked values %v", s.Values)
	}
	send(f, "j")
	if msgs := send(f, "ctrl+o"); len(msgs) != 0 {
		t.Fatalf("pick request emitted for remote host: %v", msgs)
	}
	if !strings.Contains(f.FieldNote("rdir"), "no local picker for remote host") {
		t.Fatalf("remote note %q", f.FieldNote("rdir"))
	}
	// base is the value's directory when it exists
	send(f, "j")
	m := send(f, "ctrl+o")
	if r := m[0].(PickRequestMsg); r.Base != dir || r.Mode != PickSave || r.PickerOptions().Mode != pathpicker.SaveAs {
		t.Fatalf("save request %+v", r)
	}
	f.Update(m[0].(PickRequestMsg).Result([]string{filepath.Join(dir, "new.json")}, nil))
	if f.Text("out") != filepath.Join(dir, "new.json") {
		t.Fatalf("single pick applied %q", f.Text("out"))
	}
	// stale picker result is ignored after an edit
	send(f, "k", "k")
	m = send(f, "ctrl+o")
	send(f, "i", "z")
	f.Update(m[0].(PickRequestMsg).Result([]string{"/stale"}, nil))
	if f.Text("files") != "z" {
		t.Fatalf("stale picker applied: %q", f.Text("files"))
	}
	m = send(f, "ctrl+o")
	f.Update(m[0].(PickRequestMsg).Result(nil, ErrPickerCanceled))
	if !strings.Contains(f.FieldNote("files"), "cancelled") || f.Text("files") != "z" {
		t.Fatal("cancel not reported")
	}
}

func TestDraftRestoreAndDirtyAndFailedSubmitKeepsDraft(t *testing.T) {
	mk := func() *Form {
		return mustNew(t,
			Field{ID: "name", Label: "Name", Kind: Text, Required: true},
			Field{ID: "n", Label: "Count", Kind: Number},
			Field{ID: "ok", Label: "Queue", Kind: Bool},
		)
	}
	f := mk()
	if f.Dirty() {
		t.Fatal("fresh form dirty")
	}
	send(f, "2", "i")
	typeText(f, "abc")
	send(f, "esc", "3", "enter")
	if !f.Dirty() || !strings.Contains(f.View(), "unsaved changes") {
		t.Fatal("dirty marker missing")
	}
	if _, ok := submit(t, f); ok {
		t.Fatal("invalid submitted")
	}
	if f.Text("n") != "abc" || f.Focused() != "name" {
		t.Fatalf("draft lost or focus wrong: %q %s", f.Text("n"), f.Focused())
	}
	d := f.Draft()
	g := mk()
	g.Restore(d)
	if g.Text("n") != "abc" || !g.Dirty() {
		t.Fatal("restore failed")
	}
	send(g, "1", "i")
	typeText(g, "bob")
	send(g, "esc", "2", "i", "ctrl+u")
	for range "abc" {
		send(g, "backspace")
	}
	typeText(g, "4")
	s, ok := submit(t, g)
	if !ok || s.Values["name"] != "bob" || s.Values["n"] != 4.0 || s.Values["ok"] != true {
		t.Fatalf("submit %+v %v", s, ok)
	}
	// escape ladder
	send(g, "esc")
	msgs := send(g, "esc")
	if c, ok := msgs[0].(CancelMsg); !ok || !c.Dirty {
		t.Fatalf("cancel %v", msgs)
	}
}

func TestNavigationFinderShortcutsAndReserved(t *testing.T) {
	if _, err := New("x", "x", Field{ID: "a", Shortcut: 'j'}); err == nil {
		t.Fatal("navigation-colliding shortcut accepted")
	}
	f := mustNew(t, Field{ID: "a", Label: "Alpha"}, Field{ID: "b", Label: "Bravo", Shortcut: 'b'}, Field{ID: "c", Label: "Charlie"})
	send(f, "tab")
	if f.Focused() != "b" {
		t.Fatal("tab")
	}
	send(f, "shift+tab", "k")
	if f.Focused() != "c" {
		t.Fatal("wrap k")
	}
	send(f, "b")
	if f.Focused() != "b" || !f.Insert() {
		t.Fatal("shortcut")
	}
	send(f, "esc", "/")
	typeText(f, "chr")
	send(f, "enter")
	if f.Focused() != "c" {
		t.Fatalf("finder -> %s", f.Focused())
	}
}

func TestRankExactPrefixFuzzy(t *testing.T) {
	got := Rank("api", []Option{{Value: "rapid"}, {Value: "apiserver"}, {Value: "api"}, {Value: "zzz"}})
	if len(got) != 3 || got[0].Value != "api" || got[1].Value != "apiserver" || got[2].Value != "rapid" {
		t.Fatalf("%v", got)
	}
}
