// Package live is the live interaction session: a stream-pane with a size
// lease, an x/vt emulator fed by S/O/R frames, a leader-key state machine and
// detach. It is the only module allowed to lease a pane. It does not touch
// inventory; next/prev/zoom/detach are emitted as messages for the orchestrator.
package live

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/vt"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/preview"
	"github.com/unixsuperhero/gmtm/internal/tuios"
)

const (
	DefaultLeader = "ctrl+a"
	RenewEvery    = 10 * time.Second
	leaseTimeout  = 2 * time.Second
	inputTimeout  = 2 * time.Second
	inputQueue    = 1024
)

// Messages emitted to the orchestrator.
type (
	DetachMsg        struct{ Target domain.Target }
	SwitchMsg        struct{ Delta int }
	ZoomMsg          struct{ Target domain.Target }
	LeaderHelpMsg    struct{ Leader string }
	UnknownLeaderMsg struct{ Key string }
	ErrorMsg         struct {
		ID  uint64
		Err error
	}
	ExitMsg struct {
		ID     uint64
		Reason string
	}
)

// frameMsg carries one daemon frame back into Update.
type frameMsg struct {
	id uint64
	f  tuios.Frame
	ok bool
}

// Option configures a Session.
type Option func(*Session)

// WithLeader sets the leader key (a tea.KeyMsg String(), e.g. "ctrl+a").
func WithLeader(k string) Option { return func(s *Session) { s.leader = k } }

// WithRenewEvery overrides the lease renewal interval (tests).
func WithRenewEvery(d time.Duration) Option { return func(s *Session) { s.renew = d } }

var nextID atomic.Uint64

// Session is one live attachment to exactly one target.
type Session struct {
	ID     uint64
	client tuios.Client
	target domain.Target
	stream tuios.Stream
	leader string
	renew  time.Duration

	mu      sync.Mutex
	emu     *vt.Emulator
	san     *preview.Sanitizer
	pending bool

	leaseMu    sync.Mutex
	cols, rows uint16
	released   bool

	ctx       context.Context
	cancel    context.CancelFunc
	input     chan []byte
	events    chan tea.Msg
	writerOut chan struct{}
	renewOut  chan struct{}
	stopOnce  sync.Once
	stopped   atomic.Bool
}

// Start opens a stream-pane on target holding a cols×rows lease.
// ctx bounds startup only; Stop owns cancellation after a successful start.
func Start(ctx context.Context, client tuios.Client, target domain.Target, cols, rows int, opts ...Option) (*Session, error) {
	c, r := clampU16(cols), clampU16(rows)
	if c == 0 || r == 0 {
		return nil, errors.New("live: lease size must be non-zero")
	}
	s := &Session{
		ID: nextID.Add(1), client: client, target: target, leader: DefaultLeader, renew: RenewEvery,
		cols: c, rows: r, san: preview.NewSanitizer(true),
		input: make(chan []byte, inputQueue), events: make(chan tea.Msg, 16),
		writerOut: make(chan struct{}), renewOut: make(chan struct{}),
	}
	for _, o := range opts {
		o(s)
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	stopStartupCancel := context.AfterFunc(ctx, s.cancel)
	st, err := client.StreamPane(s.ctx, target.Session, target.WindowID, c, r)
	stopped := stopStartupCancel()
	if err == nil && (!stopped || ctx.Err() != nil) {
		_ = st.Close()
		err = ctx.Err()
	}
	if err != nil {
		s.cancel()
		return nil, err
	}
	s.stream = st
	s.setEmu(int(c), int(r))
	go s.writer()
	go s.renewer()
	return s, nil
}

// Target is the attached target.
func (s *Session) Target() domain.Target { return s.target }

// Leader returns the configured leader key.
func (s *Session) Leader() string { return s.leader }

// LeaderPending reports whether the leader was pressed and awaits a command.
func (s *Session) LeaderPending() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.pending }

// Init starts frame delivery; call once and feed every msg to Update.
func (s *Session) Init() tea.Cmd { return s.next() }

