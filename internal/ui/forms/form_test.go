package forms

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/ui/pathpicker"
)

func key(s string) tea.KeyMsg {
	keys := map[string]tea.KeyType{
		"esc": tea.KeyEsc, "enter": tea.KeyEnter, "tab": tea.KeyTab,
		"ctrl+s": tea.KeyCtrlS, "ctrl+n": tea.KeyCtrlN, "ctrl+p": tea.KeyCtrlP,
		"ctrl+o": tea.KeyCtrlO, "ctrl+u": tea.KeyCtrlU, "backspace": tea.KeyBackspace,
		"up": tea.KeyUp, "down": tea.KeyDown,
	}
	if k, ok := keys[s]; ok {
		return tea.KeyMsg{Type: k}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func run(c tea.Cmd) []tea.Msg {
	if c == nil {
		return nil
	}
	m := c()
	if batch, ok := m.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, run(c)...)
		}
		return out
	}
	if m == nil {
		return nil
	}
	return []tea.Msg{m}
}

func send(f *Form, keys ...string) []tea.Msg {
	var out []tea.Msg
	for _, k := range keys {
		out = append(out, run(f.Update(key(k)))...)
	}
	return out
}

func typeText(f *Form, s string) []tea.Msg {
	var out []tea.Msg
	for _, r := range s {
		out = append(out, send(f, string(r))...)
	}
	return out
}

func selectField(f *Form, id string) []tea.Msg {
	if f.Insert() {
		send(f, "esc")
	}
	return send(f, "ctrl+u", ":"+f.byID[id].code, "enter")
}

func replace(f *Form, text string) {
	send(f, "ctrl+u")
	typeText(f, text)
}

