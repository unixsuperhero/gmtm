package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/unixsuperhero/gmtm/internal/tuios"
)

type captureClient struct {
	tuios.Client
	call      func(string, any, any) error
	subscribe func(context.Context) (<-chan tuios.Event, error)
}

func (c captureClient) Server() string { return "clean-mtm" }
func (c captureClient) Call(_ context.Context, verb string, params, out any) error {
	return c.call(verb, params, out)
}
func (c captureClient) Subscribe(ctx context.Context, _ []string, _ int64, _ string) (<-chan tuios.Event, error) {
	return c.subscribe(ctx)
}
func reply(out, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func fixtureClient(t *testing.T, history func() []command, output commandOutput) captureClient {
	t.Helper()
	return captureClient{call: func(verb string, params, out any) error {
		p, _ := params.(map[string]any)
		switch verb {
		case "list-windows":
			return reply(out, map[string]any{"windows": []any{map[string]any{"window_id": "stable-id", "at_prompt": true, "command_seq": 2}}})
		case "command-history":
			if p["session"] != "work" || p["window"] != "stable-id" || p["limit"] != 200 {
				t.Fatalf("unscoped history: %+v", p)
			}
			return reply(out, map[string]any{"commands": history()})
		case "capture-pane":
			if p["source"] != "last-command-output" || p["session"] != "work" || p["window"] != "stable-id" || p["lines"] != MaxOutputLines+1 {
				t.Fatalf("unsafe capture: %+v", p)
			}
			return reply(out, output)
		default:
			return fmt.Errorf("unexpected verb %s", verb)
		}
	}}
}

func terminalCommands() []command {
	zero := 0
	return []command{
		{Session: "work", Window: "stable-id", Seq: 2, Cmdline: "printf second", Exit: &zero, At: 1700000000020, DurationMS: 5},
		{Session: "work", Window: "stable-id", Seq: 1, Cmdline: "printf first", Exit: &zero, At: 1700000000000, DurationMS: 5},
	}
}

func TestSyncCapturesOnlyMatchingStructuredOutput(t *testing.T) {
	r := openRepository(t)
	commands := terminalCommands()
	c := fixtureClient(t, func() []command { return commands }, commandOutput{Source: "last-command-output", Seq: 2, Cmdline: commands[0].Cmdline, Exit: commands[0].Exit, Content: "second\n"})
	s := NewService(c, r)
	for range 2 {
		status, err := s.Sync(context.Background(), agentTurn().Target())
		if err != nil || !status.Supported || status.Captured != 2 {
			t.Fatalf("%+v %v", status, err)
		}
	}
	rows, err := s.List(context.Background(), agentTurn().Target())
	if err != nil || len(rows) != 2 {
		t.Fatalf("%+v %v", rows, err)
	}
	if rows[0].Prompt != "printf second" || rows[0].Response != "second\n" || rows[0].OutputStatus != "captured" {
		t.Fatalf("newest: %+v", rows[0])
	}
	if rows[1].Response != "" || rows[1].OutputStatus != "unavailable" {
		t.Fatalf("older got another command's output: %+v", rows[1])
	}
}

func TestSyncRejectsOutputRaceAndCrossTargetHistory(t *testing.T) {
	for _, mode := range []string{"sequence", "reread", "target", "command", "exit"} {
		t.Run(mode, func(t *testing.T) {
			r := openRepository(t)
			commands := terminalCommands()
			output := commandOutput{Source: "last-command-output", Seq: 2, Cmdline: commands[0].Cmdline, Exit: commands[0].Exit, Content: "unrelated output"}
			if mode == "sequence" {
				output.Seq = 3
			}
			if mode == "command" {
				output.Cmdline = "other"
			}
			if mode == "exit" {
				one := 1
				output.Exit = &one
			}
			if mode == "target" {
				commands[0].Window = "another-pane"
			}
			reads := 0
			c := fixtureClient(t, func() []command {
				reads++
				if mode == "reread" && reads > 1 {
					copyRows := append([]command(nil), commands...)
					copyRows[0].Seq = 3
					return copyRows
				}
				return commands
			}, output)
			_, err := NewService(c, r).Sync(context.Background(), agentTurn().Target())
			if mode == "target" {
				if err == nil {
					t.Fatal("foreign history accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			rows, err := r.List(context.Background(), agentTurn().Target())
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.Response != "" || row.OutputStatus != "unavailable" {
					t.Fatalf("race attached output: %+v", row)
				}
			}
		})
	}
}

func TestSyncReportsUnsupportedWithoutParsingScreen(t *testing.T) {
	r := openRepository(t)
	c := captureClient{call: func(verb string, _, out any) error {
		if verb != "list-windows" {
			t.Fatalf("queried unmarked shell: %s", verb)
		}
		return reply(out, map[string]any{"windows": []any{map[string]any{"window_id": "stable-id"}}})
	}}
	s := NewService(c, r)
	status, err := s.Sync(context.Background(), agentTurn().Target())
	if err != nil || status.Supported || status.Detail == "" {
		t.Fatalf("%+v %v", status, err)
	}
	foreign := agentTurn().Target()
	foreign.Server = "other"
	if _, err := s.Sync(context.Background(), foreign); err == nil {
		t.Fatal("wrong server accepted")
	}
	foreign = agentTurn().Target()
	foreign.Host = "remote"
	status, err = s.Sync(context.Background(), foreign)
	if err != nil || status.Supported {
		t.Fatalf("remote unsupported: %+v %v", status, err)
	}
}

func TestBoundedOutputReportsTruncation(t *testing.T) {
	for _, text := range []string{strings.Repeat("x", MaxTextBytes+1), strings.Repeat("line\n", MaxOutputLines+1)} {
		got, status := boundedOutput(text, false)
		if status != "truncated" || len(got) > MaxTextBytes || strings.Count(got, "\n") >= MaxOutputLines {
			t.Fatalf("bound missing: %d %s", len(got), status)
		}
	}
	if got, status := boundedOutput("exact\n", false); got != "exact\n" || status != "captured" {
		t.Fatal("changed captured output")
	}
}

func TestRunReconnectsAndResynchronizes(t *testing.T) {
	r := openRepository(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	attempts, scans := 0, 0
	c := captureClient{call: func(verb string, _, out any) error {
		if verb != "list-sessions" {
			return fmt.Errorf("unexpected %s", verb)
		}
		scans++
		return reply(out, map[string]any{"sessions": []any{}})
	}, subscribe: func(context.Context) (<-chan tuios.Event, error) {
		attempts++
		ch := make(chan tuios.Event)
		if attempts == 1 {
			close(ch)
		} else {
			cancel()
		}
		return ch, nil
	}}
	err := NewService(c, r).Run(ctx)
	if !errors.Is(err, context.Canceled) || attempts != 2 || scans < 1 {
		t.Fatalf("attempts=%d scans=%d err=%v", attempts, scans, err)
	}
}

func TestListPageReportsBounds(t *testing.T) {
	r := openRepository(t)
	ctx := context.Background()
	v := agentTurn()
	for i := range MaxListTurns + 1 {
		v.ID = fmt.Sprintf("turn-%d", i)
		v.StartedAt = 1700000000000 + int64(i)
		if err := r.Upsert(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	page, err := r.ListPage(ctx, v.Target())
	if err != nil || !page.Limited || len(page.Turns) != MaxListTurns {
		t.Fatalf("page %d limited=%v err=%v", len(page.Turns), page.Limited, err)
	}
	if page.Turns[0].ID != "turn-500" {
		t.Fatalf("not newest-first: %+v", page.Turns[0])
	}
}

func TestSyncAfterMarkWaitsForQueuedCompletion(t *testing.T) {
	r := openRepository(t)
	commands := terminalCommands()
	waited := false
	c := fixtureClient(t, func() []command {
		if !waited {
			t.Fatal("read history before observing queued shell marks")
		}
		return commands
	}, commandOutput{Source: "last-command-output", Seq: 2, Cmdline: commands[0].Cmdline, Exit: commands[0].Exit, Content: "second"})
	call := c.call
	c.call = func(verb string, params, out any) error {
		if verb == "wait-for" {
			p := params.(map[string]any)
			if p["condition"] != "command-finished" || p["session"] != "work" || p["window"] != "stable-id" || p["command_seq"] != float64(2) {
				t.Fatalf("unscoped completion wait: %+v", p)
			}
			waited = true
			// Already-consumed D times out rather than raising another event.
			return &tuios.Error{Code: "timeout", Message: "no later command"}
		}
		return call(verb, params, out)
	}
	status, err := NewService(c, r).SyncAfterMark(context.Background(), agentTurn().Target())
	if err != nil || !waited || status.Captured != 2 {
		t.Fatalf("%+v waited=%v err=%v", status, waited, err)
	}
}

func TestExactShellCommandEnrichesOnlyCorrelatedNewestTurn(t *testing.T) {
	r := openRepository(t)
	commands := terminalCommands()
	commands[0].Cmdline = "printf 'GMTM_COMMAN D_ONE\\n'"
	raw := "printf 'GMTM_COMMAND_ONE\\n'\nprintf 'multiline\\n'"
	c := fixtureClient(t, func() []command { return commands }, commandOutput{
		Source: "last-command-output", Seq: 2, Cmdline: commands[0].Cmdline,
		Exit: commands[0].Exit, Content: "GMTM_COMMAND_ONE\nmultiline\n",
	})
	call := c.call
	c.call = func(verb string, params, out any) error {
		if verb == "wait-for" {
			return &tuios.Error{Code: "timeout", Message: "already finished"}
		}
		return call(verb, params, out)
	}
	service := NewService(c, r)
	ctx := context.Background()
	target := agentTurn().Target()
	if _, err := service.SyncAfterCommand(ctx, target, raw); err != nil {
		t.Fatal(err)
	}
	// An ordinary background read has no raw shell text and must preserve it.
	if _, err := service.Sync(ctx, target); err != nil {
		t.Fatal(err)
	}
	rows, err := r.List(ctx, target)
	if err != nil || len(rows) != 2 {
		t.Fatalf("%+v %v", rows, err)
	}
	if rows[0].RawCommand != raw || rows[0].Prompt != commands[0].Cmdline || rows[0].Response != "GMTM_COMMAND_ONE\nmultiline\n" {
		t.Fatalf("lost raw command or daemon correlation: %+v", rows[0])
	}
	if rows[1].RawCommand != "" {
		t.Fatalf("raw input attached to older turn: %+v", rows[1])
	}
	if _, err := service.SyncAfterCommand(ctx, target, "different exact command"); err == nil {
		t.Fatal("conflicting exact shell command overwrote saved input")
	}
	// A metadata-only replay must preserve both saved output and exact input.
	saved := rows[0]
	saved.RawCommand, saved.Response, saved.OutputStatus = "", "", "unavailable"
	if err := r.Upsert(ctx, saved); err != nil {
		t.Fatal(err)
	}
	rows, err = r.List(ctx, target)
	if err != nil || rows[0].RawCommand != raw || rows[0].Response == "" {
		t.Fatalf("%+v %v", rows, err)
	}
}

func TestExactShellCommandIsNotAttachedToRacingOutput(t *testing.T) {
	r := openRepository(t)
	commands := terminalCommands()
	c := fixtureClient(t, func() []command { return commands }, commandOutput{
		Source: "last-command-output", Seq: 3, Cmdline: commands[0].Cmdline, Exit: commands[0].Exit, Content: "other command",
	})
	call := c.call
	c.call = func(verb string, params, out any) error {
		if verb == "wait-for" {
			return &tuios.Error{Code: "timeout", Message: "already finished"}
		}
		return call(verb, params, out)
	}
	if _, err := NewService(c, r).SyncAfterCommand(context.Background(), agentTurn().Target(), "exact command"); err != nil {
		t.Fatal(err)
	}
	rows, err := r.List(context.Background(), agentTurn().Target())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.RawCommand != "" || row.Response != "" {
			t.Fatalf("racing output enriched: %+v", row)
		}
	}
}
