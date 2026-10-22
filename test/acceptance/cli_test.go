//go:build acceptance

package acceptance

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func buildMTM(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "mtm")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/mtm")
	cmd.Dir = filepath.Join("..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

type runOut struct {
	code     int
	out, err string
}

func runBin(t *testing.T, path string, env []string, stdin string, args ...string) runOut {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %s %v: %v", path, args, err)
	}
	return runOut{code, o.String(), e.String()}
}

func writeScript(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestCLI(t *testing.T) {
	mtm := buildMTM(t)
	pathDir := t.TempDir()
	env := []string{"PATH=" + pathDir + ":" + os.Getenv("PATH"), "HOME=" + t.TempDir(),
		"TUIOS_SOCKET=/bogus/user.sock", "TUIOS_SESSION=users", "TUIOS_PANE_ID=p1"}

	// help is prose
	h := runBin(t, mtm, env, "", "help")
	if h.code != 0 || !strings.Contains(h.out, "Usage:") || !strings.Contains(h.out, "Commands:") {
		t.Fatalf("help: %+v", h)
	}
	if s := strings.TrimSpace(h.out); strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
		t.Fatal("help printed JSON")
	}

	// renamed binary dispatches <bin>-<sub>, args and exit status preserved
	foo := filepath.Join(t.TempDir(), "foo")
	data, err := os.ReadFile(mtm)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foo, data, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, pathDir, "foo-hello", `echo "hi $1 sock=$TUIOS_SOCKET sess=$TUIOS_SESSION srv=$TUIOS_SERVER"; exit 7`)
	r := runBin(t, foo, env, "", "--server", "cmt-cli0", "hello", "there")
	if r.code != 7 || r.out != "hi there sock= sess= srv=cmt-cli0\n" {
		t.Fatalf("renamed dispatch: %+v", r)
	}

	// abbreviated external dispatch: unique prefix runs, ambiguous errors
	writeScript(t, pathDir, "mtm-deploy", `echo "deploy $1"; exit 5`)
	writeScript(t, pathDir, "mtm-dbx", "echo DBX")
	writeScript(t, pathDir, "mtm-dbz", "echo DBZ")
	if r := runBin(t, mtm, env, "", "dep", "prod"); r.code != 5 || r.out != "deploy prod\n" {
		t.Fatalf("prefix dispatch: %+v", r)
	}
	if r := runBin(t, mtm, env, "", "db"); r.code == 0 || !strings.Contains(r.err, "ambiguous") || !strings.Contains(r.err, "mtm-dbx") || !strings.Contains(r.err, "mtm-dbz") {
		t.Fatalf("ambiguous: %+v", r)
	}
	if r := runBin(t, mtm, env, "", "zzznope"); r.code == 0 || !strings.Contains(r.err, "unknown command") {
		t.Fatalf("unknown: %+v", r)
	}

	// built-in json wins over PATH mtm-json (invalid input reaches the built-in parser)
	writeScript(t, pathDir, "mtm-json", "echo EXTERNAL; exit 9")
	j := runBin(t, mtm, env, "{\n \"a\" 1}", "json")
	if j.code == 0 || j.code == 9 || strings.Contains(j.out, "EXTERNAL") || !strings.Contains(j.err, "line 2") {
		t.Fatalf("built-in json: %+v", j)
	}
}