func mustNew(t *testing.T, fields ...Field) *Form {
	t.Helper()
	f, err := New("test", "Test", fields...)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func submit(t *testing.T, f *Form) (SubmitMsg, bool) {
	t.Helper()
	for _, m := range send(f, "ctrl+s") {
		if s, ok := m.(SubmitMsg); ok {
			return s, true
		}
	}
	return SubmitMsg{}, false
}

var noProject = &Option{Value: "no-project", Label: "no-project"}

func TestCodesAreUniqueAndSelectionIsExplicit(t *testing.T) {
	for _, fields := range [][]Field{
		{{ID: ""}}, {{ID: "a"}, {ID: "a"}}, {{ID: "name"}, {ID: "NAME"}},
		{{ID: "bad code"}}, {{ID: ":name"}},
		{{ID: "child", Kind: Linked}}, {{ID: "child", Kind: Linked, ParentID: "missing"}},
	} {
		if _, err := New("test", "Test", fields...); err == nil {
			t.Fatalf("accepted invalid fields: %+v", fields)
		}
	}
	f := mustNew(t, Field{ID: "name", Label: "Name"}, Field{ID: "notes", Label: "Notes"})
	for _, code := range []string{"name", ":N", ":MISSING", "i", "1", "/"} {
		send(f, "ctrl+u", code, "enter")
		if f.Insert() || f.Focused() != "name" || f.Dirty() {
			t.Fatalf("invalid selector %q changed form", code)
		}
	}
	selectField(f, "notes")
	if !f.Insert() || f.Focused() != "notes" {
		t.Fatal("unique code did not select notes")
	}
	typeText(f, "a note")
	send(f, "enter")
	if f.Insert() || f.Text("notes") != "a note" || f.Text("name") != "" {
		t.Fatal("Enter did not apply only the selected field")
	}
}

func TestExtensibleFuzzySelectionVersusNewDraft(t *testing.T) {
	f := mustNew(t, Field{ID: "project", Kind: Extensible, Options: []Option{{Value: "backend"}, {Value: "website"}}})
	selectField(f, "project")
	typeText(f, "bkd")
	if got := f.suggestions(f.cur()); len(got) != 1 || got[0].Value != "backend" {
		t.Fatalf("fuzzy suggestions: %v", got)
	}
	send(f, "down", "enter")
	s, ok := submit(t, f)
	if !ok || s.Values["project"] != "backend" || len(s.Staged) != 0 {
		t.Fatalf("selected existing option was staged: %+v", s)
	}
	selectField(f, "project")
	replace(f, "web") // Fuzzy matches do not implicitly replace typed new values.
	send(f, "enter")
	s, ok = submit(t, f)
	if !ok || !reflect.DeepEqual(s.Staged["project"], []string{"web"}) {
		t.Fatalf("new draft not staged on successful submit: %+v", s)
	}
	selectField(f, "project")
	replace(f, "Website")
	s, ok = submit(t, f)
	if !ok || s.Values["project"] != "website" || len(s.Staged) != 0 {
		t.Fatalf("exact label/value must not stage: %+v", s)
	}
	selectField(f, "project")
	replace(f, "")
	s, ok = submit(t, f)
	if !ok || len(s.Staged) != 0 || s.Values["project"] != nil {
		t.Fatalf("empty must not stage: %+v", s)
	}
}

func TestStrictChoiceAndValidationKeepDraft(t *testing.T) {
	f := mustNew(t, Field{ID: "protocol", Kind: Choice, Required: true, Options: []Option{{Value: "ssh"}, {Value: "local"}}})
	selectField(f, "protocol")
	typeText(f, "new")
	send(f, "enter")
	if !f.Insert() || f.Text("protocol") != "new" || f.FieldError("protocol") == "" {
		t.Fatal("invalid enum draft not retained")
	}
	if _, ok := submit(t, f); ok {
		t.Fatal("unmatched enum submitted")
	}
	replace(f, "ss")
	send(f, "ctrl+n")
	s, ok := submit(t, f)
	if !ok || s.Values["protocol"] != "ssh" || len(s.Staged) != 0 {
		t.Fatalf("explicit enum suggestion selection: %+v", s)
	}
	g := mustNew(t, Field{ID: "name", Kind: Text, Validate: func(v any) error {
		if v == "bad" {
			return errors.New("name rejected")
		}
		return nil
	}})
	selectField(g, "name")
	typeText(g, "bad")
	if _, ok := submit(t, g); ok || g.FieldError("name") != "name rejected" || g.Text("name") != "bad" {
		t.Fatal("custom validation lost or accepted invalid draft")
	}
}

func TestRequiredExplicitNoValueAndRestore(t *testing.T) {
	field := Field{ID: "project", Kind: Extensible, Required: true, NoValue: noProject, Options: []Option{{Value: "website"}}}
	f := mustNew(t, field)
	if _, ok := submit(t, f); ok || !strings.Contains(f.FieldError("project"), "no-project") {
		t.Fatal("required field must offer explicit no-value choice")
	}
	selectField(f, "project")
	send(f, "ctrl+n", "enter")
	s, ok := submit(t, f)
	if !ok || s.Values["project"] != (None{Value: "no-project"}) || len(s.Staged) != 0 {
		t.Fatalf("explicit no-project: %+v", s)
	}
	field.Default = "website"
	g := mustNew(t, field)
	g.Restore(f.Draft())
	s, ok = submit(t, g)
	if !ok || s.Values["project"] != (None{Value: "no-project"}) {
		t.Fatalf("default overwrote restored explicit sentinel: %+v", s)
	}
}

func TestMultiSelectAndExtensibleItems(t *testing.T) {
	for _, kind := range []Kind{MultiSelect, Extensible} {
		f := mustNew(t, Field{ID: "tags", Kind: kind, Multi: true, Options: []Option{{Value: "backend"}, {Value: "api"}}})
		selectField(f, "tags")
		typeText(f, "bkd")
		send(f, "down", "tab")
		if !f.Insert() || !reflect.DeepEqual(f.cur().items, []string{"backend"}) {
			t.Fatal("tab did not accept multi item and retain editor")
		}
		typeText(f, "api")
		send(f, "enter")
		if f.Insert() {
			t.Fatal("Enter did not return to field table")
		}
		s, ok := submit(t, f)
		if !ok || !reflect.DeepEqual(s.Values["tags"], []string{"backend", "api"}) {
			t.Fatalf("multi submit: %+v", s)
		}
		selectField(f, "tags")
		typeText(f, "backend")
		send(f, "tab")       // Toggle selected item off.
		send(f, "backspace") // Empty backspace removes the last remaining item.
		if len(f.cur().items) != 0 {
			t.Fatal("multi items could not be removed")
		}
		typeText(f, "new-tag")
		s, ok = submit(t, f)
		if kind == MultiSelect {
			if ok || f.FieldError("tags") == "" {
				t.Fatal("closed multi accepted a new value")
			}
		} else if !ok || !reflect.DeepEqual(s.Staged["tags"], []string{"new-tag"}) {
			t.Fatalf("extensible multi did not stage: %+v", s)
		}
	}
}

func TestLinkedDependencies(t *testing.T) {
	f := mustNew(t,
		Field{ID: "host", Kind: Choice, Options: []Option{{Value: "local"}, {Value: "remote"}}},
		Field{ID: "session", Kind: Linked, ParentID: "host", Options: []Option{
			{Value: "a", Label: "alpha", Parent: "local"}, {Value: "b", Label: "beta", Parent: "remote"},
		}},
	)
	selectField(f, "session")
	typeText(f, "beta")
	send(f, "enter")
	if f.Text("host") != "remote" {
		t.Fatal("child did not fill parent")
	}
	selectField(f, "host")
	replace(f, "local")
	send(f, "enter")
	if f.Text("session") != "" || !strings.Contains(f.FieldNote("session"), "cleared") {
		t.Fatal("parent did not clear incompatible child with explanation")
	}
	selectField(f, "session")
	if got := f.suggestions(f.cur()); len(got) != 1 || got[0].Value != "a" {
		t.Fatalf("child options not scoped to selected parent: %v", got)
	}
	send(f, "down", "enter")
	s, ok := submit(t, f)
	if !ok || s.Values["session"] != "a" || s.Values["host"] != "local" {
		t.Fatalf("linked submit: %+v", s)
	}
}

func TestCompletionGenerationsAndRestore(t *testing.T) {
	f := mustNew(t, Field{ID: "query", Complete: func(_ context.Context, q string) ([]Option, error) {
		return []Option{{Value: q + "-done"}}, nil
	}})
	selectField(f, "query")
	first, second := send(f, "a"), send(f, "b")
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("completion commands: %v %v", first, second)
	}
	f.Update(first[0])
	if len(f.cur().async) != 0 {
		t.Fatal("stale completion accepted")
	}
	f.Update(second[0])
	if got := f.cur().async; len(got) != 1 || got[0].Value != "ab-done" {
		t.Fatalf("fresh completion missing: %v", got)
	}
	f.Update(CompletionMsg{FormID: "foreign", FieldID: "query", Gen: f.Gen("query"), Options: []Option{{Value: "wrong"}}})
	if f.cur().async[0].Value != "ab-done" {
		t.Fatal("foreign completion accepted")
	}
	d := f.Draft()
	f.Restore(d)
	f.Update(second[0])
	if f.Text("query") != "ab" || f.Gen("query") <= second[0].(CompletionMsg).Gen {
		t.Fatal("restore did not invalidate in-flight completion")
	}
}

