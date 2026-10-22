// Package tuiostest is a scriptable fake TUIOS daemon on a unix socket, for tests
// of any package that talks to the daemon through tuios.Client.
package tuiostest

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/unixsuperhero/gmtm/internal/tuios"
)

// Handler answers one verb. Return a result (marshalled to JSON) or a daemon error.
type Handler func(params json.RawMessage) (any, *tuios.Error)

// StreamHandler runs a stream-pane connection after the reply line was sent.
type StreamHandler func(params json.RawMessage, sc *StreamConn)

// Request is one recorded request.
type Request struct {
	ID     int64           `json:"id"`
	Verb   string          `json:"verb"`
	Params json.RawMessage `json:"params"`
}

// Daemon is a fake daemon.
type Daemon struct {
	Path string
	dir  string
	ln   net.Listener

	mu       sync.Mutex
	handlers map[string]Handler
	stream   StreamHandler
	reqs     []Request
	conns    map[net.Conn]bool
	subs     map[*subscriber]bool
	seq      int64
	BootID   string
	wg       sync.WaitGroup
}

type subscriber struct {
	c   net.Conn
	wmu sync.Mutex
}

// Start listens on a short temp socket path and registers cleanup on t.
func Start(t testing.TB) *Daemon {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "tfd")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{Path: p, dir: dir, ln: ln, handlers: map[string]Handler{}, conns: map[net.Conn]bool{}, subs: map[*subscriber]bool{}, BootID: "fakeboot"}
	d.Handle("hello", func(json.RawMessage) (any, *tuios.Error) {
		return map[string]any{"type": "hello", "protocol": 1, "min_protocol": 1}, nil
	})
	d.wg.Add(1)
	go d.accept()
	t.Cleanup(d.Close)
	return d
}

// Handle sets the handler for verb.
func (d *Daemon) Handle(verb string, h Handler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[verb] = h
}

// Result is a Handler that always returns v.
func Result(v any) Handler { return func(json.RawMessage) (any, *tuios.Error) { return v, nil } }

// Fail is a Handler that always returns a daemon error.
func Fail(code, msg string) Handler {
	return func(json.RawMessage) (any, *tuios.Error) { return nil, &tuios.Error{Code: code, Message: msg} }
}

// OnStream sets the stream-pane behaviour.
func (d *Daemon) OnStream(h StreamHandler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stream = h
}

// Requests returns a copy of every request received, in order.
func (d *Daemon) Requests() []Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Request(nil), d.reqs...)
}

// Count returns how many requests used verb.
func (d *Daemon) Count(verb string) int {
	n := 0
	for _, r := range d.Requests() {
		if r.Verb == verb {
			n++
		}
	}
	return n
}

// Subscribers returns the number of live subscribers.
func (d *Daemon) Subscribers() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.subs)
}

// Emit sends an event (seq, boot_id added) to every subscriber.
func (d *Daemon) Emit(ev map[string]any) {
	d.mu.Lock()
	d.seq++
	ev["seq"] = d.seq
	ev["boot_id"] = d.BootID
	subs := make([]*subscriber, 0, len(d.subs))
	for s := range d.subs {
		subs = append(subs, s)
	}
	d.mu.Unlock()
	b, _ := json.Marshal(ev)
	for _, s := range subs {
		s.wmu.Lock()
		_, _ = s.c.Write(append(b, '\n'))
		s.wmu.Unlock()
	}
}

// DropSubscribers closes every subscriber connection (simulated disconnect).
func (d *Daemon) DropSubscribers() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for s := range d.subs {
		s.c.Close()
		delete(d.subs, s)
	}
}

// DropAll closes every open connection, RPC ones included.
func (d *Daemon) DropAll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for c := range d.conns {
		c.Close()
	}
	d.subs = map[*subscriber]bool{}
}

// Close stops the daemon.
func (d *Daemon) Close() {
	d.ln.Close()
	d.DropAll()
	d.wg.Wait()
	os.RemoveAll(d.dir)
}

