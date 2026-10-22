package keys

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Mode is an interaction mode.
type Mode int

const (
	Normal Mode = iota
	Insert
	Live
	Jump
	Context
	Palette
	Help
	Note
)

var modeNames = [...]string{"normal", "insert", "live", "jump", "context", "palette", "help", "note"}

func (m Mode) String() string {
	if int(m) < len(modeNames) && m >= 0 {
		return modeNames[m]
	}
	return "unknown"
}

// ParseMode returns the Mode named s.
func ParseMode(s string) (Mode, bool) {
	for i, n := range modeNames {
		if n == s {
			return Mode(i), true
		}
	}
	return Normal, false
}

// TextMode reports whether keys in m are text/input, not navigation.
func (m Mode) TextMode() bool { return m == Insert || m == Note || m == Palette || m == Live }

// Nav is a navigation motion.
type Nav int

const (
	NavNone Nav = iota
	Up
	Down
	Left
	Right
	HalfUp
	HalfDown
	PageUp
	PageDown
	Top
	Bottom
	PrevSection
	NextSection
)

var navNames = [...]string{"none", "up", "down", "left", "right", "half-up", "half-down", "page-up", "page-down", "top", "bottom", "prev-section", "next-section"}

func (n Nav) String() string { return navNames[n] }

var navKeys = map[string]Nav{
	"j": Down, "down": Down,
	"k": Up, "up": Up,
	"h": Left, "left": Left,
	"l": Right, "right": Right,
	"d": HalfDown, "u": HalfUp,
	"ctrl+d": HalfDown, "ctrl+u": HalfUp,
	"ctrl+f": PageDown, "pgdown": PageDown,
	"ctrl+b": PageUp, "pgup": PageUp,
	"g": Top, "home": Top,
	"G": Bottom, "end": Bottom,
	"{": PrevSection, "}": NextSection,
}

// NavFor maps a key string (tea.KeyMsg.String()) to a motion.
func NavFor(key string) (Nav, bool) { n, ok := navKeys[key]; return n, ok }

// Kind classifies a routed key.
type Kind int

const (
	Pass Kind = iota
	Global
	Navigate
	Pending
	Sequence
	Cancelled
)

var kindNames = [...]string{"pass", "global", "navigate", "pending", "sequence", "cancelled"}

func (k Kind) String() string { return kindNames[k] }

// Result of routing one key.
type Result struct {
	Kind   Kind
	Key    string // the key, or the completed sequence ("n t")
	Nav    Nav    // for Navigate
	Action string // for Global
}

// DefaultTimeout for pending multi-key sequences.
const DefaultTimeout = 1200 * time.Millisecond

// TimeoutMsg cancels a pending sequence when its generation still matches.
type TimeoutMsg struct{ Gen int }

// Router routes keys by mode. Use a pointer; it holds pending-sequence state.
type Router struct {
	Globals map[string]string // key → action id, reserved in every mode
	Timeout time.Duration
	Now     func() time.Time

	seqs     map[string]bool // full sequences, space separated
	prefixes map[string]bool
	pending  []string
	deadline time.Time
	gen      int
}

// NewRouter returns a router with F4 reserved for New note.
func NewRouter() *Router {
	return &Router{
		Globals:  map[string]string{"f4": "note.new"},
		Timeout:  DefaultTimeout,
		Now:      time.Now,
		seqs:     map[string]bool{},
		prefixes: map[string]bool{},
	}
}

// SetGlobal reserves key for action in every mode ("" removes it).
func (r *Router) SetGlobal(key, action string) {
	if action == "" {
		delete(r.Globals, key)
		return
	}
	r.Globals[key] = action
}

// AddSequences registers multi-key bindings like "n t". Single keys are ignored.
func (r *Router) AddSequences(seqs ...string) {
	for _, s := range seqs {
		parts := strings.Fields(s)
		if len(parts) < 2 {
			continue
		}
		r.seqs[strings.Join(parts, " ")] = true
		for i := 1; i < len(parts); i++ {
			r.prefixes[strings.Join(parts[:i], " ")] = true
		}
	}
}

// PendingKeys is the in-progress sequence ("" when none), for status display.
func (r *Router) PendingKeys() string { return strings.Join(r.pending, " ") }

// TimeoutCmd schedules cancellation of the current pending sequence.
func (r *Router) TimeoutCmd() tea.Cmd {
	gen := r.gen
	return tea.Tick(r.Timeout, func(time.Time) tea.Msg { return TimeoutMsg{Gen: gen} })
}

// HandleTimeout cancels the pending sequence if msg is current.
func (r *Router) HandleTimeout(msg TimeoutMsg) Result {
	if msg.Gen == r.gen && len(r.pending) > 0 {
		r.cancel()
		return Result{Kind: Cancelled}
	}
	return Result{Kind: Pass}
}

// Cancel drops any pending sequence.
func (r *Router) Cancel() { r.cancel() }

func (r *Router) cancel() { r.pending = nil; r.gen++ }

func navMode(m Mode) bool { return m == Normal || m == Jump || m == Context || m == Help }

// Route classifies msg in mode.
func (r *Router) Route(mode Mode, msg tea.KeyMsg) Result {
	key := msg.String()
	if a, ok := r.Globals[key]; ok {
		r.cancel()
		return Result{Kind: Global, Key: key, Action: a}
	}
	if !navMode(mode) {
		if len(r.pending) > 0 {
			r.cancel()
		}
		return Result{Kind: Pass, Key: key}
	}
	if len(r.pending) > 0 {
		expired := r.Now().After(r.deadline)
		if !expired && key == "esc" {
			r.cancel()
			return Result{Kind: Cancelled, Key: key}
		}
		cand := strings.Join(append(append([]string{}, r.pending...), key), " ")
		if !expired && r.seqs[cand] {
			r.cancel()
			return Result{Kind: Sequence, Key: cand}
		}
		if !expired && r.prefixes[cand] {
			r.pending = append(r.pending, key)
			r.deadline = r.Now().Add(r.Timeout)
			return Result{Kind: Pending, Key: cand}
		}
		r.cancel() // fall through: route key fresh
	}
	if r.prefixes[key] {
		r.gen++
		r.pending = []string{key}
		r.deadline = r.Now().Add(r.Timeout)
		return Result{Kind: Pending, Key: key}
	}
	if n, ok := NavFor(key); ok {
		return Result{Kind: Navigate, Key: key, Nav: n}
	}
	return Result{Kind: Pass, Key: key}
}