func (s *Session) next() tea.Cmd {
	frames := s.stream.Frames()
	return func() tea.Msg {
		select {
		case f, ok := <-frames:
			return frameMsg{id: s.ID, f: f, ok: ok}
		case m := <-s.events:
			return m
		case <-s.ctx.Done():
			return nil
		}
	}
}

// Update consumes this session's internal messages and returns follow-up cmds.
// Messages for other sessions are ignored (nil).
func (s *Session) Update(msg tea.Msg) tea.Cmd {
	if s.stopped.Load() {
		return nil
	}
	switch m := msg.(type) {
	case frameMsg:
		if m.id != s.ID {
			return nil
		}
		if !m.ok {
			return emit(ExitMsg{ID: s.ID, Reason: "stream closed"})
		}
		switch m.f.Type {
		case 'S':
			s.mu.Lock()
			s.setEmuLocked(int(m.f.Cols), int(m.f.Rows))
			_, _ = s.emu.Write(s.san.Write(m.f.Data))
			s.mu.Unlock()
		case 'O':
			s.mu.Lock()
			_, _ = s.emu.Write(s.san.Write(m.f.Data))
			s.mu.Unlock()
		case 'R':
			s.mu.Lock()
			if m.f.Cols > 0 && m.f.Rows > 0 {
				s.emu.Resize(int(m.f.Cols), int(m.f.Rows))
			}
			s.mu.Unlock()
		case 'E':
			return tea.Batch(emit(ErrorMsg{ID: s.ID, Err: errors.New("stream: " + strings.TrimSpace(string(m.f.Data)))}), s.next())
		case 'X':
			return emit(ExitMsg{ID: s.ID, Reason: strings.TrimSpace(string(m.f.Data))})
		}
		return s.next()
	case ErrorMsg:
		if m.ID != s.ID {
			return nil
		}
		// writer/renewer errors travel through events; pass them on and keep reading.
		return tea.Batch(emit(m), s.next())
	}
	return nil
}

// View renders the emulator screen cropped to w×h (never resizes the target).
func (s *Session) View(w, h int) string {
	s.mu.Lock()
	r := s.emu.Render()
	s.mu.Unlock()
	lines := strings.Split(r, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return strings.Join(preview.Crop(lines, w, h), "\n")
}

// Size is the current emulator (pane) size.
func (s *Session) Size() (cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emu.Width(), s.emu.Height()
}

// Resize re-leases at cols×rows (the measured detail area).
func (s *Session) Resize(cols, rows int) error {
	c, r := clampU16(cols), clampU16(rows)
	if c == 0 || r == 0 {
		return errors.New("live: lease size must be non-zero")
	}
	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	if s.released {
		return errors.New("live: session stopped")
	}
	s.cols, s.rows = c, r
	return s.leaseLocked(c, r)
}

func (s *Session) leaseLocked(c, r uint16) error {
	ctx, cancel := context.WithTimeout(context.Background(), leaseTimeout)
	defer cancel()
	return s.stream.Lease(ctx, c, r)
}

func (s *Session) renewer() {
	defer close(s.renewOut)
	t := time.NewTicker(s.renew)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.leaseMu.Lock()
			var err error
			if !s.released {
				err = s.leaseLocked(s.cols, s.rows)
			}
			s.leaseMu.Unlock()
			if err != nil {
				s.report(err)
			}
		}
	}
}

func (s *Session) writer() {
	defer close(s.writerOut)
	for b := range s.input {
		ctx, cancel := context.WithTimeout(s.ctx, inputTimeout)
		err := s.stream.SendInput(ctx, b)
		cancel()
		if err != nil {
			s.report(err)
		}
	}
}

func (s *Session) report(err error) {
	select {
	case s.events <- ErrorMsg{ID: s.ID, Err: err}:
	default:
	}
}