func TestPathPickerRoundtripAndManualPaths(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := mustNew(t,
		Field{ID: "files", Kind: Path, Path: PathSpec{Mode: PickFile, Multiple: true}},
		Field{ID: "remote", Kind: Path, Path: PathSpec{Mode: PickDir, Host: "server"}},
		Field{ID: "out", Kind: Path, Path: PathSpec{Mode: PickSave}, Default: filepath.Join(dir, "missing.json")},
	)
	selectField(f, "files")
	msgs := send(f, "ctrl+o")
	if len(msgs) != 1 {
		t.Fatalf("picker request: %v", msgs)
	}
	req := msgs[0].(PickRequestMsg)
	if req.FieldID != "files" || req.FormID != f.ID || !req.Multiple || req.Base != home || req.Gen != f.Gen("files") {
		t.Fatalf("request: %+v", req)
	}
	if opts := req.PickerOptions(); opts.Mode != pathpicker.Files || !opts.Multiple || opts.ID != f.ID+"/files" {
		t.Fatalf("picker options: %+v", opts)
	}
	f.Update(req.Result([]string{"/tmp/a", "/tmp/b"}, nil))
	s, ok := submit(t, f)
	if !ok || !reflect.DeepEqual(s.Values["files"], []string{"/tmp/a", "/tmp/b"}) {
		t.Fatalf("picked multi: %+v", s)
	}
	selectField(f, "remote")
	if got := send(f, "ctrl+o"); len(got) != 0 || !strings.Contains(f.FieldNote("remote"), "no local picker") {
		t.Fatal("remote field used local picker")
	}
	typeText(f, "/srv/project")
	send(f, "enter")
	selectField(f, "out")
	req = send(f, "ctrl+o")[0].(PickRequestMsg)
	if req.Base != dir || req.Mode != PickSave || req.PickerOptions().Mode != pathpicker.SaveAs {
		t.Fatalf("save picker: %+v", req)
	}
	f.Update(req.Result([]string{filepath.Join(dir, "new.json")}, nil))
	if f.input.Value() != filepath.Join(dir, "new.json") {
		t.Fatal("picker did not refresh shared input")
	}
	req = send(f, "ctrl+o")[0].(PickRequestMsg)
	replace(f, "/manual")
	f.Update(req.Result([]string{"/stale"}, nil))
	if f.Text("out") != "/manual" {
		t.Fatal("stale picker overwrote edit")
	}
	req = send(f, "ctrl+o")[0].(PickRequestMsg)
	f.Update(req.Result(nil, ErrPickerCanceled))
	if f.Text("out") != "/manual" || !strings.Contains(f.FieldNote("out"), "cancelled") {
		t.Fatal("picker cancel lost draft")
	}
}

