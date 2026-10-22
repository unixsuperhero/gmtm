// Package inventory owns the one shared snapshot of the selected server:
// sessions, windows and hosts built from daemon reads plus subscribe events.
// A failed read never prunes; disconnection marks data stale and unavailable.
package inventory

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/tuios"
)

// EventTypes the service subscribes to.
var EventTypes = []string{
	"window-created", "window-closed", "window-exit", "window-retitled", "window-moved",
	"agent-state", "session-created", "session-closed", "attention", "gap",
}

// Service holds the shared snapshot. Safe for concurrent use.
type Service struct {
	c tuios.Client

	// ReadTimeout bounds each daemon read; BackoffMin/Max bound reconnect waits.
	ReadTimeout time.Duration
	BackoffMin  time.Duration
	BackoffMax  time.Duration
	now         func() time.Time

	mu        sync.Mutex
	conn      domain.Connection
	hosts     []domain.Host
	sessions  map[string]domain.Session
	windows   map[string][]domain.Window // by session name, daemon order
	stale     bool
	takenAt   time.Time
	lastSeq   int64
	bootID    string
	connected bool

	updates chan struct{}
}

// New returns a service for client. Nothing is read until Refresh or Run.
func New(client tuios.Client) *Service {
	return &Service{
		c:           client,
		ReadTimeout: 10 * time.Second,
		BackoffMin:  250 * time.Millisecond,
		BackoffMax:  10 * time.Second,
		now:         time.Now,
		conn: domain.Connection{
			Server:     client.Server(),
			SocketPath: client.SocketPath(),
			State:      domain.ConnUnknown,
		},
		sessions: map[string]domain.Session{},
		windows:  map[string][]domain.Window{},
		updates:  make(chan struct{}, 1),
	}
}

// Updates signals (coalesced) whenever the snapshot changes.
func (s *Service) Updates() <-chan struct{} { return s.updates }

func (s *Service) notify() {
	select {
	case s.updates <- struct{}{}:
	default:
	}
}

// PlacementSession is the session a window of project belongs in ("" → no-project).
func (s *Service) PlacementSession(project string) string { return PlacementSession(project) }

// Snapshot returns a copy of the shared snapshot. Raw maps are shared and read-only.
func (s *Service) Snapshot() domain.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.sessions))
	seen := map[string]bool{}
	for n := range s.sessions {
		names = append(names, n)
		seen[n] = true
	}
	for n := range s.windows { // closed sessions keep their closed windows until a full refresh
		if !seen[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	snap := domain.Snapshot{
		Connection: s.conn,
		Hosts:      append([]domain.Host(nil), s.hosts...),
		TakenAt:    s.takenAt,
		Stale:      s.stale,
	}
	for _, n := range names {
		if ss, ok := s.sessions[n]; ok {
			snap.Sessions = append(snap.Sessions, ss)
		}
		snap.Windows = append(snap.Windows, s.windows[n]...)
	}
	return snap
}

func (s *Service) readCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.ReadTimeout)
}

type sessionRead struct {
	info    tuios.SessionInfo
	windows []tuios.WindowInfo
}

// Refresh performs a full read and replaces the snapshot. On failure the previous
// data is retained, marked stale and unavailable, and the error is returned.
func (s *Service) Refresh(ctx context.Context) error {
	s.mu.Lock()
	if !s.connected {
		s.conn.State = domain.ConnConnecting
	}
	s.mu.Unlock()
	reads, agents, err := s.readAll(ctx)
	if err != nil {
		s.markDisconnected(err)
		return err
	}
	hosts := s.readHosts(ctx)
	s.mu.Lock()
	server := s.c.Server()
	s.sessions = map[string]domain.Session{}
	s.windows = map[string][]domain.Window{}
	for _, r := range reads {
		s.sessions[r.info.Name] = s.buildSession(r.info, len(r.windows))
		s.windows[r.info.Name] = buildWindows(server, r.info.Name, r.windows, agents)
	}
	s.hosts = hosts
	s.markConnectedLocked()
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *Service) readAll(ctx context.Context) ([]sessionRead, map[string]*tuios.AgentInfo, error) {
	rctx, cancel := s.readCtx(ctx)
	defer cancel()
	infos, err := tuios.ListSessions(rctx, s.c)
	if err != nil {
		return nil, nil, err
	}
	reads := make([]sessionRead, 0, len(infos))
	for _, in := range infos {
		wl, err := tuios.ListWindows(rctx, s.c, in.Name)
		if tuios.IsCode(err, tuios.CodeSessionNotFound) {
			continue // closed between the two reads
		}
		if err != nil {
			return nil, nil, err
		}
		reads = append(reads, sessionRead{info: in, windows: wl.Windows})
	}
	agents, err := s.readAgents(rctx, tuios.ListAgentsParams{AllSessions: true, All: true})
	if err != nil {
		return nil, nil, err
	}
	return reads, agents, nil
}

