// Package tuios is the only module that speaks the TUIOS daemon protocol.
// Every client targets exactly one named server; there is no default-server path.
package tuios

import (
	"context"
	"encoding/json"
)

// Error is a daemon error envelope.
type Error struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Hint    json.RawMessage `json:"hint,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Event is one line from a subscribe stream.
type Event struct {
	Seq     int64           `json:"seq"`
	BootID  string          `json:"boot_id,omitempty"`
	Type    string          `json:"type"`
	Session string          `json:"session,omitempty"`
	Window  string          `json:"window,omitempty"`
	Time    int64           `json:"time,omitempty"`
	Raw     json.RawMessage `json:"-"`
}

// Frame is one binary stream-pane frame.
type Frame struct {
	Type byte // 'S','O','R','E','X' from daemon; 'I','L' to daemon
	Seq  uint64
	Cols uint16
	Rows uint16
	Data []byte
}

// Stream is an open stream-pane connection.
type Stream interface {
	Frames() <-chan Frame
	SendInput(ctx context.Context, b []byte) error
	// Lease holds the pane at most at cols×rows; 0,0 releases. Must be renewed every 10s by the caller.
	Lease(ctx context.Context, cols, rows uint16) error
	Close() error
}

// Client is the single seam to the daemon. Implementations must never read
// TUIOS_SOCKET/TUIOS_SESSION/TUIOS_PANE_ID from the environment.
type Client interface {
	Server() string
	SocketPath() string
	// Call sends one verb and decodes result into out (may be nil).
	Call(ctx context.Context, verb string, params any, out any) error
	// Subscribe opens an event stream on its own connection; closes on ctx cancel.
	Subscribe(ctx context.Context, types []string, afterSeq int64, bootID string) (<-chan Event, error)
	// StreamPane opens a stream-pane; lease 0,0 means none (preview must pass 0,0).
	StreamPane(ctx context.Context, session, window string, leaseCols, leaseRows uint16) (Stream, error)
	// Ping reports whether the daemon answers hello.
	Ping(ctx context.Context) error
	// StartServer execs `tuios --server <name> start-server` with scrubbed env.
	StartServer(ctx context.Context) error
	// KillServer execs `tuios --server <name> kill-server`. Only tests should call it.
	KillServer(ctx context.Context) error
	// ChildEnv returns the environment a launched child/external subcommand receives:
	// inherited env minus pane identity vars, plus TUIOS_SERVER=<name>.
	ChildEnv() []string
}
