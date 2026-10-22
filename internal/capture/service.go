package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/tuios"
)

const MaxOutputLines = 4096

// Status distinguishes unsupported integration from an empty successful read.
// Older history is metadata-only: TUIOS exposes output only for the last command.
type Status struct {
	Supported bool   `json:"supported"`
	Detail    string `json:"detail"`
	Captured  int    `json:"captured"`
}

type Service struct {
	client    tuios.Client
	repo      *Repository
	mu        sync.RWMutex
	runStatus Status
}

func NewService(client tuios.Client, repo *Repository) *Service {
	return &Service{client: client, repo: repo}
}

func (s *Service) List(ctx context.Context, target domain.Target) ([]Turn, error) {
	if target.Server != s.client.Server() {
		return nil, errors.New("capture target does not belong to selected server")
	}
	return s.repo.List(ctx, target)
}

func (s *Service) ListPage(ctx context.Context, target domain.Target) (Page, error) {
	if target.Server != s.client.Server() {
		return Page{}, errors.New("capture target does not belong to selected server")
	}
	return s.repo.ListPage(ctx, target)
}

// RunStatus reports live collection health, including transient disconnects.
func (s *Service) RunStatus() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runStatus
}

func (s *Service) setRunStatus(status Status) {
	s.mu.Lock()
	s.runStatus = status
	s.mu.Unlock()
}

type command struct {
	Session    string `json:"session"`
	Window     string `json:"window"`
	Seq        uint64 `json:"seq"`
	Cmdline    string `json:"cmdline"`
	Exit       *int   `json:"exit"`
	At         int64  `json:"at"`
	DurationMS int64  `json:"duration_ms"`
}

type commandOutput struct {
	Content   string `json:"content"`
	Source    string `json:"source"`
	Cmdline   string `json:"cmdline"`
	Seq       uint64 `json:"command_seq"`
	Exit      *int   `json:"exit_code"`
	Truncated bool   `json:"truncated"`
}

func (s *Service) history(ctx context.Context, t domain.Target) ([]command, error) {
	var reply struct {
		Commands []command `json:"commands"`
	}
	if err := s.client.Call(ctx, "command-history", map[string]any{"session": t.Session, "window": t.WindowID, "limit": 200}, &reply); err != nil {
		return nil, err
	}
	if len(reply.Commands) > 200 {
		return nil, errors.New("daemon command-history exceeded requested limit")
	}
	for _, c := range reply.Commands {
		if c.Session != t.Session || c.Window != t.WindowID {
			return nil, errors.New("daemon returned command history for another target")
		}
		if c.Seq == 0 || c.At <= 0 || c.DurationMS < 0 || c.DurationMS > (1<<63-1)-c.At {
			return nil, errors.New("daemon returned invalid command identity or timestamps")
		}
	}
	return reply.Commands, nil
}

func unsupported(err error) bool {
	return tuios.IsCode(err, "unknown_verb") || tuios.IsCode(err, "no_shell_integration")
}

// Sync imports OSC 133 command facts. It never assigns screen/scrollback text to
// a command. The output's sequence, command and exit status must match history,
// and the newest sequence must still match after capture. Races stay unavailable.
// Remote panes are explicitly unsupported rather than accidentally read locally.
func (s *Service) Sync(ctx context.Context, target domain.Target) (Status, error) {
	return s.sync(ctx, target, false, "")
}

// SyncAfterMark is for a shell hook just after writing OSC 133 D. Observing a
// bounded command-finished wait lets the PTY reader consume queued marks even
// when the initial RPC still sees the previous prompt. It waits at most 200ms
// when the daemon has already consumed D; it never sleeps or parses screen text.
func (s *Service) SyncAfterMark(ctx context.Context, target domain.Target) (Status, error) {
	return s.sync(ctx, target, true, "")
}

// SyncAfterCommand records exact shell input alongside the daemon's command
// reconstruction. Only the newest sequence with correlated output is enriched.
// The shell must invoke this synchronously after D and before another command.
func (s *Service) SyncAfterCommand(ctx context.Context, target domain.Target, rawCommand string) (Status, error) {
	return s.sync(ctx, target, true, rawCommand)
}