func TestMultilineDefaultEscapesPasteAndRestore(t *testing.T) {
	original := "first\nsecond\r\n\tthird\\literal\\n🙂"
	f := mustNew(t, Field{ID: "body", Kind: Multiline, Default: original})
	selectField(f, "body")
	if strings.ContainsAny(f.input.Value(), "\n\r\t") || decodeLine(f.input.Value()) != original {
		t.Fatalf("single-line representation lost data: %q", f.input.Value())
	}
	send(f, "enter")
	s, ok := submit(t, f)
	if !ok || s.Values["body"] != original || f.Dirty() {
		t.Fatalf("untouched multiline changed: %+v", s)
	}
	selectField(f, "body")
	replace(f, `one\ntwo\tthree\\n`)
	want := "one\ntwo\tthree\\n"
	if f.Text("body") != want {
		t.Fatalf("explicit escapes: %q", f.Text("body"))
	}
	send(f, "ctrl+u")
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(original), Paste: true})
	if f.Text("body") != original {
		t.Fatalf("bracketed paste lost data: %q", f.Text("body"))
	}
	send(f, "ctrl+u")
	f.Update(pasteMsg{text: original, gen: f.inputGeneration()})
	if f.Text("body") != original {
		t.Fatal("clipboard paste lost data")
	}
	g := mustNew(t, Field{ID: "body", Kind: Multiline})
	g.Restore(f.Draft())
	s, ok = submit(t, g)
	if !ok || s.Values["body"] != original {
		t.Fatalf("restored multiline: %+v", s)
	}
}

func TestBoolNumberDirtyCancelAndSubmitLevels(t *testing.T) {
	f := mustNew(t, Field{ID: "enabled", Kind: Bool}, Field{ID: "count", Kind: Number, Required: true})
	selectField(f, "enabled")
	typeText(f, "false")
	send(f, "enter")
	selectField(f, "count")
	typeText(f, "bad")
	if _, ok := submit(t, f); ok || f.Text("count") != "bad" {
		t.Fatal("invalid number submitted or lost")
	}
	replace(f, "4")
	s, ok := submit(t, f)
	if !ok || s.Values["count"] != 4.0 || s.Values["enabled"] != false {
		t.Fatalf("editing-level submission: %+v", s)
	}
	if msgs := send(f, "esc"); len(msgs) != 0 || f.Insert() {
		t.Fatal("first escape must return to table, not cancel")
	}
	msgs := send(f, "esc")
	if len(msgs) != 1 || !msgs[0].(CancelMsg).Dirty {
		t.Fatalf("dirty cancellation: %v", msgs)
	}
	f.MarkClean()
	msgs = send(f, "esc")
	if len(msgs) != 1 || msgs[0].(CancelMsg).Dirty {
		t.Fatal("clean cancellation marked dirty")
	}
	s, ok = submit(t, f)
	if !ok || s.Values["count"] != 4.0 {
		t.Fatal("table-level submission failed")
	}
}