// send queues bytes for the target, preserving order.
func (s *Session) send(b []byte) tea.Cmd {
	if len(b) == 0 || s.stopped.Load() {
		return nil
	}
	select {
	case s.input <- b:
		return nil
	default:
		return emit(ErrorMsg{ID: s.ID, Err: errors.New("live: input queue full; key dropped")})
	}
}

// HandleKey routes one key in live mode. Every key is consumed while the
// session runs: leader commands become messages, everything else (including
// bare Escape and ctrl+z) is forwarded to the target.
func (s *Session) HandleKey(k tea.KeyMsg) (bool, tea.Cmd) {
	if s.stopped.Load() {
		return false, nil
	}
	key := k.String()
	s.mu.Lock()
	pending := s.pending
	s.pending = false
	isLeader := !k.Paste && key == s.leader
	if !pending && isLeader {
		s.pending = true
	}
	s.mu.Unlock()

	if !pending {
		if isLeader {
			return true, nil
		}
		return true, s.send(KeyBytes(k))
	}
	switch {
	case isLeader:
		return true, s.send(KeyBytes(k))
	case key == "d":
		return true, emit(DetachMsg{Target: s.target})
	case key == "n":
		return true, emit(SwitchMsg{Delta: +1})
	case key == "p":
		return true, emit(SwitchMsg{Delta: -1})
	case key == "z":
		return true, emit(ZoomMsg{Target: s.target})
	case key == "?":
		return true, emit(LeaderHelpMsg{Leader: s.leader})
	default:
		return true, emit(UnknownLeaderMsg{Key: key})
	}
}

// Stop flushes queued input, releases the lease (L 0 0) and then closes the
// stream. Safe to call more than once.
func (s *Session) Stop() error {
	var err error
	s.stopOnce.Do(func() {
		s.stopped.Store(true)
		close(s.input)
		select {
		case <-s.writerOut:
		case <-time.After(inputTimeout + time.Second):
		}
		s.leaseMu.Lock()
		s.released = true
		err = s.leaseLocked(0, 0)
		s.leaseMu.Unlock()
		s.cancel()
		<-s.renewOut
		if cerr := s.stream.Close(); err == nil {
			err = cerr
		}
		s.mu.Lock()
		closeEmu(s.emu)
		s.mu.Unlock()
	})
	return err
}

// Switch stops this session (lease released, stream closed) and only then
// starts a session on next with the same lease size and leader.
func (s *Session) Switch(ctx context.Context, next domain.Target, opts ...Option) (*Session, error) {
	stopErr := s.Stop()
	s.leaseMu.Lock()
	c, r := s.cols, s.rows
	s.leaseMu.Unlock()
	opts = append([]Option{WithLeader(s.leader), WithRenewEvery(s.renew)}, opts...)
	ns, err := Start(ctx, s.client, next, int(c), int(r), opts...)
	if err != nil {
		return nil, errors.Join(stopErr, err)
	}
	return ns, nil
}

func (s *Session) setEmu(w, h int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setEmuLocked(w, h)
}

func (s *Session) setEmuLocked(w, h int) {
	if w <= 0 || h <= 0 {
		w, h = int(s.cols), int(s.rows)
	}
	if s.emu != nil {
		closeEmu(s.emu)
	}
	e := vt.NewEmulator(w, h)
	go func() { _, _ = io.Copy(io.Discard, e) }() // discard emulator replies (DA/DSR); the daemon answers the program
	s.emu = e
}

// closeEmu ends the reply drain by closing the emulator's reply pipe directly;
// vt.Emulator.Close writes an unguarded flag that the draining Read checks.
func closeEmu(e *vt.Emulator) {
	if pw, ok := e.InputPipe().(*io.PipeWriter); ok {
		_ = pw.CloseWithError(io.EOF)
	}
}

func emit(m tea.Msg) tea.Cmd { return func() tea.Msg { return m } }

func clampU16(v int) uint16 {
	if v <= 0 {
		return 0
	}
	if v > 0xffff {
		return 0xffff
	}
	return uint16(v)
}
