// Package launch creates terminals, Vim windows and agents on the selected
// server. Quick and form creation share LaunchSpec and these placement rules:
// the session is spec.Session when explicit, else the project's placement
// session ("" → no-project). Creation never focuses, resizes or rearranges
// existing windows (focus:false on every request).
package launch

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/inventory"
	"github.com/unixsuperhero/gmtm/internal/store"
	"github.com/unixsuperhero/gmtm/internal/tuios"
)

// Harnesses recognised by mtm, in preference order.
var Harnesses = []string{"omp", "claude", "codex", "gemini"}

// Option is one catalog entry of a harness setting (model, effort, speed).
// Entries are Recognized only: a catalog entry is not proof of account access.
type Option struct {
	Harness string
	Value   string
	Flags   []string // argv appended to the agent command when chosen
}

// Catalog is the static per-harness option catalog.
var Catalog = struct{ Models, Efforts, Speeds []Option }{
	Models: []Option{
		{"claude", "opus", []string{"--model", "opus"}},
		{"claude", "sonnet", []string{"--model", "sonnet"}},
		{"claude", "haiku", []string{"--model", "haiku"}},
		{"codex", "gpt-5", []string{"--model", "gpt-5"}},
		{"codex", "gpt-5-codex", []string{"--model", "gpt-5-codex"}},
		{"gemini", "gemini-2.5-pro", []string{"--model", "gemini-2.5-pro"}},
		{"gemini", "gemini-2.5-flash", []string{"--model", "gemini-2.5-flash"}},
	},
	Efforts: []Option{
		{"codex", "low", []string{"-c", "model_reasoning_effort=low"}},
		{"codex", "medium", []string{"-c", "model_reasoning_effort=medium"}},
		{"codex", "high", []string{"-c", "model_reasoning_effort=high"}},
	},
	Speeds: nil, // no recognized harness exposes a speed option
}

// Result of a launch. Target is kept even when the prompt step failed, so a
// retry addresses the same window instead of launching another agent.
type Result struct {
	Target  domain.Target
	Outcome domain.Outcome // prompt step; "" when there was no prompt
	Reason  string
}

// Service launches windows through the one client.
type Service struct {
	client   tuios.Client
	inv      *inventory.Service
	store    store.Store
	LookPath func(string) (string, error)
	Timeout  time.Duration
}

// New returns a launch service.
func New(c tuios.Client, inv *inventory.Service, st store.Store) *Service {
	return &Service{client: c, inv: inv, store: st, LookPath: exec.LookPath, Timeout: 150 * time.Second}
}

// Capabilities reports each recognized harness: Installed when its program is
// on PATH; Usable = Installed (no account or compatibility proof is claimed).
func (s *Service) Capabilities() []domain.Capability {
	out := make([]domain.Capability, 0, len(Harnesses))
	for _, h := range Harnesses {
		c := domain.Capability{ID: h, Label: h, Recognized: true}
		if _, err := s.LookPath(h); err == nil {
			c.Installed, c.Usable = true, true
			c.Reason = "installed on PATH; account access not verified"
		} else {
			c.Reason = "not installed (not found on PATH)"
		}
		out = append(out, c)
	}
	return out
}

// Usable reports whether harness h is usable.
func (s *Service) Usable(h string) bool {
	for _, c := range s.Capabilities() {
		if c.ID == h {
			return c.Usable
		}
	}
	return false
}

// DefaultHarness is the first usable harness, or "".
func (s *Service) DefaultHarness() string {
	for _, c := range s.Capabilities() {
		if c.Usable {
			return c.ID
		}
	}
	return ""
}

// SessionFor resolves the placement session of spec.
func SessionFor(spec domain.LaunchSpec) string {
	if spec.Session != "" {
		return spec.Session
	}
	return inventory.PlacementSession(spec.Project)
}

func remote(host string) bool { return host != "" && host != domain.HostLocal }

func lookup(opts []Option, harness, v string) (Option, bool) {
	for _, o := range opts {
		if o.Harness == harness && o.Value == v {
			return o, true
		}
	}
	return Option{}, false
}

// AgentCommand is the start-agent "agent" string: the harness plus catalog
// flags, added only when the harness is usable and the option is in its catalog.
func (s *Service) AgentCommand(spec domain.LaunchSpec) (string, error) {
	h := spec.Harness
	known := false
	for _, x := range Harnesses {
		known = known || x == h
	}
	if !known {
		return "", fmt.Errorf("unsupported harness %q", h)
	}
	parts := []string{h}
	if !s.Usable(h) {
		return "", fmt.Errorf("harness %s is not installed on this machine", h)
	}
	for _, sel := range []struct {
		cat []Option
		v   string
		n   string
	}{{Catalog.Models, spec.Model, "model"}, {Catalog.Efforts, spec.Effort, "effort"}, {Catalog.Speeds, spec.Speed, "speed"}} {
		if sel.v == "" {
			continue
		}
		o, ok := lookup(sel.cat, h, sel.v)
		if !ok {
			return "", fmt.Errorf("%s %q is not supported for %s", sel.n, sel.v, h)
		}
		parts = append(parts, o.Flags...)
	}
	return strings.Join(parts, " "), nil
}

