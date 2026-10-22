//go:build acceptance

package acceptance

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/ui/pathpicker"
)

func pickTree(t *testing.T) string {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	for _, p := range []string{"a.txt", ".hidden", "sub/b.go", "sub/deep/c.md", "node_modules/x.js", ".git/HEAD"} {
		fp := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte(p+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func driveP(m *pathpicker.Model, c tea.Cmd) {
	if c == nil {
		return
	}
	switch v := c().(type) {
	case nil, pathpicker.ChosenMsg, pathpicker.CancelMsg:
	case tea.BatchMsg:
		for _, c := range v {
			driveP(m, c)
		}
	default:
		driveP(m, m.Update(v))
	}
}

func pkey(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestPathPickerFd(t *testing.T) {
	root := pickTree(t)
	f, ok := pathpicker.LookupFd()
	if !ok {
		t.Fatal("neither fd nor fdfind on PATH")
	}
	want := "fd"
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("fd"); err != nil {
			want = "fdfind"
		}
	}
	if filepath.Base(f.Bin) != want {
		t.Fatalf("finder binary %s, want %s", f.Bin, want)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := f.List(ctx, root, pathpicker.Files)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != ".hidden,a.txt,sub/b.go,sub/deep/c.md" {
		t.Fatalf("%s listing %v", f.Bin, got)
	}

	// find fallback with fd/fdfind absent from PATH
	only := t.TempDir()
	findBin, err := exec.LookPath("find")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(findBin, filepath.Join(only, "find")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", only)
	ff := pathpicker.NewFinder()
	if _, isFind := ff.(pathpicker.FindFinder); !isFind {
		t.Fatalf("fallback finder %T", ff)
	}
	fgot, err := ff.List(ctx, root, pathpicker.Files)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(fgot)
	if strings.Join(fgot, ",") != strings.Join(got, ",") {
		t.Fatalf("find listing %v != fd listing %v", fgot, got)
	}
	os.Setenv("PATH", "") // restored by t.Setenv cleanup

	// picker model with the real fd finder: Tab descends, Backspace goes up
	t.Setenv("HOME", root)
	m := pathpicker.New(pathpicker.Options{Mode: pathpicker.Dirs, Finder: f})
	m.SetSize(80, 20)
	driveP(m, m.Init())
	if m.Base() != root {
		t.Fatalf("default base %q want $HOME %q", m.Base(), root)
	}
	if _, total := m.Counts(); total != 2 { // sub, sub/deep (node_modules/.git excluded)
		t.Fatalf("dir candidates %d", total)
	}
	for _, r := range "deep" {
		driveP(m, m.Update(pkey(string(r))))
	}
	driveP(m, m.Update(pkey("tab")))
	if m.Base() != filepath.Join(root, "sub", "deep") || m.Query() != "" {
		t.Fatalf("tab: base %q query %q", m.Base(), m.Query())
	}
	driveP(m, m.Update(pkey("backspace")))
	if m.Base() != filepath.Join(root, "sub") {
		t.Fatalf("backspace: base %q", m.Base())
	}
	driveP(m, m.Update(pkey("~")))
	if m.Base() != root {
		t.Fatalf("~: base %q", m.Base())
	}
}