func (s *Service) readAgents(ctx context.Context, p tuios.ListAgentsParams) (map[string]*tuios.AgentInfo, error) {
	al, err := tuios.ListAgents(ctx, s.c, p)
	if err != nil {
		return nil, err
	}
	m := make(map[string]*tuios.AgentInfo, len(al.Agents))
	for i := range al.Agents {
		a := &al.Agents[i]
		m[a.Session+"|"+a.WindowID] = a
	}
	return m, nil
}

// readHosts is best effort: failure keeps the previous host list, marked stale.
func (s *Service) readHosts(ctx context.Context) []domain.Host {
	server := s.c.Server()
	rctx, cancel := s.readCtx(ctx)
	defer cancel()
	hs, err := tuios.ListHosts(rctx, s.c)
	if err != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		out := make([]domain.Host, len(s.hosts))
		for i, h := range s.hosts {
			h.Stale = true
			out[i] = h
		}
		if len(out) == 0 {
			out = []domain.Host{{Server: server, Name: domain.HostLocal, Status: "up"}}
		}
		return out
	}
	out := []domain.Host{{Server: server, Name: domain.HostLocal, Status: "up"}}
	for _, h := range hs {
		out = append(out, domain.Host{Server: server, Name: h.Name, Status: h.Status})
	}
	return out
}

func (s *Service) buildSession(in tuios.SessionInfo, n int) domain.Session {
	return domain.Session{Server: s.c.Server(), Host: domain.HostLocal, Name: in.Name, ID: in.ID, Windows: n, Project: ProjectForSession(in.Name)}
}

func buildWindows(server, session string, ws []tuios.WindowInfo, agents map[string]*tuios.AgentInfo) []domain.Window {
	out := make([]domain.Window, 0, len(ws))
	for _, w := range ws {
		out = append(out, buildWindow(server, session, w, agents[session+"|"+w.WindowID]))
	}
	return out
}

func (s *Service) markConnectedLocked() {
	s.connected = true
	s.stale = false
	s.takenAt = s.now()
	s.conn.State = domain.ConnConnected
	s.conn.LastSuccess = s.takenAt
	s.conn.LastError = ""
}

// markDisconnected keeps every row, flagged stale and unavailable (not closed).
func (s *Service) markDisconnected(err error) {
	s.mu.Lock()
	s.connected = false
	s.stale = true
	s.conn.State = domain.ConnDisconnected
	if err != nil {
		s.conn.LastError = err.Error()
	}
	for n, ss := range s.sessions {
		ss.Stale = true
		s.sessions[n] = ss
	}
	for n, ws := range s.windows {
		for i := range ws {
			ws[i].Stale = true
			if !ws[i].Closed {
				ws[i].Unavailable = true
			}
		}
		s.windows[n] = ws
	}
	for i := range s.hosts {
		s.hosts[i].Stale = true
	}
	s.mu.Unlock()
	s.notify()
}

func (s *Service) setState(st domain.ConnState) {
	s.mu.Lock()
	s.conn.State = st
	s.mu.Unlock()
	s.notify()
}

// refreshSession re-reads one session. A missing session closes its windows.
func (s *Service) refreshSession(ctx context.Context, name string) error {
	rctx, cancel := s.readCtx(ctx)
	defer cancel()
	wl, err := tuios.ListWindows(rctx, s.c, name)
	if tuios.IsCode(err, tuios.CodeSessionNotFound) {
		s.closeSession(name)
		return nil
	}
	if err != nil {
		return err
	}
	agents, err := s.readAgents(rctx, tuios.ListAgentsParams{Session: name, All: true})
	if err != nil && !tuios.IsCode(err, tuios.CodeSessionNotFound) {
		return err
	}
	fresh := buildWindows(s.c.Server(), name, wl.Windows, agents)
	s.mu.Lock()
	present := map[string]bool{}
	for _, w := range fresh {
		present[w.Target.WindowID] = true
	}
	// Windows the daemon no longer lists are proven closed; keep them flagged until a full refresh.
	for _, old := range s.windows[name] {
		if !present[old.Target.WindowID] {
			old.Closed, old.Unavailable = true, false
			fresh = append(fresh, old)
		}
	}
	s.windows[name] = fresh
	ss, ok := s.sessions[name]
	if !ok {
		ss = domain.Session{Server: s.c.Server(), Host: domain.HostLocal, Name: name, Project: ProjectForSession(name)}
	}
	ss.Windows = len(wl.Windows)
	ss.Stale = false
	s.sessions[name] = ss
	s.takenAt = s.now()
	s.conn.LastSuccess = s.takenAt
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *Service) closeSession(name string) {
	s.mu.Lock()
	delete(s.sessions, name)
	ws := s.windows[name]
	for i := range ws {
		ws[i].Closed, ws[i].Unavailable = true, false
	}
	if len(ws) == 0 {
		delete(s.windows, name)
	}
	s.mu.Unlock()
	s.notify()
}

