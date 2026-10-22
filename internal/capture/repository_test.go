package capture

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/store"
)

func openRepository(t *testing.T) *Repository {
	t.Helper()
	r, err := Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func agentTurn() Turn {
	return Turn{ID: "request-1", Server: "clean-mtm", Host: "local", Session: "work", Window: "stable-id", Kind: "agent", Harness: "omp", ConversationID: "conversation-1", Prompt: "full\nuser prompt", StartedAt: 1700000000000, State: "working"}
}

func TestDurableAgentTurnReplayAndCompletion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mtm.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	start := agentTurn()
	for range 2 {
		if err := r.Upsert(ctx, start); err != nil {
			t.Fatal(err)
		}
	}
	finished := start
	finished.State, finished.CompletedAt, finished.Response = "errored", start.StartedAt+100, "Partial answer\nprovider failed"
	if err := r.Upsert(ctx, finished); err != nil {
		t.Fatal(err)
	}
	if err := r.Upsert(ctx, start); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	rows, err := r.List(ctx, start.Target())
	if err != nil || len(rows) != 1 {
		t.Fatalf("%+v %v", rows, err)
	}
	if rows[0].State != "errored" || rows[0].Response != finished.Response || rows[0].CompletedAt != finished.CompletedAt {
		t.Fatalf("lost completion: %+v", rows[0])
	}
	finished.Response = "different replay"
	if err := r.Upsert(ctx, finished); err == nil {
		t.Fatal("conflicting completed response accepted")
	}
}

func TestCaptureTargetAndConversationIsolation(t *testing.T) {
	r := openRepository(t)
	ctx := context.Background()
	base := agentTurn()
	variants := []Turn{base, base, base, base, base, base}
	variants[1].Server = "other"
	variants[2].Host = "remote"
	variants[3].Session = "other"
	variants[4].Window = "other"
	variants[5].ConversationID = "other"
	for _, v := range variants {
		if err := r.Upsert(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := r.List(ctx, base.Target())
	if err != nil || len(rows) != 2 {
		t.Fatalf("isolation failed: %+v %v", rows, err)
	}
	if _, err := r.List(ctx, domain.Target{}); err == nil {
		t.Fatal("implicit all-server history accepted")
	}
	base.Host = ""
	if err := r.Upsert(ctx, base); err == nil {
		t.Fatal("unattributed record accepted")
	}
}

func TestCaptureValidationAndInterruptedState(t *testing.T) {
	r := openRepository(t)
	ctx := context.Background()
	for _, mutate := range []func(*Turn){
		func(v *Turn) { v.Harness = "" }, func(v *Turn) { v.ConversationID = "" }, func(v *Turn) { v.StartedAt = 0 },
		func(v *Turn) { v.State = "interrupted" }, func(v *Turn) { v.CompletedAt = v.StartedAt - 1 },
		func(v *Turn) { v.Response = strings.Repeat("x", MaxTextBytes+1) }, func(v *Turn) { v.State = "unknown" },
	} {
		v := agentTurn()
		mutate(&v)
		if err := r.Upsert(ctx, v); err == nil {
			t.Fatalf("invalid record accepted: state=%s", v.State)
		}
	}
	v := agentTurn()
	v.State = "interrupted"
	v.CompletedAt = v.StartedAt + 1
	v.Response = "partial"
	if err := r.Upsert(ctx, v); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureCoexistsWithApplicationMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Upsert(context.Background(), agentTurn()); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := r.List(context.Background(), agentTurn().Target())
	if err != nil || len(rows) != 1 {
		t.Fatalf("capture lost after app migration: %v %v", rows, err)
	}
}

func TestTerminalMetadataReplayPreservesOutput(t *testing.T) {
	r := openRepository(t)
	v := agentTurn()
	v.Kind = "terminal"
	v.Harness = ""
	v.ConversationID = ""
	v.State = "done"
	v.CompletedAt = v.StartedAt + 1
	v.Response = "actual output"
	v.OutputStatus = "captured"
	if err := r.Upsert(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	v.Response = ""
	v.OutputStatus = "unavailable"
	if err := r.Upsert(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	rows, err := r.List(context.Background(), v.Target())
	if err != nil || len(rows) != 1 || rows[0].Response != "actual output" || rows[0].OutputStatus != "captured" {
		t.Fatalf("%+v %v", rows, err)
	}
}

func TestAgentContinuationPromptsAppendWithoutReplayLoss(t *testing.T) {
	r := openRepository(t)
	ctx := context.Background()
	start := agentTurn()
	if err := r.Upsert(ctx, start); err != nil {
		t.Fatal(err)
	}
	continued := start
	continued.Prompt += "\n\nFollow-up delivered while working"
	if err := r.Upsert(ctx, continued); err != nil {
		t.Fatal(err)
	}
	// A delayed start notification must not erase delivered follow-ups.
	if err := r.Upsert(ctx, start); err != nil {
		t.Fatal(err)
	}
	rows, err := r.List(ctx, start.Target())
	if err != nil || len(rows) != 1 || rows[0].Prompt != continued.Prompt {
		t.Fatalf("lost continuation: %+v %v", rows, err)
	}
	conflict := continued
	conflict.Prompt = "replacement rather than continuation"
	if err := r.Upsert(ctx, conflict); err == nil {
		t.Fatal("working prompt replacement accepted")
	}
	finished := continued
	finished.Prompt += "\n\nFinal delivered input"
	finished.State, finished.CompletedAt, finished.Response = "done", finished.StartedAt+100, "complete answer"
	if err := r.Upsert(ctx, finished); err != nil {
		t.Fatal(err)
	}
	if err := r.Upsert(ctx, continued); err != nil {
		t.Fatal(err)
	}
	if err := r.Upsert(ctx, finished); err != nil {
		t.Fatal(err)
	}
	rows, err = r.List(ctx, start.Target())
	if err != nil || len(rows) != 1 || rows[0].Prompt != finished.Prompt || rows[0].State != "done" {
		t.Fatalf("lost completion: %+v %v", rows, err)
	}
	finished.Prompt += "\nlate mutation"
	if err := r.Upsert(ctx, finished); err == nil {
		t.Fatal("completed prompt mutation accepted")
	}
}

func TestTerminalPromptRemainsImmutable(t *testing.T) {
	r := openRepository(t)
	v := agentTurn()
	v.Kind, v.Harness, v.ConversationID = "terminal", "", ""
	if err := r.Upsert(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	v.Prompt += "\nnot another command"
	if err := r.Upsert(context.Background(), v); err == nil {
		t.Fatal("terminal prompt append accepted")
	}
}
