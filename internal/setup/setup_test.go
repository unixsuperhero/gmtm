package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/gmtm/internal/tuios"
)

func testManager(t *testing.T, home, server string) *Manager {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tuios"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	m, err := New(Options{Server: server, Home: home, Binary: filepath.Join(home, "bin", "gmtm's command"), DB: filepath.Join(home, "capture db.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestInstallIdempotentAndReversible(t *testing.T) {
	home := t.TempDir()
	original := []byte("# user config, no trailing newline\nexport USER_SETTING=keep")
	for _, rc := range []string{".bashrc", ".zshrc"} {
		if err := os.WriteFile(filepath.Join(home, rc), original, 0640); err != nil {
			t.Fatal(err)
		}
	}
	m := testManager(t, home, "cmt-setup")
	for range 2 {
		items, err := m.Install()
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.Status != "installed" {
				t.Fatalf("%+v", item)
			}
		}
	}
	for _, f := range m.files {
		data, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "__SERVER__") || strings.Contains(string(data), "__DAEMON_SERVER__") || strings.Contains(string(data), "__BINARY__") || strings.Contains(string(data), "__DB__") {
			t.Fatalf("unexpanded template: %s", f.path)
		}
		if f.rc != "" {
			rc, err := os.ReadFile(f.rc)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Count(rc, []byte(f.sourceLine())) != 1 || !bytes.HasPrefix(rc, original) {
				t.Fatalf("rc changed unexpectedly: %s", rc)
			}
		}
	}
	if _, err := m.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Uninstall(); err != nil {
		t.Fatal(err)
	}
	for _, rc := range []string{".bashrc", ".zshrc"} {
		path := filepath.Join(home, rc)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, original) {
			t.Fatalf("rc did not round-trip: %q", got)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0640 {
			t.Fatalf("mode changed: %v %v", info, err)
		}
	}
}

func TestInstallPreservesMatomeruAndOtherServers(t *testing.T) {
	home := t.TempDir()
	m := testManager(t, home, "cmt-one")
	legacy := filepath.Join(filepath.Dir(m.files[0].path), "matomeru-omp.ts")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("user's matomeru integration"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(); err != nil {
		t.Fatal(err)
	}
	second := testManager(t, home, "cmt-two")
	// Keep the same resolved tuios path so the first manager remains unchanged.
	if _, err := second.Install(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(legacy); err != nil || string(data) != "user's matomeru integration" {
		t.Fatalf("legacy modified: %q %v", data, err)
	}
	items, err := second.Status()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Status != "installed" {
			t.Fatalf("other server damaged: %+v", item)
		}
	}
}

func TestInstallRefusesUnownedAndModifiedFiles(t *testing.T) {
	for _, owned := range []bool{false, true} {
		t.Run(map[bool]string{false: "unowned", true: "modified"}[owned], func(t *testing.T) {
			home := t.TempDir()
			m := testManager(t, home, "cmt-safe")
			f := m.files[0]
			if err := os.MkdirAll(filepath.Dir(f.path), 0700); err != nil {
				t.Fatal(err)
			}
			content := "user customization"
			if owned {
				content = f.marker + content
			}
			if err := os.WriteFile(f.path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Install(); err == nil {
				t.Fatal("overwrote existing file")
			}
			if data, _ := os.ReadFile(f.path); string(data) != content {
				t.Fatal("existing file changed")
			}
			if _, err := os.Stat(filepath.Join(home, ".bashrc")); !os.IsNotExist(err) {
				t.Fatal("preflight wrote unrelated rc")
			}
			if _, err := m.EditPath("omp"); (err == nil) != owned {
				t.Fatalf("edit ownership: %v", err)
			}
			if _, err := m.Uninstall(); (err == nil) != owned {
				t.Fatalf("uninstall ownership: %v", err)
			}
		})
	}
}

func TestSetupRefusesSymlinkRC(t *testing.T) {
	home := t.TempDir()
	m := testManager(t, home, "cmt-link")
	target := filepath.Join(t.TempDir(), "realrc")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".zshrc")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(); err == nil {
		t.Fatal("accepted symlink rc")
	}
	if data, _ := os.ReadFile(target); string(data) != "keep" {
		t.Fatal("symlink target changed")
	}
	if _, err := os.Stat(m.files[0].path); !os.IsNotExist(err) {
		t.Fatal("preflight created extension")
	}
}

func TestSetupIsolatedHomeIgnoresOMPOverrides(t *testing.T) {
	home, elsewhere := t.TempDir(), t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", elsewhere)
	t.Setenv("ZDOTDIR", elsewhere)
	t.Setenv("GMTM_SETUP_HOME", home)
	m, err := New(Options{Server: "cmt-env", DB: filepath.Join(home, "db")})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range m.files {
		if !strings.HasPrefix(f.path, home+string(filepath.Separator)) || strings.Contains(f.rc, elsewhere) {
			t.Fatalf("escaped isolated home: %+v", f)
		}
	}
}

func TestSetupPinsServerAndAbsoluteRuntime(t *testing.T) {
	m := testManager(t, t.TempDir(), "cmt-pin")
	omp := m.files[0].source
	for _, want := range []string{`"server":"cmt-pin"`, `"--server", capture.server`, `"capture", "record"`, `state: "interrupted"`, `last.stopReason === "error"`} {
		if !strings.Contains(omp, want) {
			t.Errorf("extension missing %q", want)
		}
	}
	for _, f := range m.files[1:] {
		if !strings.Contains(f.source, "== 'cmt-pin'") || !strings.Contains(f.source, "capture sync --session") {
			t.Fatalf("unscoped shell: %s", f.source)
		}
	}
	for _, server := range []string{"", "bad/server", "waytoolongservername"} {
		if _, err := New(Options{Server: server}); err == nil {
			t.Fatalf("accepted invalid server %q", server)
		}
	}
}

func TestDefaultSetupCoexistsWithNamedDefault(t *testing.T) {
	home := t.TempDir()
	unnamed := testManager(t, home, tuios.DefaultDaemon)
	named := testManager(t, home, "default")
	for i, f := range unnamed.files {
		if f.path == named.files[i].path || !strings.Contains(f.path, tuios.DefaultDaemon) {
			t.Fatalf("default identity collided: %s", f.path)
		}
	}
	for _, m := range []*Manager{unnamed, named} {
		if _, err := m.Install(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := unnamed.Uninstall(); err != nil {
		t.Fatal(err)
	}
	items, err := named.Status()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Status != "installed" {
			t.Fatalf("named default damaged: %+v", item)
		}
	}
}
