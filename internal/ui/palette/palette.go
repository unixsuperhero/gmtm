package palette

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
	"github.com/unixsuperhero/gmtm/internal/actions"
	"github.com/unixsuperhero/gmtm/internal/ui/frame"
	"github.com/unixsuperhero/gmtm/internal/ui/keys"
)

// ExecuteMsg asks the orchestrator to run an action.
type ExecuteMsg struct {
	ActionID string
	TargetID string
}

// CloseMsg asks the orchestrator to close the palette.
type CloseMsg struct{}

// Options configure a palette instance.
type Options struct {
	TargetID    string // selected target ("" = none)
	TargetLabel string
	Page        string // filter page-scoped actions ("" = all)
	Width       int
	Height      int
}

type entryKind int

const (
	entryAction entryKind = iota
	entryVerb
	entryTarget
)

type entry struct {
	kind     entryKind
	label    string
	hint     string
	actionID string
	verb     string
}

type level struct {
	title     string
	entries   []entry // full list (root search adds actions)
	extra     []entry // matched only when searching (root: all actions)
	query     string
	searching bool
	cursor    int
}

// Model is the palette widget (value type).
type Model struct {
	reg    *actions.Registry
	opts   Options
	levels []level
}

// New opens the palette at its root, ready to search.
func New(reg *actions.Registry, opts Options) Model {
	m := Model{reg: reg, opts: opts}
	m.levels = []level{m.rootLevel()}
	return m
}

// SetSize sets the cell size.
func (m Model) SetSize(w, h int) Model { m.opts.Width, m.opts.Height = w, h; return m }

func (m Model) visibleActions() []actions.Action {
	var out []actions.Action
	for _, a := range m.reg.All() {
		if a.Page != "" && m.opts.Page != "" && a.Page != m.opts.Page {
			continue
		}
		out = append(out, a)
	}
	return out
}

func (m Model) hint(id string) string {
	k := m.reg.Keys(id)
	if len(k) == 0 {
		return "unbound"
	}
	return strings.Join(k, ", ")
}

func (m Model) actionEntry(a actions.Action, label string) entry {
	return entry{kind: entryAction, label: label, hint: m.hint(a.ID), actionID: a.ID}
}

func (m Model) rootLevel() level {
	lv := level{title: "Palette", searching: true}
	acts := m.visibleActions()
	if m.opts.TargetID != "" {
		n := 0
		for _, a := range acts {
			if a.NeedsTarget {
				n++
			}
		}
		if n > 0 {
			label := m.opts.TargetLabel
			if label == "" {
				label = m.opts.TargetID
			}
			lv.entries = append(lv.entries, entry{kind: entryTarget, label: label, hint: fmt.Sprintf("%d actions ›", n)})
		}
	}
	byVerb := map[string][]actions.Action{}
	var verbs []string
	for _, a := range acts {
		if _, ok := byVerb[a.Verb]; !ok {
			verbs = append(verbs, a.Verb)
		}
		byVerb[a.Verb] = append(byVerb[a.Verb], a)
	}
	for _, v := range verbs {
		as := byVerb[v]
		if len(as) == 1 && as[0].Object == "" {
			lv.entries = append(lv.entries, m.actionEntry(as[0], as[0].Label()))
			continue
		}
		lv.entries = append(lv.entries, entry{kind: entryVerb, label: v, verb: v, hint: "›"})
		for _, a := range as {
			lv.extra = append(lv.extra, m.actionEntry(a, a.Label()))
		}
	}
	return lv
}

func (m Model) verbLevel(verb string) level {
	lv := level{title: verb, searching: true}
	for _, a := range m.visibleActions() {
		if a.Verb == verb {
			label := a.Object
			if label == "" {
				label = a.Verb
			}
			lv.entries = append(lv.entries, m.actionEntry(a, label))
		}
	}
	return lv
}

func (m Model) targetLevel() level {
	title := m.opts.TargetLabel
	if title == "" {
		title = m.opts.TargetID
	}
	lv := level{title: title, searching: true}
	for _, a := range m.visibleActions() {
		if a.NeedsTarget {
			lv.entries = append(lv.entries, m.actionEntry(a, a.Label()))
		}
	}
	return lv
}

func (lv level) visible() []entry {
	if lv.query == "" {
		return lv.entries
	}
	all := append(append([]entry{}, lv.entries...), lv.extra...)
	labels := make([]string, len(all))
	for i, e := range all {
		labels[i] = e.label
	}
	var out []entry
	for _, mt := range fuzzy.Find(lv.query, labels) {
		out = append(out, all[mt.Index])
	}
	return out
}

func (m Model) top() *level { return &m.levels[len(m.levels)-1] }

// Depth is the number of levels (1 = root).
func (m Model) Depth() int { return len(m.levels) }

// Path is the breadcrumb titles.
func (m Model) Path() []string {
	out := make([]string, len(m.levels))
	for i, l := range m.levels {
		out[i] = l.title
	}
	return out
}

// Breadcrumb joins Path with " › ".
func (m Model) Breadcrumb() string { return strings.Join(m.Path(), " › ") }

// Query of the current level.
func (m Model) Query() string { return m.levels[len(m.levels)-1].query }

// Searching reports whether the current level's search input has focus.
func (m Model) Searching() bool { return m.levels[len(m.levels)-1].searching }

// Cursor is the highlighted index in the current level.
func (m Model) Cursor() int { return m.levels[len(m.levels)-1].cursor }

// Labels lists visible entry labels of the current level.
func (m Model) Labels() []string {
	var out []string
	for _, e := range m.levels[len(m.levels)-1].visible() {
		out = append(out, e.label)
	}
	return out
}