func (d *Daemon) accept() {
	defer d.wg.Done()
	for {
		c, err := d.ln.Accept()
		if err != nil {
			return
		}
		d.mu.Lock()
		d.conns[c] = true
		d.mu.Unlock()
		d.wg.Add(1)
		go d.serve(c)
	}
}

func (d *Daemon) serve(c net.Conn) {
	defer d.wg.Done()
	defer func() {
		d.mu.Lock()
		delete(d.conns, c)
		d.mu.Unlock()
		c.Close()
	}()
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var req Request
		if json.Unmarshal(line, &req) != nil {
			return
		}
		d.mu.Lock()
		d.reqs = append(d.reqs, req)
		h := d.handlers[req.Verb]
		sh := d.stream
		d.mu.Unlock()
		switch req.Verb {
		case "subscribe":
			d.mu.Lock()
			d.seq++
			reply := map[string]any{"id": req.ID, "result": map[string]any{"type": "subscribed", "seq": d.seq, "boot_id": d.BootID}}
			b, _ := json.Marshal(reply)
			s := &subscriber{c: c}
			// write before registering so the reply precedes any event
			_, _ = c.Write(append(b, '\n'))
			d.subs[s] = true
			d.mu.Unlock()
			_, _ = io.Copy(io.Discard, r) // hold until closed
			d.mu.Lock()
			delete(d.subs, s)
			d.mu.Unlock()
			return
		case "stream-pane":
			if h == nil {
				h = Result(map[string]any{"type": "pane_stream", "boot_id": d.BootID, "cols": 80, "rows": 24, "mode": "snapshot"})
			}
		}
		var reply map[string]any
		if h == nil {
			reply = map[string]any{"id": req.ID, "error": map[string]any{"code": "unknown_verb", "message": "unknown verb " + req.Verb}}
		} else if res, derr := h(req.Params); derr != nil {
			reply = map[string]any{"id": req.ID, "error": derr}
		} else {
			reply = map[string]any{"id": req.ID, "result": res}
		}
		b, _ := json.Marshal(reply)
		if _, err := c.Write(append(b, '\n')); err != nil {
			return
		}
		if req.Verb == "stream-pane" && reply["error"] == nil {
			if sh != nil {
				sh(req.Params, &StreamConn{c: c, r: r})
			}
			return
		}
	}
}

// StreamConn is the daemon side of a stream-pane connection.
type StreamConn struct {
	c net.Conn
	r *bufio.Reader
}

// WriteFrame writes a raw frame.
func (s *StreamConn) WriteFrame(typ byte, payload []byte) error {
	buf := make([]byte, 5+len(payload))
	buf[0] = typ
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[5:], payload)
	_, err := s.c.Write(buf)
	return err
}

// Snapshot writes an S frame.
func (s *StreamConn) Snapshot(seq uint64, cols, rows uint16, data []byte) error {
	p := make([]byte, 12+len(data))
	binary.BigEndian.PutUint64(p[0:8], seq)
	binary.BigEndian.PutUint16(p[8:10], cols)
	binary.BigEndian.PutUint16(p[10:12], rows)
	copy(p[12:], data)
	return s.WriteFrame('S', p)
}

// Output writes an O frame.
func (s *StreamConn) Output(seq uint64, data []byte) error {
	p := make([]byte, 8+len(data))
	binary.BigEndian.PutUint64(p[0:8], seq)
	copy(p[8:], data)
	return s.WriteFrame('O', p)
}

// ReadFrame reads one client frame (type, payload).
func (s *StreamConn) ReadFrame() (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(s.r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:5])
	if n > 1<<20 {
		return 0, nil, errors.New("frame too large")
	}
	p := make([]byte, n)
	_, err := io.ReadFull(s.r, p)
	return hdr[0], p, err
}

// Close ends the stream connection.
func (s *StreamConn) Close() error { return s.c.Close() }
