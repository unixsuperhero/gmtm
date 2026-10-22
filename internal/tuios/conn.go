package tuios

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ProtocolVersion is the control protocol mtm speaks.
const ProtocolVersion = 1

// ClientName is what mtm reports in hello.
const ClientName = "mtm"

// Options tune a client. The zero value is the production configuration.
type Options struct {
	// SocketPath overrides the resolved socket (tests only).
	SocketPath string
	// Binary is the tuios executable for start/kill-server; default "tuios".
	Binary string
	// Environ supplies the inherited environment; default os.Environ.
	Environ func() []string
	// Version is reported in hello.
	Version string
	// MaxIdle is how many idle RPC connections are kept; default 4.
	MaxIdle int
}

// New returns a client for a named server or the explicit DefaultDaemon selector.
func New(serverName string) (Client, error) { return NewWithOptions(serverName, Options{}) }

// NewWithOptions is New with explicit options.
func NewWithOptions(serverName string, o Options) (Client, error) {
	if err := ValidateServerName(serverName); err != nil {
		return nil, err
	}
	sock := o.SocketPath
	if sock == "" {
		var err error
		if sock, err = ResolveSocketPath(serverName); err != nil {
			return nil, err
		}
	}
	if o.Binary == "" {
		o.Binary = "tuios"
	}
	if o.Environ == nil {
		o.Environ = os.Environ
	}
	if o.MaxIdle <= 0 {
		o.MaxIdle = 4
	}
	return &client{name: serverName, sock: sock, opt: o}, nil
}

type client struct {
	name string
	sock string
	opt  Options

	mu   sync.Mutex
	idle []*rpcConn
	id   atomic.Int64
}

type rpcConn struct {
	c net.Conn
	r *bufio.Reader
}

func (c *client) Server() string     { return c.name }
func (c *client) SocketPath() string { return c.sock }
func (c *client) ChildEnv() []string { return ScrubEnv(c.opt.Environ(), c.name) }

type request struct {
	ID     int64  `json:"id"`
	Verb   string `json:"verb"`
	Params any    `json:"params,omitempty"`
}

type envelope struct {
	ID     *int64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

// bindCtx applies ctx's deadline to conn and aborts blocked I/O on cancel.
// The returned func detaches the cancel hook and clears the deadline.
func bindCtx(ctx context.Context, conn net.Conn) func() {
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Time{})
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	return func() {
		stop()
		_ = conn.SetDeadline(time.Time{})
	}
}

func ctxErr(ctx context.Context, err error) error {
	if ce := ctx.Err(); ce != nil {
		return fmt.Errorf("tuios: %w (%v)", ce, err)
	}
	return err
}

// dial opens a connection and performs hello on it.
func (c *client) dial(ctx context.Context) (*rpcConn, error) {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "unix", c.sock)
	if err != nil {
		return nil, fmt.Errorf("tuios: server %q not reachable at %s: %w", c.name, c.sock, err)
	}
	rc := &rpcConn{c: nc, r: bufio.NewReader(nc)}
	release := bindCtx(ctx, nc)
	defer release()
	if _, err := c.roundTrip(rc, "hello", c.helloParams()); err != nil {
		nc.Close()
		return nil, ctxErr(ctx, fmt.Errorf("tuios: hello: %w", err))
	}
	return rc, nil
}

func (c *client) helloParams() map[string]any {
	p := map[string]any{"client": ClientName, "protocol": ProtocolVersion}
	if c.opt.Version != "" {
		p["version"] = c.opt.Version
	}
	return p
}

// roundTrip writes one request and reads lines until its reply arrives.
func (c *client) roundTrip(rc *rpcConn, verb string, params any) (json.RawMessage, error) {
	id := c.id.Add(1)
	b, err := json.Marshal(request{ID: id, Verb: verb, Params: params})
	if err != nil {
		return nil, fmt.Errorf("tuios: encode %s: %w", verb, err)
	}
	if _, err := rc.c.Write(append(b, '\n')); err != nil {
		return nil, err
	}
	for {
		line, err := rc.r.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		var env envelope
		if err := json.Unmarshal(line, &env); err != nil {
			return nil, fmt.Errorf("tuios: bad reply to %s: %w", verb, err)
		}
		if env.ID != nil && *env.ID != id {
			continue // stale reply from an abandoned request
		}
		if env.Error != nil {
			return nil, env.Error
		}
		return env.Result, nil
	}
}

func (c *client) get(ctx context.Context) (*rpcConn, bool, error) {
	c.mu.Lock()
	if n := len(c.idle); n > 0 {
		rc := c.idle[n-1]
		c.idle = c.idle[:n-1]
		c.mu.Unlock()
		return rc, true, nil
	}
	c.mu.Unlock()
	rc, err := c.dial(ctx)
	return rc, false, err
}

func (c *client) put(rc *rpcConn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.idle) >= c.opt.MaxIdle {
		rc.c.Close()
		return
	}
	c.idle = append(c.idle, rc)
}