func (s *Service) markWindowClosed(session, id string) {
	s.mu.Lock()
	ws := s.windows[session]
	for i := range ws {
		if ws[i].Target.WindowID == id {
			ws[i].Closed, ws[i].Unavailable = true, false
		}
	}
	s.mu.Unlock()
	s.notify()
}

// EnsureSession creates an empty session if it does not exist (idempotent).
func (s *Service) EnsureSession(ctx context.Context, name string) error {
	rctx, cancel := s.readCtx(ctx)
	defer cancel()
	if _, err := tuios.NewSession(rctx, s.c, name, false, ""); err != nil && !tuios.IsCode(err, tuios.CodeSessionExists) {
		return err
	}
	return s.refreshSession(ctx, name)
}

// Run subscribes to daemon events and keeps the snapshot current until ctx ends.
// It reconnects with backoff; on gap or disconnect it marks the snapshot stale and
// resynchronizes with a full refresh. It returns ctx.Err().
func (s *Service) Run(ctx context.Context) error {
	backoff := s.BackoffMin
	for {
		err := s.session(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			backoff = s.BackoffMin
			err = errors.New("event stream closed")
		}
		s.markDisconnected(err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, s.BackoffMax)
	}
}

// session runs one subscribe connection. A nil return means the stream was
// established and then ended (backoff resets).
func (s *Service) session(ctx context.Context) error {
	s.mu.Lock()
	if s.connected || s.stale {
		s.conn.State = domain.ConnResynchronizing
	} else {
		s.conn.State = domain.ConnConnecting
	}
	s.mu.Unlock()
	s.notify()

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Subscribe first so nothing between the full read and the stream is missed.
	ch, err := s.c.Subscribe(sctx, EventTypes, 0, "")
	if err != nil {
		return err
	}
	if err := s.Refresh(sctx); err != nil {
		return err
	}
	for {
		var ev tuios.Event
		var ok bool
		select {
		case <-ctx.Done():
			return nil
		case ev, ok = <-ch:
		}
		if !ok {
			return nil
		}
		batch := []tuios.Event{ev}
	drain:
		for len(batch) < 512 {
			select {
			case e, ok2 := <-ch:
				if !ok2 {
					break drain
				}
				batch = append(batch, e)
			default:
				break drain
			}
		}
		if err := s.apply(sctx, batch); err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
}

// apply folds a batch of events into the snapshot, re-reading each affected session once.
func (s *Service) apply(ctx context.Context, batch []tuios.Event) error {
	dirty := map[string]bool{}
	full := false
	for _, ev := range batch {
		s.mu.Lock()
		if ev.Seq > 0 {
			s.lastSeq = ev.Seq
		}
		if ev.BootID != "" {
			s.bootID = ev.BootID
		}
		s.mu.Unlock()
		switch ev.Type {
		case "subscribed":
		case "gap":
			full = true
		case "window-moved":
			full = true // ownership changes across sessions
		case "session-closed":
			s.closeSession(ev.Session)
			delete(dirty, ev.Session)
		case "window-closed":
			s.markWindowClosed(ev.Session, ev.Window)
			if ev.Session != "" {
				dirty[ev.Session] = true
			}
		default:
			if ev.Session != "" {
				dirty[ev.Session] = true
			} else {
				full = true
			}
		}
	}
	if full {
		s.mu.Lock()
		s.stale = true
		s.mu.Unlock()
		s.setState(domain.ConnResynchronizing)
		return s.Refresh(ctx)
	}
	names := make([]string, 0, len(dirty))
	for n := range dirty {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := s.refreshSession(ctx, n); err != nil {
			var de *tuios.Error
			if errors.As(err, &de) {
				s.mu.Lock()
				s.conn.LastError = err.Error()
				if ss, ok := s.sessions[n]; ok {
					ss.Stale = true
					s.sessions[n] = ss
				}
				s.mu.Unlock()
				s.notify()
				continue
			}
			return err // transport failure: reconnect
		}
	}
	return nil
}
