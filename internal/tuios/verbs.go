package tuios

import (
	"context"
	"encoding/json"
	"errors"
)

// Stable daemon error codes mtm branches on.
const (
	CodeSessionExists   = "session_exists"
	CodeSessionNotFound = "session_not_found"
	CodeWindowNotFound  = "window_not_found"
	CodeSameSession     = "same_session"
	CodePaneNotMovable  = "pane_not_movable"
	CodeBusy            = "busy"
	CodeTooManyConns    = "too_many_connections"
)

// IsCode reports whether err is a daemon *Error with the given code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// rawHolder lets decode helpers keep every source field next to the typed ones.
type rawHolder interface{ setRaw(map[string]any) }

func decodeOne[T any, PT interface {
	*T
	rawHolder
}](b json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return v, err
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err == nil {
		PT(&v).setRaw(raw)
	}
	return v, nil
}

func decodeList[T any, PT interface {
	*T
	rawHolder
}](items []json.RawMessage) ([]T, error) {
	out := make([]T, 0, len(items))
	for _, it := range items {
		v, err := decodeOne[T, PT](it)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func callRaw[T any, PT interface {
	*T
	rawHolder
}](ctx context.Context, c Client, verb string, params any) (T, error) {
	var res json.RawMessage
	if err := c.Call(ctx, verb, params, &res); err != nil {
		var zero T
		return zero, err
	}
	return decodeOne[T, PT](res)
}

// ---- sessions ----

// SessionInfo is one list-sessions row.
type SessionInfo struct {
	Name             string         `json:"name"`
	ID               string         `json:"id"`
	Attached         bool           `json:"attached"`
	Created          int64          `json:"created"`
	LastActive       int64          `json:"last_active"`
	WindowCount      int            `json:"window_count"`
	Width            int            `json:"width"`
	Height           int            `json:"height"`
	CurrentWorkspace int            `json:"current_workspace"`
	Raw              map[string]any `json:"-"`
}

func (s *SessionInfo) setRaw(m map[string]any) { s.Raw = m }

// ListSessions returns every session on the server.
func ListSessions(ctx context.Context, c Client) ([]SessionInfo, error) {
	var res struct {
		Sessions []json.RawMessage `json:"sessions"`
	}
	if err := c.Call(ctx, "list-sessions", nil, &res); err != nil {
		return nil, err
	}
	return decodeList[SessionInfo](res.Sessions)
}

// SessionCreated is the new-session reply.
type SessionCreated struct {
	Session   string         `json:"session"`
	SessionID string         `json:"session_id"`
	Windows   int            `json:"windows"`
	Width     int            `json:"width"`
	Height    int            `json:"height"`
	Raw       map[string]any `json:"-"`
}

func (s *SessionCreated) setRaw(m map[string]any) { s.Raw = m }

// NewSession creates a session. window=false makes an empty session.
func NewSession(ctx context.Context, c Client, name string, window bool, cwd string) (SessionCreated, error) {
	p := map[string]any{"name": name, "window": window}
	if cwd != "" {
		p["cwd"] = cwd
	}
	return callRaw[SessionCreated](ctx, c, "new-session", p)
}

// RenameSession renames a session.
func RenameSession(ctx context.Context, c Client, session, name string) error {
	return c.Call(ctx, "rename-session", map[string]any{"session": session, "name": name}, nil)
}

// KillSession terminates a session and every window in it.
func KillSession(ctx context.Context, c Client, session string) error {
	return c.Call(ctx, "kill-session", map[string]any{"session": session}, nil)
}

// ---- windows ----

// WindowInfo is one list-windows row.
type WindowInfo struct {
	WindowID       string         `json:"window_id"`
	PtyID          string         `json:"pty_id"`
	CustomName     string         `json:"custom_name"`
	DisplayName    string         `json:"display_name"`
	Title          string         `json:"title"`
	CWD            string         `json:"cwd"`
	AgentState     string         `json:"agent_state"`
	Focused        bool           `json:"focused"`
	Width          int            `json:"width"`
	Height         int            `json:"height"`
	PID            int            `json:"pid"`
	TTY            string         `json:"tty"`
	Host           string         `json:"host"`
	Workspace      int            `json:"workspace"`
	Minimized      bool           `json:"minimized"`
	Index          int            `json:"index"`
	AtPrompt       *bool          `json:"at_prompt"`
	RunningCmdline string         `json:"running_cmdline"`
	LastCmdline    string         `json:"last_cmdline"`
	LastExitCode   *int           `json:"last_exit_code"`
	Raw            map[string]any `json:"-"`
}

func (w *WindowInfo) setRaw(m map[string]any) { w.Raw = m }

// WindowList is the list-windows reply.
type WindowList struct {
	Windows         []WindowInfo
	FocusedWindowID string
	Shell           string
	Total           int
}

// ListWindows lists the windows of one session.
func ListWindows(ctx context.Context, c Client, session string) (WindowList, error) {
	var res struct {
		Windows         []json.RawMessage `json:"windows"`
		FocusedWindowID string            `json:"focused_window_id"`
		Shell           string            `json:"shell"`
		Total           int               `json:"total"`
	}
	if err := c.Call(ctx, "list-windows", map[string]any{"session": session}, &res); err != nil {
		return WindowList{}, err
	}
	ws, err := decodeList[WindowInfo](res.Windows)
	if err != nil {
		return WindowList{}, err
	}
	return WindowList{Windows: ws, FocusedWindowID: res.FocusedWindowID, Shell: res.Shell, Total: res.Total}, nil
}

// NewWindowParams are new-window's parameters. Focus is always sent (the daemon
// defaults to true; mtm must not steal focus unless asked).
type NewWindowParams struct {
	Session     string   `json:"session"`
	Name        string   `json:"name,omitempty"`
	Workspace   int      `json:"workspace,omitempty"`
	CWD         string   `json:"cwd,omitempty"`
	Focus       bool     `json:"focus"`
	Command     []string `json:"command,omitempty"`
	Host        string   `json:"host,omitempty"`
	Grants      []string `json:"grants,omitempty"`
	CloseOnExit bool     `json:"close_on_exit,omitempty"`
}

// WindowCreated is the new-window reply.
type WindowCreated struct {
	WindowID  string         `json:"window_id"`
	PtyID     string         `json:"pty_id"`
	Name      string         `json:"name"`
	Workspace int            `json:"workspace"`
	Focused   bool           `json:"focused"`
	Host      string         `json:"host"`
	Unplaced  bool           `json:"unplaced"`
	Raw       map[string]any `json:"-"`
}

func (w *WindowCreated) setRaw(m map[string]any) { w.Raw = m }

// NewWindow creates a window.
func NewWindow(ctx context.Context, c Client, p NewWindowParams) (WindowCreated, error) {
	return callRaw[WindowCreated](ctx, c, "new-window", p)
}

// CloseWindow closes one window by id.
func CloseWindow(ctx context.Context, c Client, session, window string) error {
	return c.Call(ctx, "close-window", map[string]any{"session": session, "window": window}, nil)
}

// MoveWindow moves a window to another session, keeping its id and scrollback.
func MoveWindow(ctx context.Context, c Client, session, window, toSession string) error {
	return c.Call(ctx, "move-window", map[string]any{"session": session, "window": window, "to_session": toSession}, nil)
}

// CapturePane returns a pane's content. source is "visible" (default), "recent"
// or "last-command-output"; lines<=0 means all.
func CapturePane(ctx context.Context, c Client, session, window, source string, lines int, styled bool) (string, error) {
	p := map[string]any{"session": session, "window": window, "styled": styled}
	if source != "" {
		p["source"] = source
	}
	if lines > 0 {
		p["lines"] = lines
	}
	var res struct {
		Content string `json:"content"`
	}
	if err := c.Call(ctx, "capture-pane", p, &res); err != nil {
		return "", err
	}
	return res.Content, nil
}

// SendKeys sends key names (or literal bytes when literal) to a window.
func SendKeys(ctx context.Context, c Client, session, window, keys string, literal bool) error {
	p := map[string]any{"session": session, "window": window, "keys": keys}
	if literal {
		p["literal"] = true
	}
	return c.Call(ctx, "send-keys", p, nil)
}

// SendText writes text to a window's PTY. Acceptance is not proof of delivery.
func SendText(ctx context.Context, c Client, session, window, text string, paste, submit bool) error {
	return c.Call(ctx, "send-text", map[string]any{"session": session, "window": window, "text": text, "paste": paste, "submit": submit}, nil)
}

// ---- agents ----

// StartAgentParams are start-agent's parameters. The caller's ctx must outlive ReadyTimeoutMS.
type StartAgentParams struct {
	Session        string            `json:"session,omitempty"`
	Agent          string            `json:"agent"`
	Args           []string          `json:"args,omitempty"`
	Name           string            `json:"name,omitempty"`
	CWD            string            `json:"cwd,omitempty"`
	Workspace      int               `json:"workspace,omitempty"`
	Focus          bool              `json:"focus"`
	Prompt         string            `json:"prompt,omitempty"`
	ReadyTimeoutMS int               `json:"ready_timeout,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Protocol       string            `json:"protocol,omitempty"`
	Grants         []string          `json:"grants,omitempty"`
}

// StartAgentReply is start-agent's reply. Ready=false with BlockedBy means the pane waits on a person.
type StartAgentReply struct {
	Agent          string         `json:"agent"`
	Command        string         `json:"command"`
	CreatedSession bool           `json:"created_session"`
	CWD            string         `json:"cwd"`
	Name           string         `json:"name"`
	Outcome        string         `json:"outcome"`
	PtyID          string         `json:"pty_id"`
	Ready          bool           `json:"ready"`
	Reason         string         `json:"reason"`
	BlockedBy      string         `json:"blocked_by"`
	Session        string         `json:"session"`
	SessionID      string         `json:"session_id"`
	State          string         `json:"state"`
	WindowID       string         `json:"window_id"`
	Workspace      int            `json:"workspace"`
	Raw            map[string]any `json:"-"`
}

func (r *StartAgentReply) setRaw(m map[string]any) { r.Raw = m }

// StartAgent starts an agent pane and waits for it to be ready.
func StartAgent(ctx context.Context, c Client, p StartAgentParams) (StartAgentReply, error) {
	return callRaw[StartAgentReply](ctx, c, "start-agent", p)
}

// AgentInfo is one list-agents row (with all=true every window appears, agent or not).
type AgentInfo struct {
	WindowID       string         `json:"window_id"`
	Name           string         `json:"name"`
	Session        string         `json:"session"`
	Host           string         `json:"host"`
	HarnessID      string         `json:"harness_id"`
	State          string         `json:"state"`
	AgentStateAt   int64          `json:"agent_state_at"`
	Message        string         `json:"message"`
	BlockedBy      string         `json:"blocked_by"`
	NeedsYou       bool           `json:"needs_you"`
	Ready          bool           `json:"ready"`
	Queued         int            `json:"queued"`
	Unread         int            `json:"unread"`
	Subagents      int            `json:"subagents"`
	FinishedUnread bool           `json:"finished_unread"`
	Foreground     string         `json:"foreground"`
	CWD            string         `json:"cwd"`
	Confidence     string         `json:"confidence"`
	Source         string         `json:"source"`
	Workspace      int            `json:"workspace"`
	Raw            map[string]any `json:"-"`
}

func (a *AgentInfo) setRaw(m map[string]any) { a.Raw = m }

// ListAgentsParams select which panes list-agents returns.
type ListAgentsParams struct {
	Session     string `json:"session,omitempty"`
	All         bool   `json:"all,omitempty"`
	AllSessions bool   `json:"all_sessions,omitempty"`
	Select      string `json:"select,omitempty"`
}

// AgentList is the list-agents reply.
type AgentList struct {
	Agents      []AgentInfo
	HumanUnread int
	Total       int
}

// ListAgents lists agent panes (every pane with All).
func ListAgents(ctx context.Context, c Client, p ListAgentsParams) (AgentList, error) {
	var res struct {
		Agents      []json.RawMessage `json:"agents"`
		HumanUnread int               `json:"human_unread"`
		Total       int               `json:"total"`
	}
	if err := c.Call(ctx, "list-agents", p, &res); err != nil {
		return AgentList{}, err
	}
	as, err := decodeList[AgentInfo](res.Agents)
	if err != nil {
		return AgentList{}, err
	}
	return AgentList{Agents: as, HumanUnread: res.HumanUnread, Total: res.Total}, nil
}

// AgentStateInfo is the get-agent-state reply.
type AgentStateInfo struct {
	WindowID     string         `json:"window_id"`
	State        string         `json:"state"`
	Activity     string         `json:"activity"`
	Message      string         `json:"message"`
	HarnessID    string         `json:"harness_id"`
	Source       string         `json:"source"`
	Confidence   string         `json:"confidence"`
	AgentStateAt int64          `json:"agent_state_at"`
	NeedsYou     bool           `json:"needs_you"`
	Ready        bool           `json:"ready"`
	BlockedBy    string         `json:"blocked_by"`
	Queued       int            `json:"queued"`
	Subagents    int            `json:"subagents"`
	Raw          map[string]any `json:"-"`
}

func (a *AgentStateInfo) setRaw(m map[string]any) { a.Raw = m }

// GetAgentState reads one window's reported agent state.
func GetAgentState(ctx context.Context, c Client, session, window string) (AgentStateInfo, error) {
	return callRaw[AgentStateInfo](ctx, c, "get-agent-state", map[string]any{"session": session, "window": window})
}

// Activity is the agent-activity reply. Entry text is the agent's and untrusted.
type Activity struct {
	Session   string           `json:"session"`
	Window    string           `json:"window"`
	Entries   []map[string]any `json:"entries"`
	LastSeq   int64            `json:"last_seq"`
	Recap     map[string]any   `json:"recap"`
	Untrusted bool             `json:"untrusted"`
	Raw       map[string]any   `json:"-"`
}

func (a *Activity) setRaw(m map[string]any) { a.Raw = m }

// AgentActivity reads a pane's activity ring. limit<=0 uses the daemon default.
func AgentActivity(ctx context.Context, c Client, session, window string, limit int, recap bool) (Activity, error) {
	p := map[string]any{"session": session, "window": window}
	if limit > 0 {
		p["limit"] = limit
	}
	if recap {
		p["recap"] = true
	}
	return callRaw[Activity](ctx, c, "agent-activity", p)
}

// ---- hosts ----

// HostInfo is one list-hosts row. Field names beyond Raw are best effort:
// the verified reply held no hosts, so callers should consult Raw.
type HostInfo struct {
	Name   string         `json:"name"`
	Status string         `json:"status"`
	Raw    map[string]any `json:"-"`
}

func (h *HostInfo) setRaw(m map[string]any) {
	h.Raw = m
	if h.Name == "" {
		h.Name, _ = m["host"].(string)
	}
	if h.Status == "" {
		h.Status, _ = m["state"].(string)
	}
}

// ListHosts lists the machines in the daemon's [hosts] table.
func ListHosts(ctx context.Context, c Client) ([]HostInfo, error) {
	var res struct {
		Hosts []json.RawMessage `json:"hosts"`
	}
	if err := c.Call(ctx, "list-hosts", nil, &res); err != nil {
		return nil, err
	}
	return decodeList[HostInfo](res.Hosts)
}

// HostSession is one session on a host.
type HostSession struct {
	Name        string `json:"name"`
	Created     int64  `json:"created"`
	LastActive  int64  `json:"last_active"`
	WindowCount int    `json:"window_count"`
}

// HostSessions is one host's entry in list-host-sessions.
type HostSessions struct {
	Host     string         `json:"host"`
	Status   string         `json:"status"`
	Sessions []HostSession  `json:"sessions"`
	Raw      map[string]any `json:"-"`
}

func (h *HostSessions) setRaw(m map[string]any) { h.Raw = m }

// ListHostSessions lists sessions on host ("" = every host, "local" = this machine).
func ListHostSessions(ctx context.Context, c Client, host string) ([]HostSessions, error) {
	var p any
	if host != "" {
		p = map[string]any{"host": host}
	}
	var res struct {
		Hosts []json.RawMessage `json:"hosts"`
	}
	if err := c.Call(ctx, "list-host-sessions", p, &res); err != nil {
		return nil, err
	}
	return decodeList[HostSessions](res.Hosts)
}
