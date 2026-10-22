package setup

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellRawCommandTransport(t *testing.T) {
	for _, shell := range []string{"zsh", "bash"} {
		t.Run(shell, func(t *testing.T) {
			binary, err := exec.LookPath(shell)
			if err != nil {
				t.Skip(shell + " not installed")
			}
			home := t.TempDir()
			captureBin := filepath.Join(home, "capture arguments")
			if err := os.WriteFile(captureBin, []byte("#!/bin/sh\nprintf '%s\\000' \"$@\" > \"$GMTM_ARGS\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			first, second := filepath.Join(home, "first"), filepath.Join(home, "second")
			exact := "printf '%s\\n' 'GMTM_COMMAND_ONE'\nprintf '%s\\n' 'second line with spaces'"
			var source, invocation string
			var args []string
			if shell == "zsh" {
				source = zshSource
				args = []string{"-f", "-i", "-c"}
				invocation = "_gmtm_capture_preexec " + shellQuote(exact) + "; _gmtm_capture_sync; export GMTM_ARGS=" + shellQuote(second) + "; _gmtm_capture_sync"
			} else {
				source = bashSource
				args = []string{"--noprofile", "--norc", "-i", "-c"}
				invocation = "history -c; history -s " + shellQuote(exact) + "; _GMTM_HISTORY_NUMBER=$((HISTCMD - 1)); _gmtm_capture_sync; export GMTM_ARGS=" + shellQuote(second) + "; _GMTM_HISTORY_NUMBER=$HISTCMD; _gmtm_capture_sync"
			}
			file := filepath.Join(home, "shell."+shell)
			source = strings.NewReplacer("__SERVER__", shellQuote("cmt-raw"), "__BINARY__", shellQuote(captureBin), "__DB__", shellQuote(filepath.Join(home, "db"))).Replace(source)
			if err := os.WriteFile(file, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			invocation = ". " + shellQuote(file) + "; " + invocation
			if shell == "bash" {
				invocation = "(( BASH_VERSINFO[0] > 4 || (BASH_VERSINFO[0] == 4 && BASH_VERSINFO[1] >= 4) )) || exit 77; " + invocation
			}
			cmd := exec.Command(binary, append(args, invocation)...)
			cmd.Env = append(os.Environ(), "HOME="+home, "ZDOTDIR="+home, "TUIOS_SERVER=cmt-raw", "TUIOS_PANE_ID=window-1", "TUIOS_SESSION=work", "GMTM_ARGS="+first, "HISTCONTROL=", "HISTIGNORE=")
			out, err := cmd.CombinedOutput()
			if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 77 {
				t.Skip("bash before 4.4")
			}
			if err != nil {
				t.Fatalf("shell: %v: %s", err, out)
			}
			data, err := os.ReadFile(first)
			if err != nil {
				t.Fatalf("first invocation: %v: %s", err, out)
			}
			want := []byte("--command\x00" + exact + "\x00")
			if !bytes.HasSuffix(data, want) {
				t.Fatalf("raw command changed: %q, want suffix %q", data, want)
			}
			data, err = os.ReadFile(second)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte("--command\x00")) {
				t.Fatalf("stale raw command reused: %q", data)
			}
		})
	}
}