func TestCancelAndFailedSubmitDoNotCreateExtensibleValues(t *testing.T) {
	f := mustNew(t, Field{ID: "project", Kind: Extensible}, Field{ID: "required", Required: true})
	selectField(f, "project")
	typeText(f, "new-project")
	if _, ok := submit(t, f); ok {
		t.Fatal("invalid form emitted staged creation")
	}
	msgs := send(f, "esc")
	if len(msgs) != 1 {
		t.Fatalf("cancel did not emit exactly one message: %v", msgs)
	}
	if c, ok := msgs[0].(CancelMsg); !ok || !c.Dirty {
		t.Fatalf("cancel emitted something other than dirty cancellation: %v", msgs)
	}
}

func TestRankExactPrefixFuzzy(t *testing.T) {
	got := Rank("api", []Option{{Value: "rapid"}, {Value: "apiserver"}, {Value: "api"}, {Value: "zzz"}})
	if len(got) != 3 || got[0].Value != "api" || got[1].Value != "apiserver" || got[2].Value != "rapid" {
		t.Fatalf("ranking: %v", got)
	}
}

func TestLabeledMultiDefaultsRemainToggleable(t *testing.T) {
	for _, def := range []any{"first", []string{"First"}} {
		f := mustNew(t, Field{ID: "items", Kind: MultiSelect, Default: def,
			Options: []Option{{Value: "first", Label: "First"}}})
		selectField(f, "items")
		send(f, "down", "tab", "enter")
		s, ok := submit(t, f)
		if !ok || s.Values["items"] != nil {
			t.Fatalf("selected labeled default did not toggle off: %+v", s)
		}
	}
}

func TestPathCompletionUsesValueNotLabel(t *testing.T) {
	f := mustNew(t, Field{ID: "path", Kind: Path, Path: PathSpec{Host: "server"}})
	selectField(f, "path")
	typeText(f, "folder")
	f.Update(CompletionMsg{FieldID: "path", Gen: f.Gen("path"),
		Options: []Option{{Value: "/srv/folder", Label: "folder"}}})
	send(f, "down", "enter")
	s, ok := submit(t, f)
	if !ok || s.Values["path"] != "/srv/folder" {
		t.Fatalf("path completion submitted label instead of path: %+v", s)
	}
}

func TestEscapePreservesUnappliedDraftAndNeverStages(t *testing.T) {
	f := mustNew(t, Field{ID: "project", Kind: Extensible})
	selectField(f, "project")
	typeText(f, "new-project")
	if msgs := send(f, "esc"); len(msgs) != 0 || f.Text("project") != "new-project" {
		t.Fatal("field escape lost draft or emitted creation")
	}
	msgs := send(f, "esc")
	if len(msgs) != 1 {
		t.Fatalf("top-level cancel: %v", msgs)
	}
	if cancel, ok := msgs[0].(CancelMsg); !ok || !cancel.Dirty {
		t.Fatalf("cancel did not preserve discard confirmation: %v", msgs)
	}
}

func TestEmptyFormStillSubmitsAndCancels(t *testing.T) {
	f := mustNew(t)
	if _, ok := submit(t, f); !ok {
		t.Fatal("empty form did not submit")
	}
	if msgs := send(f, "esc"); len(msgs) != 1 || msgs[0].(CancelMsg).Dirty {
		t.Fatal("empty form did not cancel cleanly")
	}
}