// Call sends one verb on a pooled connection and decodes result into out.
func (c *client) Call(ctx context.Context, verb string, params any, out any) error {
	for attempt := 0; ; attempt++ {
		rc, reused, err := c.get(ctx)
		if err != nil {
			return err
		}
		release := bindCtx(ctx, rc.c)
		res, err := c.roundTrip(rc, verb, params)
		release()
		var de *Error
		switch {
		case err == nil, errors.As(err, &de):
			c.put(rc)
		default:
			rc.c.Close()
			// A pooled connection the daemon already closed: retry once on a fresh one.
			if reused && attempt == 0 && ctx.Err() == nil && isClosedConn(err) {
				continue
			}
			return ctxErr(ctx, fmt.Errorf("tuios: %s: %w", verb, err))
		}
		if err != nil {
			return err
		}
		if out != nil && len(res) > 0 {
			if err := json.Unmarshal(res, out); err != nil {
				return fmt.Errorf("tuios: decode %s result: %w", verb, err)
			}
		}
		return nil
	}
}

func isClosedConn(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "broken pipe") || strings.Contains(err.Error(), "connection reset")
}

// Ping reports whether the daemon answers hello on a fresh connection.
func (c *client) Ping(ctx context.Context) error {
	rc, err := c.dial(ctx)
	if err != nil {
		return err
	}
	c.put(rc)
	return nil
}

// Close drops idle pooled connections. Not part of Client; callers may type-assert io.Closer.
func (c *client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, rc := range c.idle {
		rc.c.Close()
	}
	c.idle = nil
	return nil
}

// openDedicated dials, says hello, sends verb, and returns the conn positioned after the reply.
func (c *client) openDedicated(ctx context.Context, verb string, params any, out any) (*rpcConn, error) {
	rc, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	release := bindCtx(ctx, rc.c)
	res, err := c.roundTrip(rc, verb, params)
	release()
	if err != nil {
		rc.c.Close()
		var de *Error
		if errors.As(err, &de) {
			return nil, err
		}
		return nil, ctxErr(ctx, fmt.Errorf("tuios: %s: %w", verb, err))
	}
	if out != nil && len(res) > 0 {
		if err := json.Unmarshal(res, out); err != nil {
			rc.c.Close()
			return nil, fmt.Errorf("tuios: decode %s result: %w", verb, err)
		}
	}
	return rc, nil
}

// Subscribe opens a dedicated event connection. The first event delivered is the
// daemon's "subscribed" reply (carrying seq and boot_id). The channel closes when
// ctx ends or the connection drops.
func (c *client) Subscribe(ctx context.Context, types []string, afterSeq int64, bootID string) (<-chan Event, error) {
	params := map[string]any{}
	if len(types) > 0 {
		params["types"] = types
	}
	if afterSeq > 0 || bootID != "" {
		params["after_seq"] = afterSeq
		if bootID != "" {
			params["boot_id"] = bootID
		}
	}
	var sub json.RawMessage
	rc, err := c.openDedicated(ctx, "subscribe", params, &sub)
	if err != nil {
		return nil, err
	}
	ch := make(chan Event, 256)
	first, _ := parseEvent(sub)
	if first.Type == "" {
		first.Type = "subscribed"
	}
	ch <- first
	stop := context.AfterFunc(ctx, func() { rc.c.Close() })
	go func() {
		defer close(ch)
		defer stop()
		defer rc.c.Close()
		for {
			line, err := rc.r.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				ev, perr := parseEvent(line)
				if perr == nil {
					select {
					case ch <- ev:
					case <-ctx.Done():
						return
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return ch, nil
}

func parseEvent(line []byte) (Event, error) {
	var ev Event
	line = bytes.TrimSpace(line)
	if err := json.Unmarshal(line, &ev); err != nil {
		return ev, err
	}
	ev.Raw = append(json.RawMessage(nil), line...)
	return ev, nil
}

// StartServer execs `tuios --server <name> start-server` with the scrubbed env,
// then waits (within ctx) until the socket answers hello.
func (c *client) StartServer(ctx context.Context) error {
	ctx, cancel := withDefaultTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := c.runTuios(ctx, "start-server"); err != nil {
		return err
	}
	var last error
	for {
		pctx, pcancel := context.WithTimeout(ctx, 2*time.Second)
		last = c.Ping(pctx)
		pcancel()
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("tuios: server %q started but does not answer: %w", c.name, last)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// KillServer execs `tuios --server <name> kill-server`.
func (c *client) KillServer(ctx context.Context) error {
	ctx, cancel := withDefaultTimeout(ctx, 30*time.Second)
	defer cancel()
	c.Close()
	return c.runTuios(ctx, "kill-server")
}

func (c *client) runTuios(ctx context.Context, sub string) error {
	name := c.name
	if name == DefaultDaemon {
		name = ""
	}
	cmd := exec.CommandContext(ctx, c.opt.Binary, "--server", name, sub)
	cmd.Env = c.ChildEnv()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.WaitDelay = 2 * time.Second // a daemon child holding our pipes must not hang us
	if err := cmd.Run(); err != nil {
		return ctxErr(ctx, fmt.Errorf("tuios --server %s %s: %w: %s", c.name, sub, err, strings.TrimSpace(out.String())))
	}
	return nil
}

func withDefaultTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}
