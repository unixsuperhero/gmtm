package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if c, _, _ := run("mtm", "--server", "s1", "--db", "/x.db", "tui"); c != 0 || got != (Options{"s1", "/x.db"}) {
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