// Highlighted returns the highlighted entry label.
func (m Model) Highlighted() string {
	v := m.levels[len(m.levels)-1].visible()
	c := m.Cursor()
	if c >= 0 && c < len(v) {
		return v[c].label
	}
	return ""
}

func (m Model) clone() Model {
	m.levels = append([]level(nil), m.levels...)
	return m
}

func closeCmd() tea.Msg { return CloseMsg{} }

func (m Model) move(d int) Model {
	lv := m.top()
	n := len(lv.visible())
	lv.cursor += d
	if lv.cursor >= n {
		lv.cursor = n - 1
	}
	if lv.cursor < 0 {
		lv.cursor = 0
	}
	return m
}

func (m Model) back() Model {
	if len(m.levels) > 1 {
		m.levels = m.levels[:len(m.levels)-1]
	}
	return m
}

func (m Model) selectCurrent() (Model, tea.Cmd) {
	lv := m.top()
	v := lv.visible()
	if lv.cursor < 0 || lv.cursor >= len(v) {
		return m, nil
	}
	e := v[lv.cursor]
	switch e.kind {
	case entryVerb:
		m.levels = append(m.levels, m.verbLevel(e.verb))
	case entryTarget:
		m.levels = append(m.levels, m.targetLevel())
	case entryAction:
		a, _ := m.reg.Get(e.actionID)
		msg := ExecuteMsg{ActionID: e.actionID}
		if a.NeedsTarget {
			msg.TargetID = m.opts.TargetID
		}
		return m, func() tea.Msg { return msg }
	}
	return m, nil
}

func (m Model) Init() tea.Cmd { return nil }

// Update handles keys and paste.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	m = m.clone()
	lv := m.top()
	key := km.String()
	if key == "esc" {
		if lv.searching && lv.query != "" {
			lv.searching = false
			return m, nil
		}
		return m, closeCmd
	}
	if lv.searching {
		switch {
		case key == "enter":
			return m.selectCurrent()
		case key == "up" || key == "ctrl+p":
			return m.move(-1), nil
		case key == "down" || key == "ctrl+n":
			return m.move(1), nil
		case key == "backspace":
			if lv.query == "" {
				return m.back(), nil
			}
			r := []rune(lv.query)
			lv.query = string(r[:len(r)-1])
			lv.cursor = 0
		case key == "ctrl+u":
			lv.query, lv.cursor = "", 0
		case km.Type == tea.KeyRunes || km.Type == tea.KeySpace:
			lv.query += strings.ReplaceAll(string(km.Runes), "\n", " ")
			lv.cursor = 0
		}
		return m, nil
	}
	switch key {
	case "/", "i":
		lv.searching = true
		return m, nil
	case "enter", "l", "right":
		return m.selectCurrent()
	case "h", "left", "backspace":
		return m.back(), nil
	case "q":
		return m, closeCmd
	}
	if n, ok := keys.NavFor(key); ok {
		page := max(1, m.listHeight())
		switch n {
		case keys.Down:
			return m.move(1), nil
		case keys.Up:
			return m.move(-1), nil
		case keys.Top:
			return m.move(-1 << 30), nil
		case keys.Bottom:
			return m.move(1 << 30), nil
		case keys.HalfDown:
			return m.move(max(1, page/2)), nil
		case keys.HalfUp:
			return m.move(-max(1, page/2)), nil
		case keys.PageDown:
			return m.move(page), nil
		case keys.PageUp:
			return m.move(-page), nil
		}
	}
	return m, nil
}

func (m Model) listHeight() int {
	h := m.opts.Height
	if h <= 0 {
		h = 16
	}
	return max(1, h-2)
}

var (
	crumbStyle = lipgloss.NewStyle().Bold(true)
	selStyle   = lipgloss.NewStyle().Reverse(true).Bold(true)
	hintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	queryStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
)

// View renders breadcrumb, search line and entries.
func (m Model) View() string {
	w := m.opts.Width
	if w <= 0 {
		w = 50
	}
	lv := m.levels[len(m.levels)-1]
	v := lv.visible()
	total := len(lv.entries) + len(lv.extra)
	lines := []string{crumbStyle.Render(frame.Truncate(m.Breadcrumb(), w))}
	q := "search: " + lv.query
	if lv.searching {
		q += "▏"
	} else if lv.query != "" {
		q += "  (esc closes · / edit)"
	}
	lines = append(lines, queryStyle.Render(frame.Truncate(fmt.Sprintf("%s  %d/%d", q, len(v), total), w)))
	hintW := 0
	for _, e := range v {
		hintW = max(hintW, frame.Width(e.hint))
	}
	hintW = min(hintW, w/3)
	labelW := max(1, w-2-hintW-2)
	lh := m.listHeight()
	start := 0
	if lv.cursor >= lh {
		start = lv.cursor - lh + 1
	}
	if len(v) == 0 {
		lines = append(lines, hintStyle.Render("  no matching actions"))
	}
	for i := start; i < len(v) && i < start+lh; i++ {
		e := v[i]
		hint := frame.Pad(e.hint, hintW)
		if frame.Width(e.hint) < hintW { // right-align hints
			hint = strings.Repeat(" ", hintW-frame.Width(e.hint)) + e.hint
		}
		mark := "  "
		if i == lv.cursor {
			mark = "> "
		}
		row := mark + frame.Pad(e.label, labelW) + "  "
		if i == lv.cursor {
			lines = append(lines, selStyle.Render(row+hint))
		} else {
			lines = append(lines, row+hintStyle.Render(hint))
		}
	}
	return strings.Join(lines, "\n")
}
