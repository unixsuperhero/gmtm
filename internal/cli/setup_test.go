package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/gmtm/internal/capture"
	"github.com/unixsuperhero/gmtm/internal/domain"
)

func TestCaptureRecordPersistsAndRejectsCrossServer(t *testing.T) {
	db := filepath.Join(t.TempDir(), "capture.db")
	turn := capture.Turn{ID: "cli-turn", Server: "cmt-cli", Host: "local", Session: "work", Window: "window-1", Kind: "agent", Harness: "omp", ConversationID: "conversation-1", Prompt: "full prompt\nsecond line", Response: "full response\nsecond line", StartedAt: 1000, CompletedAt: 2000, State: "done"}
	data, err := json.Marshal(turn)
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	args := []string{"gmtm", "--server", "cmt-cli", "--db", db, "capture", "record"}
	if code := Main(args, bytes.NewReader(data), &out, &stderr); code != 0 {
		t.Fatalf("capture: %d %s", code, &stderr)
	}
	repo, err := capture.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	turns, err := repo.List(context.Background(), domain.Target{Server: turn.Server, Host: turn.Host, Session: turn.Session, WindowID: turn.Window})
	if err != nil || len(turns) != 1 || turns[0].Prompt != turn.Prompt || turns[0].Response != turn.Response {
		t.Fatalf("turns=%+v error=%v", turns, err)
	}
	args[2] = "cmt-other"
	stderr.Reset()
	if code := Main(args, bytes.NewReader(data), &out, &stderr); code == 0 || !strings.Contains(stderr.String(), "does not match") {
		t.Fatalf("cross-server accepted: %d %s", code, &stderr)
	}
}

func TestCaptureRecordStrictJSON(t *testing.T) {
	db := filepath.Join(t.TempDir(), "capture.db")
	for _, input := range []string{`{"unknown":1}`, `{} {}`, `null`, `[]`, `{"server":"clean-mtm"}`, `{"server":"clean-mtm"} trailing`} {
		var out, stderr bytes.Buffer
		if code := Main([]string{"gmtm", "--db", db, "capture", "record"}, strings.NewReader(input), &out, &stderr); code == 0 {
			t.Errorf("accepted %q", input)
		}
	}
	for _, args := range [][]string{{"capture", "sync"}, {"capture", "record", "extra"}, {"capture", "unknown"}, {"setup", "unknown"}} {
		if code, _, _ := run(append([]string{"gmtm"}, args...)...); code == 0 {
			t.Errorf("accepted %v", args)
		}
	}
}

func TestSetupCLIInstallStatusAndUninstall(t *testing.T) {
	home, tools := t.TempDir(), t.TempDir()
	script(t, tools, "tuios", "exit 0")
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	db := filepath.Join(home, "capture.db")
	base := []string{"gmtm", "--server", "cmt-cli", "--db", db, "setup"}
	for _, action := range []string{"install", "status"} {
		code, out, stderr := run(append(append([]string{}, base...), action, "--home", home)...)
		if code != 0 || !strings.Contains(out, "omp: installed") || !strings.Contains(out, filepath.Join(home, ".config", "gmtm")) {
			t.Fatalf("%s: %d %s %s", action, code, out, stderr)
		}
	}
	if code, _, stderr := run(append(append([]string{}, base...), "uninstall", "--home", home)...); code != 0 {
		t.Fatalf("uninstall: %d %s", code, stderr)
	}
}