// start-agent env overlays the daemon's environment. Forward only executable
// lookup; TUIOS supplies its own server and pane identity variables.
func envMap(env []string) map[string]string {
	m := make(map[string]string, 1)
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == "PATH" {
			m[k] = v
		}
	}
	return m
}

// Launch creates the window described by spec. A project that has no store
// record yet is created after the window exists (submission creates and uses it).
func (s *Service) Launch(ctx context.Context, spec domain.LaunchSpec) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	session := SessionFor(spec)
	res := Result{Target: domain.Target{Server: s.client.Server(), Host: spec.Host, Session: session}}
	if res.Target.Host == "" {
		res.Target.Host = domain.HostLocal
	}
	host := spec.Host
	if !remote(host) {
		host = ""
	}
	var agent string
	if spec.Kind == domain.KindAgent {
		var err error
		if agent, err = s.AgentCommand(spec); err != nil {
			return Result{}, err
		}
	}
	if err := s.inv.EnsureSession(ctx, session); err != nil {
		return Result{}, fmt.Errorf("ensure session %s: %w", session, err)
	}
	switch spec.Kind {
	case domain.KindTerminal, domain.KindVim:
		cmd := spec.Command
		if spec.Kind == domain.KindVim && len(cmd) == 0 {
			cmd = []string{"vim"}
			if _, err := s.LookPath("nvim"); err == nil {
				cmd = []string{"nvim"}
			}
		}
		w, err := tuios.NewWindow(ctx, s.client, tuios.NewWindowParams{Session: session, Name: spec.Name, CWD: spec.CWD, Focus: false, Command: cmd, Host: host})
		if err != nil {
			return Result{}, err
		}
		res.Target.WindowID = w.WindowID
		if w.Host != "" {
			res.Target.Host = w.Host
		}
		if w.Unplaced {
			res.Reason = "window created but not placed (insufficient space); existing windows untouched"
		}
	case domain.KindAgent:
		p := tuios.StartAgentParams{Session: session, Agent: agent, Name: spec.Name, CWD: spec.CWD, Focus: false, Env: envMap(s.client.ChildEnv())}
		if !spec.Queue {
			p.Prompt = spec.Prompt
		}
		r, err := tuios.StartAgent(ctx, s.client, p)
		res.Target.WindowID = r.WindowID
		switch {
		case err != nil && r.WindowID == "":
			return Result{}, err
		case err != nil:
			res.Outcome, res.Reason = domain.OutcomeFailed, err.Error()
		case r.BlockedBy != "":
			res.Outcome, res.Reason = domain.OutcomeBlocked, "agent blocked by "+r.BlockedBy
		case spec.Prompt == "":
		case spec.Queue:
			res.Outcome, res.Reason = s.queue(ctx, res.Target, spec.Prompt)
		case r.Ready:
			res.Outcome, res.Reason = domain.OutcomeAccepted, "prompt typed; delivery not proven"
		default:
			res.Outcome, res.Reason = domain.OutcomeUnknown, "agent not ready: "+firstNonEmpty(r.Reason, r.Outcome, r.State)
		}
	default:
		return Result{}, fmt.Errorf("unsupported kind %q", spec.Kind)
	}
	if spec.Project != "" && s.store != nil {
		if err := s.ensureProject(ctx, spec.Project); err != nil {
			res.Reason = strings.TrimSpace(res.Reason + "; project record not saved: " + err.Error())
		}
	}
	return res, nil
}

func (s *Service) queue(ctx context.Context, t domain.Target, text string) (domain.Outcome, string) {
	err := s.client.Call(ctx, "queue-prompt", map[string]any{"session": t.Session, "window": t.WindowID, "text": text}, nil)
	if err != nil {
		return domain.OutcomeFailed, "queue failed: " + err.Error()
	}
	return domain.OutcomeWaiting, "queued; typed once the agent is at rest"
}

// RetryPrompt re-sends a prompt to an existing target (never launches).
func (s *Service) RetryPrompt(ctx context.Context, t domain.Target, prompt string, queue bool) (domain.Outcome, string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if queue {
		return s.queue(ctx, t, prompt)
	}
	if err := tuios.SendText(ctx, s.client, t.Session, t.WindowID, prompt, true, true); err != nil {
		return domain.OutcomeFailed, err.Error()
	}
	return domain.OutcomeAccepted, "prompt typed; delivery not proven"
}

func (s *Service) ensureProject(ctx context.Context, name string) error {
	if p, err := s.store.ProjectByName(ctx, name); err == nil && p != nil {
		return nil
	}
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return err
	}
	if err := tx.CreateProject(&domain.Project{Name: name}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return "unknown"
}