func (s *Service) sync(ctx context.Context, target domain.Target, afterMark bool, rawCommand string) (Status, error) {
	status := Status{}
	if len(rawCommand) > MaxTextBytes {
		return status, errors.New("shell command exceeds capture size limit")
	}
	if err := validateTarget(target); err != nil {
		return status, err
	}
	if target.Server != s.client.Server() {
		return status, errors.New("capture target does not belong to selected server")
	}
	if target.Host != domain.HostLocal {
		status.Detail = "Terminal capture on linked hosts is unsupported; capture on that host explicitly."
		return status, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// Resolve the full stable id and host before asking an API that also accepts
	// display names. A moved/closed pane must never silently resolve to another.
	windows, err := tuios.ListWindows(ctx, s.client, target.Session)
	if err != nil {
		return status, err
	}
	var pane *tuios.WindowInfo
	for i := range windows.Windows {
		if windows.Windows[i].WindowID == target.WindowID {
			pane = &windows.Windows[i]
			break
		}
	}
	if pane == nil {
		return status, &tuios.Error{Code: tuios.CodeWindowNotFound, Message: "capture target stable window ID is absent"}
	}
	if pane.Host != "" && pane.Host != domain.HostLocal {
		return status, errors.New("capture target host does not match daemon pane")
	}
	if pane.AtPrompt == nil {
		status.Detail = "Shell command marks unavailable. Install setup and open a new supported shell."
		return status, nil
	}
	if promptOnly, _ := pane.Raw["prompt_marks_only"].(bool); promptOnly {
		status.Detail = "Shell marks prompts but not commands; bash requires version 4.4 or newer."
		return status, nil
	}
	// A shell hook emits D then calls sync before emitting A. Permit the PTY
	// reader to consume D, without stalling an ordinary UI sync on a long job.
	if afterMark || !*pane.AtPrompt {
		settle, stop := context.WithTimeout(ctx, 250*time.Millisecond)
		var ignored any
		err := s.client.Call(settle, "wait-for", map[string]any{"condition": "command-finished", "session": target.Session, "window": target.WindowID, "command_seq": pane.Raw["command_seq"], "timeout": 200}, &ignored)
		stop()
		if err != nil && ctx.Err() != nil {
			return status, ctx.Err()
		}
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !tuios.IsCode(err, "timeout") {
			return status, err
		}
	}
	commands, err := s.history(ctx, target)
	if unsupported(err) {
		status.Detail = "Daemon does not expose structured command history; upgrade TUIOS."
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.Supported = true
	status.Detail = "OSC 133 command history; older missed outputs are unavailable."
	if len(commands) == 0 {
		status.Detail = "Shell integration detected; no finished commands yet."
		return status, nil
	}
	var output commandOutput
	err = s.client.Call(ctx, "capture-pane", map[string]any{"session": target.Session, "window": target.WindowID, "source": "last-command-output", "lines": MaxOutputLines + 1}, &output)
	if err != nil && !unsupported(err) {
		return status, err
	}
	hasOutput := err == nil && output.Source == "last-command-output"
	if hasOutput {
		latest, err := s.history(ctx, target)
		if err != nil {
			return status, err
		}
		hasOutput = len(latest) != 0 && latest[0].Seq == output.Seq && latest[0].At == commands[0].At && commands[0].Seq == output.Seq
	}
	for _, c := range commands {
		t := Turn{ID: fmt.Sprintf("command:%d:%d", c.At, c.Seq), Server: target.Server, Host: target.Host, Session: target.Session, Window: target.WindowID,
			Kind: "terminal", Prompt: c.Cmdline, StartedAt: c.At, CompletedAt: c.At + c.DurationMS, State: "done", ExitCode: c.Exit,
			Source: "tuios-osc133", OutputStatus: "unavailable", CommandSeq: c.Seq}
		if c.Exit != nil && *c.Exit != 0 {
			t.State = "errored"
			if *c.Exit == 130 || *c.Exit == 143 {
				t.State = "interrupted"
			}
		}
		if hasOutput && c.Seq == output.Seq && c.Cmdline == output.Cmdline && sameExit(c.Exit, output.Exit) {
			t.Response, t.OutputStatus = boundedOutput(output.Content, output.Truncated)
			t.RawCommand = rawCommand
		}
		if err := s.repo.Upsert(ctx, t); err != nil {
			return status, err
		}
		status.Captured++
	}
	return status, nil
}

func boundedOutput(text string, truncated bool) (string, string) {
	// Request one extra line so hitting our line limit is never labelled complete.
	if strings.Count(text, "\n") >= MaxOutputLines {
		n := 0
		for i := range len(text) {
			if text[i] == '\n' {
				n++
				if n == MaxOutputLines {
					text = text[:i]
					break
				}
			}
		}
		truncated = true
	}
	if len(text) > MaxTextBytes {
		text = text[:MaxTextBytes]
		for !utf8.ValidString(text) && len(text) > 0 {
			text = text[:len(text)-1]
		}
		truncated = true
	}
	if truncated {
		return text, "truncated"
	}
	return text, "captured"
}

// Run collects completions while open; installed shell hooks also collect while
// closed. Disconnects resubscribe with bounded backoff, gaps resync the inventory.
// RunStatus makes degraded capture observable without stopping the collector.
func (s *Service) Run(ctx context.Context) error {
	delay := 250 * time.Millisecond
	for ctx.Err() == nil {
		subctx, cancel := context.WithCancel(ctx)
		events, err := s.client.Subscribe(subctx, []string{"command-finished"}, 0, "")
		if err == nil {
			err = s.resync(subctx)
			if err == nil {
				s.setRunStatus(Status{Supported: true, Detail: "Terminal capture subscription connected."})
				delay = 250 * time.Millisecond
			}
			for err == nil {
				select {
				case <-ctx.Done():
					err = ctx.Err()
				case e, ok := <-events:
					if !ok {
						err = errors.New("terminal capture disconnected")
						break
					}
					if e.Type == "gap" {
						err = s.resync(subctx)
						continue
					}
					if e.Type != "command-finished" {
						continue
					}
					var provenance struct {
						Host string `json:"host"`
					}
					if len(e.Raw) != 0 {
						if err = json.Unmarshal(e.Raw, &provenance); err != nil {
							break
						}
					}
					if provenance.Host != "" && provenance.Host != domain.HostLocal {
						continue
					}
					target := domain.Target{Server: s.client.Server(), Host: domain.HostLocal, Session: e.Session, WindowID: e.Window}
					_, syncErr := s.Sync(subctx, target)
					if syncErr != nil && !tuios.IsCode(syncErr, tuios.CodeWindowNotFound) && !tuios.IsCode(syncErr, tuios.CodeSessionNotFound) {
						// Preserve collection for other panes; make failures visible.
						s.setRunStatus(Status{Detail: "Terminal capture: " + syncErr.Error()})
					}
				}
			}
		}
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.setRunStatus(Status{Detail: fmt.Sprintf("Terminal capture reconnecting: %v", err)})
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay = min(delay*2, 5*time.Second)
	}
	return ctx.Err()
}

func (s *Service) resync(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	sessions, err := tuios.ListSessions(ctx, s.client)
	if err != nil {
		return err
	}
	count := 0
	for _, session := range sessions {
		windows, err := tuios.ListWindows(ctx, s.client, session.Name)
		if tuios.IsCode(err, tuios.CodeSessionNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		for _, w := range windows.Windows {
			if w.Host != "" && w.Host != domain.HostLocal {
				continue
			}
			if w.AtPrompt == nil {
				continue
			}
			count++
			if count > 1000 {
				return errors.New("terminal resync exceeds 1000 panes; shell hooks still capture independently")
			}
			_, err := s.Sync(ctx, domain.Target{Server: s.client.Server(), Host: domain.HostLocal, Session: session.Name, WindowID: w.WindowID})
			if tuios.IsCode(err, tuios.CodeWindowNotFound) || tuios.IsCode(err, tuios.CodeSessionNotFound) {
				continue
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}
