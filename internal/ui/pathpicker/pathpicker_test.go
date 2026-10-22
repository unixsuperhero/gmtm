package pathpicker

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func key(s string) tea.KeyMsg {
	m := map[string]tea.KeyType{"esc": tea.KeyEsc, "enter": tea.KeyEnter, "tab": tea.KeyTab,
		"backspace": tea.KeyBackspace, "ctrl+c": tea.KeyCtrlC, "down": tea.KeyDown, "up": tea.KeyUp, "ctrl+d": tea.KeyCtrlD}
	if t, ok := m[s]; ok {
		return tea.KeyMsg{Type: t}
	}
	if s == " " {
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// drive runs cmd, feeding the picker's own replies back, and returns the
// messages meant for the host (ChosenMsg, CancelMsg).
func drive(m *Model, c tea.Cmd) []tea.Msg {
	if c == nil {
		return nil
	}
	msg := c()
	switch v := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range v {
			out = append(out, drive(m, c)...)
		}
		return out
	case ChosenMsg, CancelMsg:
		return []tea.Msg{v}
	}
	return drive(m, m.Update(msg))
}

func press(m *Model, keys ...string) []tea.Msg {
	var out []tea.Msg
	for _, k := range keys {
		out = append(out, drive(m, m.Update(key(k)))...)
	}
	return out
}

func typeText(m *Model, s string) []tea.Msg {
	var out []tea.Msg
	for _, r := range s {
		out = append(out, press(m, string(r))...)
	}
	return out
}

// tree builds root/{a.txt, bin.dat, sub/{b.go, deep/c.md}} and sets HOME=root.
func tree(t *testing.T) string {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	for p, body := range map[string]string{
		"a.txt": "alpha\n\tbeta\x07\xff\n", "bin.dat": "ab\x00cd", "sub/b.go": "package b\n", "sub/deep/c.md": "# c\n",
	} {
		fp := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", root)
	return root
}

func fake(root string) *FakeFinder {
	return &FakeFinder{Items: map[string][]string{
		root:                               {"a.txt", "bin.dat", "sub/b.go", "sub/deep/c.md"},
		filepath.Join(root, "sub"):         {"b.go", "deep/c.md"},
		filepath.Join(root, "sub", "deep"): {"c.md"},
	}}
}

func dirFake(root string) *FakeFinder {
	return &FakeFinder{Items: map[string][]string{
		root:                               {"sub", "sub/deep"},
		filepath.Join(root, "sub"):         {"deep"},
		filepath.Join(root, "sub", "deep"): {},
	}}
}

func open(t *testing.T, o Options) *Model {
	t.Helper()
	m := New(o)
	m.SetSize(80, 20)
	drive(m, m.Init())
	return m
}

func TestFuzzyFilterCounts(t *testing.T) {
	root := tree(t)
	m := open(t, Options{Finder: fake(root)})
	if m.Base() != root {
		t.Fatalf("default base %q want home %q", m.Base(), root)
	}
	if a, b := m.Counts(); a != 4 || b != 4 {
		t.Fatalf("counts %d/%d", a, b)
	}
	typeText(m, "bgo")
	if a, b := m.Counts(); a != 1 || b != 4 || m.Highlighted() != filepath.Join(root, "sub/b.go") {
		t.Fatalf("counts %d/%d hl %q", a, b, m.Highlighted())
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "1/4") || !strings.Contains(v, "~") {
		t.Fatalf("header/counts missing:\n%s", v)
	}
	msgs := press(m, "enter")
	if len(msgs) != 1 || msgs[0].(ChosenMsg).Paths[0] != filepath.Join(root, "sub/b.go") {
		t.Fatalf("choose %v", msgs)
	}
}

func TestDescendUpHomeAndAbsoluteRebase(t *testing.T) {
	root := tree(t)
	m := open(t, Options{Mode: Dirs, Finder: dirFake(root)})
	typeText(m, "deep")
	press(m, "tab")
	if m.Base() != filepath.Join(root, "sub", "deep") || m.Query() != "" {
		t.Fatalf("tab descend: base %q query %q", m.Base(), m.Query())
	}
	press(m, "backspace")
	if m.Base() != filepath.Join(root, "sub") || m.Highlighted() != filepath.Join(root, "sub", "deep") {
		t.Fatalf("backspace up: base %q hl %q", m.Base(), m.Highlighted())
	}
	typeText(m, "~")
	if m.Base() != root || m.Query() != "" {
		t.Fatalf("~ reset: %q %q", m.Base(), m.Query())
	}
	typeText(m, "/") // "~/" typed key by key stays at home
	if m.Base() != root {
		t.Fatalf("~/ jumped to %q", m.Base())
	}
	press(m, root+"/sub/de") // pasted absolute path
	if m.Base() != filepath.Join(root, "sub") || m.Query() != "de" {
		t.Fatalf("abs rebase: %q %q", m.Base(), m.Query())
	}
	press(m, "backspace", "backspace", "backspace") // clear query, then up
	if m.Base() != root {
		t.Fatalf("up after clearing: %q", m.Base())
	}
	press(m, "~/sub/")
	if m.Base() != filepath.Join(root, "sub") {
		t.Fatalf("~/ rebase: %q", m.Base())
	}
	press(m, "/nope-does-not-exist/x")
	if m.Base() != filepath.Join(root, "sub") || !strings.Contains(m.Note(), "no such directory") {
		t.Fatalf("missing dir: %q %q", m.Base(), m.Note())
	}
	// Dirs mode can choose the base itself via "./"
	m2 := open(t, Options{Mode: Dirs, Finder: dirFake(root)})
	if msgs := press(m2, "enter"); len(msgs) != 1 || msgs[0].(ChosenMsg).Paths[0] != root {
		t.Fatalf("choose base dir %v", msgs)
	}
}

func TestTabOnFileDescendsIntoItsDirectory(t *testing.T) {
	root := tree(t)
	m := open(t, Options{Finder: fake(root)})
	typeText(m, "cmd")
	press(m, "tab")
	if m.Base() != filepath.Join(root, "sub", "deep") {
		t.Fatalf("base %q", m.Base())
	}
}

func TestSaveAsNewPath(t *testing.T) {
	root := tree(t)
	m := open(t, Options{Mode: SaveAs, Finder: fake(root)})
	typeText(m, "out.json")
	if !strings.Contains(ansi.Strip(m.View()), "new file") {
		t.Fatalf("no new-file hint:\n%s", ansi.Strip(m.View()))
	}
	msgs := press(m, "enter")
	if len(msgs) != 1 || msgs[0].(ChosenMsg).Paths[0] != filepath.Join(root, "out.json") {
		t.Fatalf("save as %v", msgs)
	}
	m = open(t, Options{Mode: SaveAs, Finder: fake(root)})
	typeText(m, "a.txt")
	if msgs := press(m, "enter"); msgs[0].(ChosenMsg).Paths[0] != filepath.Join(root, "a.txt") {
		t.Fatalf("existing %v", msgs)
	}
}

func TestMultipleSelection(t *testing.T) {
	root := tree(t)
	m := open(t, Options{Multiple: true, Finder: fake(root)})
	press(m, " ", "down", " ")
	msgs := press(m, "enter")
	got := msgs[0].(ChosenMsg).Paths
	want := []string{filepath.Join(root, "a.txt"), filepath.Join(root, "sub/b.go")}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
	press(m, "up", "up", "up", " ") // back to a.txt and unmark it
	if len(m.Marked()) != 1 {
		t.Fatalf("toggle off: %v", m.Marked())
	}
}

func TestStaleListingIgnored(t *testing.T) {
	root := tree(t)
	ff := &FakeFinder{Items: map[string][]string{root: {"old"}}}
	m := New(Options{Finder: ff})
	m.SetSize(80, 10)
	first := m.Init()()
	ff.Items[root] = []string{"new1", "new2"}
	second := m.Update(key("~"))()
	m.Update(second)
	m.Update(first) // stale reply arrives late
	if _, total := m.Counts(); total != 2 || m.Listing() {
		t.Fatalf("stale listing applied: total %d listing %v", total, m.Listing())
	}
	// a reply from another picker instance is ignored too
	other := New(Options{Finder: ff})
	m.Update(other.Init()())
	if _, total := m.Counts(); total != 2 {
		t.Fatal("foreign reply applied")
	}
}

func TestPreviewStalenessAndContent(t *testing.T) {
	root := tree(t)
	if pv := LoadPreview(filepath.Join(root, "bin.dat")); pv.Note != "binary file" || len(pv.Lines) != 0 {
		t.Fatalf("binary %+v", pv)
	}
	pv := LoadPreview(filepath.Join(root, "a.txt"))
	if pv.Lines[0] != "alpha" || pv.Lines[1] != "    beta�" {
		t.Fatalf("sanitise %q", pv.Lines)
	}
	if pv := LoadPreview(filepath.Join(root, "sub")); strings.Join(pv.Lines, ",") != "b.go,deep/" {
		t.Fatalf("dir %q", pv.Lines)
	}
	m := New(Options{Finder: fake(root)})
	m.SetSize(80, 10)
	m.Update(m.Init()())
	c1 := m.Update(key("down")) // preview for bin.dat
	c2 := m.Update(key("down")) // preview for sub/b.go
	m.Update(c2())
	m.Update(c1()) // stale
	if m.pv.Path != filepath.Join(root, "sub/b.go") {
		t.Fatalf("stale preview applied: %q", m.pv.Path)
	}
}

func TestNormalModeAndCancel(t *testing.T) {
	root := tree(t)
	m := open(t, Options{ID: "p1", Finder: fake(root)})
	if msgs := press(m, "esc"); len(msgs) != 0 || m.InputMode() != "normal" {
		t.Fatalf("esc should enter normal: %v", msgs)
	}
	press(m, "j")
	if m.Query() != "" || m.Highlighted() != filepath.Join(root, "bin.dat") {
		t.Fatalf("j in normal: q %q hl %q", m.Query(), m.Highlighted())
	}
	if msgs := press(m, "esc"); len(msgs) != 1 || msgs[0] != (CancelMsg{ID: "p1"}) {
		t.Fatalf("second esc %v", msgs)
	}
	press(m, "i", "j")
	if m.Query() != "j" {
		t.Fatalf("insert typing %q", m.Query())
	}
	if msgs := press(m, "ctrl+c"); len(msgs) != 1 {
		t.Fatal("ctrl+c")
	}
}

func TestViewCroppedToSize(t *testing.T) {
	root := tree(t)
	long := strings.Repeat("日本語ディレクトリ/", 12) + "ファイル.txt"
	ff := &FakeFinder{Items: map[string][]string{root: {long, "a.txt", "sub/b.go"}}}
	for i := 0; i < 200; i++ {
		ff.Items[root] = append(ff.Items[root], strings.Repeat("x", i)+".log")
	}
	for _, sz := range [][2]int{{10, 2}, {20, 4}, {40, 10}, {59, 8}, {80, 24}, {200, 6}} {
		m := New(Options{Finder: ff, Title: strings.Repeat("Title ", 30)})
		m.SetSize(sz[0], sz[1])
		drive(m, m.Init())
		typeText(m, "t")
		lines := strings.Split(m.View(), "\n")
		if len(lines) != sz[1] {
			t.Fatalf("%v: %d lines", sz, len(lines))
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w != sz[0] {
				t.Fatalf("%v line %d width %d: %q", sz, i, w, ansi.Strip(l))
			}
		}
	}
	m := New(Options{Finder: ff})
	m.SetSize(80, 10)
	drive(m, m.Init())
	if !strings.Contains(ansi.Strip(m.View()), "↓") {
		t.Fatal("no overflow indicator")
	}
}

func realTree(t *testing.T) string {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	for _, p := range []string{"keep/a.txt", ".hidden/h.txt", "node_modules/x/y.js", ".git/HEAD", "deep/node_modules/z.js", "ign.log"} {
		fp := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(fp), 0o755)
		os.WriteFile(fp, []byte("x"), 0o644)
	}
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ign.log\n"), 0o644)
	return root
}

func checkReal(t *testing.T, f Finder) {
	t.Helper()
	root := realTree(t)
	ctx, cancel := context.WithTimeout(context.Background(), ListTimeout)
	defer cancel()
	got, err := f.List(ctx, root, Files)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != ".gitignore,.hidden/h.txt,ign.log,keep/a.txt" {
		t.Fatalf("files %q", got)
	}
	got, _ = f.List(ctx, root, Dirs)
	sort.Strings(got)
	if strings.Join(got, ",") != ".hidden,deep,keep" {
		t.Fatalf("dirs %q", got)
	}
}

func TestFindFinder(t *testing.T) { checkReal(t, FindFinder{}) }

func TestFdFinder(t *testing.T) {
	f, ok := LookupFd()
	if !ok {
		t.Skip("neither fd nor fdfind on PATH")
	}
	checkReal(t, f)
}
