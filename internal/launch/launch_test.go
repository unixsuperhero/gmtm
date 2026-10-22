package launch

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/inventory"
	"github.com/unixsuperhero/gmtm/internal/store"
	"github.com/unixsuperhero/gmtm/internal/tuios"
	"github.com/unixsuperhero/gmtm/internal/tuios/tuiostest"
)

func setup(t *testing.T, reply map[string]any) (*Service, *tuiostest.Daemon, store.Store) {
	d := tuiostest.Start(t)
	d.Handle("new-session", tuiostest.Result(map[string]any{}))
	d.Handle("list-windows", tuiostest.Result(map[string]any{"windows": []any{}}))
	d.Handle("list-sessions", tuiostest.Result(map[string]any{"sessions": []any{}}))
	d.Handle("list-agents", tuiostest.Result(map[string]any{"agents": []any{}}))
	d.Handle("start-agent", tuiostest.Result(reply))
	d.Handle("queue-prompt", tuiostest.Result(map[string]any{}))
	env := []string{"TUIOS_SOCKET=/x", "HOME=/h", "PATH=/custom/bin:/usr/bin"}
	for i := 0; i < 100; i++ {
		env = append(env, "CALLER_VAR_"+strconv.Itoa(i)+"=value")
	}
	c, err := tuios.NewWithOptions("clean-mtm", tuios.Options{SocketPath: d.Path, Environ: func() []string { return env }})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(c, inventory.New(c), st)
	s.LookPath = func(p string) (string, error) { return "/bin/" + p, nil }
	return s, d, st
}

func TestAgentOutcomes(t *testing.T) {
	for _, tc := range []struct {
		reply map[string]any
		queue bool
		want  domain.Outcome
	}{
		{map[string]any{"window_id": "w1", "ready": true}, false, domain.OutcomeAccepted},
		{map[string]any{"window_id": "w1", "ready": false, "blocked_by": "trust"}, false, domain.OutcomeBlocked},
		{map[string]any{"window_id": "w1", "ready": true}, true, domain.OutcomeWaiting},
	} {
		s, d, st := setup(t, tc.reply)
		res, err := s.Launch(context.Background(), domain.LaunchSpec{Kind: domain.KindAgent, Harness: "codex", Model: "gpt-5", Effort: "high", Project: "web", Prompt: "go", Queue: tc.queue})
		if err != nil || res.Outcome != tc.want || res.Target.WindowID != "w1" || res.Target.Session != "web" {
			t.Fatalf("%v: %+v %v", tc.reply, res, err)
		}
		var p struct {
			Agent  string
			Prompt string
			Env    map[string]string
		}
		for _, r := range d.Requests() {
			if r.Verb == "start-agent" {
				_ = json.Unmarshal(r.Params, &p)
			}
		}
		if !strings.Contains(p.Agent, "--model gpt-5") || !strings.Contains(p.Agent, "model_reasoning_effort=high") {
			t.Fatalf("agent %q", p.Agent)
		}
		if len(p.Env) != 1 || p.Env["PATH"] != "/custom/bin:/usr/bin" {
			t.Fatalf("env %v", p.Env)
		}
		if _, exists := p.Env["TUIOS_SERVER"]; exists {
			t.Fatal("agent launch overrides daemon-owned TUIOS_SERVER")
		}
		if tc.queue == (p.Prompt != "") {
			t.Fatalf("prompt sent %q with queue=%v", p.Prompt, tc.queue)
		}
		if pr, _ := st.ProjectByName(context.Background(), "web"); pr == nil {
			t.Fatal("project not created with the launch")
		}
	}
}

func TestUnsupportedOptionsRejected(t *testing.T) {
	s, _, _ := setup(t, map[string]any{})
	if _, err := s.Launch(context.Background(), domain.LaunchSpec{Kind: domain.KindAgent, Harness: "claude", Effort: "high"}); err == nil {
		t.Fatal("claude effort accepted")
	}
	if _, err := s.Launch(context.Background(), domain.LaunchSpec{Kind: domain.KindAgent, Harness: "nope"}); err == nil {
		t.Fatal("unknown harness accepted")
	}
}
