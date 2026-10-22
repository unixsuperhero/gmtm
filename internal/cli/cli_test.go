package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/gmtm/internal/ui/pathpicker"
)

func run(args ...string) (int, string, string) {
	var o, e bytes.Buffer
	c := Main(args, strings.NewReader(""), &o, &e)
	return c, o.String(), e.String()
}

func script(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestBuiltinBeatsPath(t *testing.T) {
	dir := t.TempDir()
	script(t, dir, "mtm-json", "echo EXTERNAL; exit 9")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	called := false
	old := runExplorer
	runExplorer = func(data []byte, title string) error { called = true; return nil }
	defer func() { runExplorer = old }()
	c, out, _ := run("mtm", "json")
	if c != 0 || !called || strings.Contains(out, "EXTERNAL") {
		t.Fatalf("code %d called %v out %q", c, called, out)
	}
}

func TestExternalRenamedBinaryAndExit(t *testing.T) {
	dir := t.TempDir()
	script(t, dir, "foo-hello", `echo "hi $1"; exit 7`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	c, out, _ := run("/x/y/foo", "hello", "there")
	if c != 7 || out != "hi there\n" {
		t.Fatalf("code %d out %q", c, out)
	}
	// mtm-hello does not exist for binary mtm
	if c, _, e := run("mtm", "hello"); c == 0 || !strings.Contains(e, "unknown command") {
		t.Fatalf("code %d err %q", c, e)
	}
}

func TestEnvScrubbing(t *testing.T) {
	dir := t.TempDir()
	script(t, dir, "mtm-env", `echo "S=$TUIOS_SESSION P=$TUIOS_PANE_ID SOCK=$TUIOS_SOCKET TS=$TUIOS_SERVER MS=$MTM_SERVER"`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, k := range []string{"TUIOS_SESSION", "TUIOS_PANE_ID", "TUIOS_SOCKET", "TUIOS_WINDOW_ID", "TUIOS_PANE_TOKEN", "TUIOS_PANE_GRANTS"} {
		t.Setenv(k, "leak")
	}
	c, out, _ := run("mtm", "--server", "abc", "env")
	if c != 0 || out != "S= P= SOCK= TS=abc MS=abc\n" {
		t.Fatalf("code %d out %q", c, out)
	}
}

func TestKnownCommandRejectsBadArgs(t *testing.T) {
	dir := t.TempDir()
	script(t, dir, "mtm-version", "echo EXTERNAL")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, a := range [][]string{{"version", "extra"}, {"version", "--bogus"}, {"actions", "x"}, {"server", "y"}, {"help", "nope"}, {"json", "a", "b"}, {"--json", "version"}} {
		c, out, e := run(append([]string{"mtm"}, a...)...)
		if c == 0 || strings.Contains(out, "EXTERNAL") || e == "" {
			t.Errorf("%v: code %d out %q err %q", a, c, out, e)
		}
	}
}

func TestServerValidation(t *testing.T) {
	for _, s := range []string{"", "has space", "waytoolongservername", "a/b"} {
		if c, _, _ := run("mtm", "--server", s, "version"); c == 0 {
			t.Errorf("server %q accepted", s)
		}
	}
	if c, _, _ := run("mtm", "--server", "ok_1-x", "version"); c != 0 {
		t.Error("valid server rejected")
	}
}

func TestHelpHumanReadable(t *testing.T) {
	for _, a := range [][]string{{"mtm", "help"}, {"mtm", "help", "json"}, {"mtm", "--help"}, {"mtm", "actions"}, {"mtm", "server"}, {"mtm", "version"}} {
		c, out, _ := run(a...)
		if c != 0 || strings.HasPrefix(strings.TrimSpace(out), "{") || strings.HasPrefix(strings.TrimSpace(out), "[") {
			t.Errorf("%v: code %d out %q", a, c, out)
		}
	}
	_, out, _ := run("mtm", "help")
	if !strings.Contains(out, "Commands:") || !strings.Contains(out, "json [FILE]") {
		t.Errorf("help body: %s", out)
	}
	if c, out, _ := run("mtm", "--json", "actions"); c != 0 || !strings.HasPrefix(out, "[") {
		t.Errorf("actions --json: %d %q", c, out)
	}
}

func TestTUIHook(t *testing.T) {
	old := RunTUI
	defer func() { RunTUI = old }()
	RunTUI = nil
	if c, _, e := run("mtm"); c != 1 || !strings.Contains(e, "TUI not wired") {
		t.Fatalf("%d %q", c, e)
	}
	var got Options
	RunTUI = func(o Options) error { got = o; return nil }
	if c, _, _ := run("mtm", "--server", "s1", "--db", "/x.db", "tui"); c != 0 || got.Server != "s1" || got.DB != "/x.db" {
		t.Fatalf("%d %+v", c, got)
	}
}

func TestJSONInvalidReportsPosition(t *testing.T) {
	var o, e bytes.Buffer
	c := Main([]string{"mtm", "json"}, strings.NewReader("{\n \"a\" 1}"), &o, &e)
	if c == 0 || !strings.Contains(e.String(), "line 2") {
		t.Fatalf("%d %q", c, e.String())
	}
}

func TestJSONNoFileNonTTYReadsStdinWithoutPicker(t *testing.T) {
	oldR, oldP := runExplorer, pickFile
	defer func() { runExplorer, pickFile = oldR, oldP }()
	pickFile = func() (string, error) { t.Fatal("picker used for piped stdin"); return "", nil }
	var gotTitle, gotData string
	runExplorer = func(data []byte, title string) error { gotData, gotTitle = string(data), title; return nil }
	var o, e bytes.Buffer
	if c := Main([]string{"mtm", "json"}, strings.NewReader(`{"a":1}`), &o, &e); c != 0 || gotTitle != "stdin" || gotData != `{"a":1}` {
		t.Fatalf("%d %q %q %q", c, gotTitle, gotData, e.String())
	}
}

func TestJSONNoFileOnTTYUsesFuzzyPicker(t *testing.T) {
	oldR, oldP, oldT := runExplorer, pickFile, isTTY
	defer func() { runExplorer, pickFile, isTTY = oldR, oldP, oldT }()
	isTTY = func(io.Reader) bool { return true }
	f := filepath.Join(t.TempDir(), "doc.json")
	os.WriteFile(f, []byte(`[1]`), 0o644)
	pickFile = func() (string, error) { return f, nil }
	var gotTitle string
	runExplorer = func(data []byte, title string) error { gotTitle = title; return nil }
	if c, _, e := run("mtm", "json"); c != 0 || gotTitle != f {
		t.Fatalf("%d %q %q", c, gotTitle, e)
	}
	pickFile = func() (string, error) { return "", pathpicker.ErrCanceled }
	if c, _, e := run("mtm", "json"); c != 1 || !strings.Contains(e, "no file chosen") {
		t.Fatalf("cancel: %d %q", c, e)
	}
}

func TestExternalAbbreviation(t *testing.T) {
	dir, dir2 := t.TempDir(), t.TempDir()
	script(t, dir, "mtm-deploy", `echo "deploy $1"; exit 5`)
	script(t, dir, "mtm-status", "echo STATUS")
	script(t, dir, "mtm-statusbar", "echo BAR")
	script(t, dir, "mtm-dbx", "echo DBX")
	script(t, dir2, "mtm-dbz", "echo DBZ")
	script(t, dir2, "mtm-deploy", "echo SHADOWED")
	script(t, dir, "mtm-json", "echo EXTERNAL")
	os.WriteFile(filepath.Join(dir, "mtm-dep-notexec"), []byte("x"), 0o644)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+dir2+string(os.PathListSeparator)+os.Getenv("PATH"))
	// exact beats prefix
	if c, out, _ := run("mtm", "status"); c != 0 || out != "STATUS\n" {
		t.Fatalf("exact: %d %q", c, out)
	}
	// unique prefix runs, args pass through, exit code propagates, first PATH entry wins
	if c, out, _ := run("mtm", "dep", "prod"); c != 5 || out != "deploy prod\n" {
		t.Fatalf("prefix: %d %q", c, out)
	}
	// ambiguous prefix (across PATH dirs) lists every candidate
	if c, _, e := run("mtm", "db"); c != 2 || !strings.Contains(e, "mtm-dbx") || !strings.Contains(e, "mtm-dbz") || !strings.Contains(e, "ambiguous") {
		t.Fatalf("ambiguous: %d %q", c, e)
	}
	if c, _, e := run("mtm", "zzz"); c != 2 || !strings.Contains(e, "unknown command") {
		t.Fatalf("none: %d %q", c, e)
	}
	// built-in json still wins; built-ins are never abbreviated ("js" is not json)
	old := runExplorer
	defer func() { runExplorer = old }()
	called := false
	runExplorer = func([]byte, string) error { called = true; return nil }
	if c, out, _ := run("mtm", "json"); c != 0 || !called || strings.Contains(out, "EXTERNAL") {
		t.Fatalf("builtin: %d %v %q", c, called, out)
	}
	called = false
	if c, out, _ := run("mtm", "js"); c != 0 || called || out != "EXTERNAL\n" {
		t.Fatalf("js should run mtm-json from PATH, not the built-in: %d %v %q", c, called, out)
	}
	// renamed binary scans its own prefix
	script(t, dir, "foo-deploy", "echo FOO")
	if c, out, _ := run("/x/foo", "dep"); c != 0 || out != "FOO\n" {
		t.Fatalf("renamed: %d %q", c, out)
	}
}
