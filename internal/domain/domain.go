// Package domain holds the pure types every other mtm module shares.
// It imports only the standard library.
package domain

import "time"

// Reserved server and session names.
const (
	DefaultServer      = "clean-mtm"
	SessionNoProject   = "no-project"
	SessionMTMOnly     = "mtm-only"
	ProjectUnknown     = "unknown"
	HostLocal          = "local"
	TestServerPrefix   = "cmt-" // + 8 hex; TUIOS server names are capped at 14 chars
)

// Target addresses one runtime object: server, host, session, stable window id.
// Never address by display name or list position alone.
type Target struct {
	Server   string `json:"server"`
	Host     string `json:"host"`
	Session  string `json:"session"`
	WindowID string `json:"windowId"`
}

func (t Target) Key() string { return t.Server + "|" + t.Host + "|" + t.Session + "|" + t.WindowID }
func (t Target) IsZero() bool { return t.WindowID == "" && t.Session == "" }

// Kind of a managed window.
type Kind string

const (
	KindTerminal Kind = "terminal"
	KindAgent    Kind = "agent"
	KindVim      Kind = "vim"
	KindUnknown  Kind = "unknown"
)

// AgentState mirrors the daemon's enum; UnknownState is not zero, ready or success.
type AgentState string

const (
	StateNone       AgentState = "none"
	StateWorking    AgentState = "working"
	StateNeedsInput AgentState = "needs_input"
	StateIdle       AgentState = "idle"
	StateDone       AgentState = "done"
	StateErrored    AgentState = "errored"
	StateUnknown    AgentState = "unknown"
)

// Flags compose; none hides another.
type Flags struct {
	Working    bool
	Blocked    bool
	NeedsInput bool
	Unread     int
	Queued     int
	Completed  bool
	Subagents  int
}

// Window is one inventory row.
type Window struct {
	Target       Target
	Name         string
	Title        string
	Kind         Kind
	Harness      string
	State        AgentState
	RawState     string // unfamiliar source values preserved
	Flags        Flags
	CWD          string
	PID          int
	Workspace    int
	LastActivity time.Time
	Closed       bool // daemon reported closed
	Unavailable  bool // source disconnected, not proven closed
	Project      string // "" = no ownership; derived from session name by inventory
	Stale        bool
	Pinned       bool
	Starred      bool
	Raw          map[string]any
}

type Session struct {
	Server  string
	Host    string
	Name    string
	ID      string
	Windows int
	Project string
	Stale   bool
}

type Host struct {
	Server string
	Name   string
	Status string
	Stale  bool
}

// ConnState of the selected server.
type ConnState string

const (
	ConnConnecting     ConnState = "connecting"
	ConnConnected      ConnState = "connected"
	ConnDisconnected   ConnState = "disconnected"
	ConnResynchronizing ConnState = "resynchronizing"
	ConnUnknown        ConnState = "unknown"
)

type Connection struct {
	Server      string
	SocketPath  string
	State       ConnState
	LastSuccess time.Time
	LastError   string
}

// Snapshot is the one shared inventory read by every page.
type Snapshot struct {
	Connection Connection
	Hosts      []Host
	Sessions   []Session
	Windows    []Window
	TakenAt    time.Time
	Stale      bool
}

// Project is application-owned (SQLite).
type Project struct {
	ID        int64
	Name      string
	Tags      []string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ContextItem is a saved file/doc/note tied (optionally) to a project.
type ContextItem struct {
	ID        int64
	Kind      string // "file" | "doc" | "note" | other
	Title     string
	Path      string
	Body      string
	Project   string // "" = no-project
	Source    *Target
	Tags      []string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Outcome of a delivery/launch step.
type Outcome string

const (
	OutcomePending   Outcome = "pending"
	OutcomeAccepted  Outcome = "accepted"
	OutcomeWaiting   Outcome = "waiting"
	OutcomeDelivered Outcome = "delivered"
	OutcomeBlocked   Outcome = "blocked"
	OutcomeFailed    Outcome = "failed"
	OutcomeUnknown   Outcome = "unknown"
)

// LaunchSpec is shared by quick and form creation.
type LaunchSpec struct {
	Kind     Kind
	Host     string
	Session  string // resolved from Project unless explicit
	Project  string // "" means no-project (explicit)
	Name     string
	CWD      string
	Command  []string
	Harness  string
	Model    string
	Effort   string
	Speed    string
	Prompt   string
	Queue    bool
}

// Capability of an agent harness/model: recognized vs installed vs usable.
type Capability struct {
	ID         string
	Label      string
	Recognized bool
	Installed  bool
	Usable     bool
	Reason     string
}
