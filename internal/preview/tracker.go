package preview

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/unixsuperhero/gmtm/internal/domain"
)

// Tracker is the generation guard: only the latest (target, gen) is accepted.
type Tracker struct {
	mu     sync.Mutex
	target domain.Target
	gen    uint64
}

// Begin selects target and returns the new generation to pass to Capture.
func (t *Tracker) Begin(target domain.Target) uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gen++
	t.target = target
	return t.gen
}

// Current returns the selected target and generation.
func (t *Tracker) Current() (domain.Target, uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.target, t.gen
}

// Accept reports whether msg belongs to the current selection; late replies
// for an older generation or another target are rejected.
func (t *Tracker) Accept(msg ResultMsg) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return msg.Gen == t.gen && msg.Target.Key() == t.target.Key()
}

// Cache keeps the last good output per target.
type Cache struct {
	mu sync.Mutex
	m  map[string]ResultMsg
}

func NewCache() *Cache { return &Cache{m: map[string]ResultMsg{}} }

// Put stores successful (current or empty) results; failures are ignored.
func (c *Cache) Put(msg ResultMsg) {
	if msg.Err != nil || (msg.State != StateCurrent && msg.State != StateEmpty) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[msg.Target.Key()] = msg
}

// Get returns the last good result for target.
func (c *Cache) Get(target domain.Target) (ResultMsg, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.m[target.Key()]
	return m, ok
}

// Forget drops target (e.g. when it is proven closed).
func (c *Cache) Forget(target domain.Target) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, target.Key())
}

// Resolve folds a fresh result into the cache. Successes are stored and
// returned. A failed refresh of a target that is not closed returns the same
// target's cached output marked StateCached and carrying the new error. A
// closed target is forgotten.
func (c *Cache) Resolve(msg ResultMsg) ResultMsg {
	if msg.Err == nil {
		c.Put(msg)
		return msg
	}
	if msg.State == StateClosed {
		c.Forget(msg.Target)
		return msg
	}
	old, ok := c.Get(msg.Target)
	if !ok {
		return msg
	}
	old.State = StateCached
	old.Err = msg.Err
	old.Gen = msg.Gen
	return old
}

// Age is how long ago msg was captured.
func Age(msg ResultMsg) time.Duration {
	if msg.TakenAt.IsZero() {
		return 0
	}
	return now().Sub(msg.TakenAt)
}

// Labeled renders the preview header, e.g.
// "clean-mtm/local/work/w1 • captured 3s ago • cached • truncated • error: boom".
// If msg failed and cached is the same target's last good output, the header
// describes the cached output (its age) plus msg's error.
func Labeled(msg ResultMsg, cached *ResultMsg) string {
	shown := msg
	if msg.Err != nil && cached != nil && cached.Target.Key() == msg.Target.Key() && msg.State != StateClosed {
		shown = *cached
		shown.State = StateCached
		shown.Err = msg.Err
	}
	host := shown.Target.Host
	if host == "" {
		host = domain.HostLocal
	}
	parts := []string{fmt.Sprintf("%s/%s/%s/%s", shown.Target.Server, host, shown.Target.Session, shown.Target.WindowID)}
	if shown.State == StateLoading {
		parts = append(parts, "loading")
	} else if !shown.TakenAt.IsZero() && shown.State != StateUnavailable && shown.State != StateClosed {
		parts = append(parts, "captured "+humanAge(Age(shown))+" ago")
	}
	switch shown.State {
	case StateCached, StateEmpty, StateClosed, StateUnavailable:
		parts = append(parts, string(shown.State))
	}
	if shown.Truncated {
		parts = append(parts, "truncated")
	}
	if shown.Err != nil {
		parts = append(parts, "error: "+oneLine(shown.Err.Error()))
	}
	return strings.Join(parts, " • ")
}

// FitHeader shrinks the header to w cells (metadata shrinks before the preview goes).
func FitHeader(h string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(h) <= w {
		return h
	}
	return ansi.Truncate(h, w, "…")
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Second:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	default:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
}

func oneLine(s string) string {
	s = string(Sanitize([]byte(s)))
	s = ansi.Strip(s)
	return strings.Join(strings.Fields(s), " ")
}
