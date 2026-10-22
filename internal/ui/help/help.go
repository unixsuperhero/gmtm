package help

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
	"github.com/unixsuperhero/gmtm/internal/actions"
	"github.com/unixsuperhero/gmtm/internal/ui/frame"
	"github.com/unixsuperhero/gmtm/internal/ui/keys"
)

// CloseMsg asks the orchestrator to close help and restore the prior view.
type CloseMsg struct{}

// Context of the help request.
type Context struct {
	Mode   string // current mode, e.g. "normal"
	Page   string // current page ("" = any)
	Leader string // live leader key, e.g. "ctrl+a"
}

// Entry is one help row.
type Entry struct {
	Section     string
	ID          string
	Label       string
	Description string
	Keys        []string
	Remapped    bool
}

// KeyText is the displayed key column.
func (e Entry) KeyText() string {
	k := "unbound"
	if len(e.Keys) > 0 {
		k = strings.Join(e.Keys, ", ")
	}
	if e.Remapped {
		k += " *"
	}
	return k
}

func (e Entry) searchText() string {
	return e.Label + " " + e.ID + " " + strings.Join(e.Keys, " ") + " " + e.Description
}

func has(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Entries builds ordered help entries from the registry's effective bindings.
func Entries(reg *actions.Registry, ctx Context) []Entry {
	eff := reg.Effective()
	mode := ctx.Mode
	if mode == "" {
		mode = actions.ModeNormal
	}
	used := map[string]bool{}
	var out []Entry
	add := func(section string, pred func(actions.Bound) bool) {
		for _, b := range eff {
			if used[b.Action.ID] || !pred(b) {
				continue
			}
			used[b.Action.ID] = true
			out = append(out, Entry{Section: section, ID: b.Action.ID, Label: b.Action.Label(),
				Description: b.Action.Description, Keys: b.Keys, Remapped: b.Remapped})
		}
	}
	pageOK := func(b actions.Bound) bool {
		return b.Action.Page == "" || ctx.Page == "" || b.Action.Page == ctx.Page
	}
	add("Global", func(b actions.Bound) bool { return has(b.Action.Modes, actions.ModeGlobal) })
	add(strings.ToUpper(mode[:1])+mode[1:]+" mode", func(b actions.Bound) bool {
		return has(b.Action.Modes, mode) && pageOK(b)
	})
	if ctx.Leader != "" && mode != actions.ModeLive {
		add("Live mode (leader "+ctx.Leader+")", func(b actions.Bound) bool { return has(b.Action.Modes, actions.ModeLive) })
	}
	for _, m := range []string{actions.ModeNormal, actions.ModeInsert, actions.ModeLive, actions.ModeJump,
		actions.ModeContext, actions.ModePalette, actions.ModeHelp, actions.ModeNote} {
		add("Other: "+m, func(b actions.Bound) bool { return has(b.Action.Modes, m) })
	}
	add("Other", func(actions.Bound) bool { return true })
	return out
}

// Model is the help widget (value type).
type Model struct {
	ctx       Context
	all       []Entry
	query     string
	searching bool
	offset    int
	width     int
	height    int
}

// New builds help for ctx.
func New(reg *actions.Registry, ctx Context) Model {
	return Model{ctx: ctx, all: Entries(reg, ctx)}
}

// SetSize sets the cell size.
func (m Model) SetSize(w, h int) Model { m.width, m.height = w, h; return m.clamp() }

// Query returns the search query.
func (m Model) Query() string { return m.query }

// Searching reports whether search has focus.
func (m Model) Searching() bool { return m.searching }

// Offset is the first visible body line.
func (m Model) Offset() int { return m.offset }

// Visible returns entries matching the query.
func (m Model) Visible() []Entry {
	if m.query == "" {
		return m.all
	}
	src := make([]string, len(m.all))
	for i, e := range m.all {
		src[i] = e.searchText()
	}
	idx := map[int]bool{}
	for _, mt := range fuzzy.Find(m.query, src) {
		idx[mt.Index] = true
	}
	var out []Entry
	for i, e := range m.all { // keep section order
		if idx[i] {
			out = append(out, e)
		}
	}
	return out
}

func (m Model) w() int {
	if m.width <= 0 {
		return 80
	}
	return m.width
}

func (m Model) bodyHeight() int {
	h := m.height
	if h <= 0 {
		h = 20
	}
	return max(1, h-1)
}

var (
	sectionStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	keyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	remapStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	dim          = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// Lines renders the body lines (sections, entries, wrapped descriptions).
func (m Model) Lines() []string {
	w := m.w()
	vis := m.Visible()
	keyW := 0
	for _, e := range vis {
		keyW = max(keyW, frame.Width(e.KeyText()))
	}
	keyW = min(keyW, max(6, w/3))
	textW := max(10, w-2-keyW-2)
	var out []string
	section := ""
	for _, e := range vis {
		if e.Section != section {
			if section != "" {
				out = append(out, "")
			}
			section = e.Section
			out = append(out, sectionStyle.Render(frame.Truncate(section, w)))
		}
		ks := frame.Pad(e.KeyText(), keyW)
		st := keyStyle
		if e.Remapped {
			st = remapStyle
		}
		text := e.Label
		if e.Description != "" {
			text += " — " + e.Description
		}
		if e.Remapped {
			text += " (remapped)"
		}
		wrapped := strings.Split(ansi.Wrap(text, textW, " "), "\n")
		for i, l := range wrapped {
			prefix := strings.Repeat(" ", keyW)
			if i == 0 {
				prefix = st.Render(ks)
			}
			out = append(out, "  "+prefix+"  "+frame.Truncate(l, textW))
		}
	}
	if len(vis) == 0 {
		out = append(out, dim.Render("  no matching bindings"))
	}
	return out
}

func (m Model) clamp() Model {
	maxOff := max(0, len(m.Lines())-m.bodyHeight())
	m.offset = min(max(0, m.offset), maxOff)
	return m
}

func (m Model) Init() tea.Cmd { return nil }

// Update handles keys.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	key := km.String()
	if m.searching {
		switch {
		case key == "esc" || key == "enter":
			m.searching = false
		case key == "backspace":
			if r := []rune(m.query); len(r) > 0 {
				m.query = string(r[:len(r)-1])
			}
			m.offset = 0
		case key == "ctrl+u":
			m.query, m.offset = "", 0
		case km.Type == tea.KeyRunes || km.Type == tea.KeySpace:
			m.query += strings.ReplaceAll(string(km.Runes), "\n", " ")
			m.offset = 0
		}
		return m.clamp(), nil
	}
	switch key {
	case "/":
		m.searching = true
		return m, nil
	case "esc", "q", "?":
		return m, func() tea.Msg { return CloseMsg{} }
	}
	if n, ok := keys.NavFor(key); ok {
		bh := m.bodyHeight()
		switch n {
		case keys.Down:
			m.offset++
		case keys.Up:
			m.offset--
		case keys.HalfDown:
			m.offset += max(1, bh/2)
		case keys.HalfUp:
			m.offset -= max(1, bh/2)
		case keys.PageDown:
			m.offset += bh
		case keys.PageUp:
			m.offset -= bh
		case keys.Top:
			m.offset = 0
		case keys.Bottom:
			m.offset = 1 << 30
		}
	}
	return m.clamp(), nil
}

// HeaderText is the one-line header: mode, search, counts, scroll position.
func (m Model) HeaderText() string {
	lines := m.Lines()
	s := fmt.Sprintf("Help · %s mode", orDefault(m.ctx.Mode, "normal"))
	if m.query != "" || m.searching {
		s += " · /" + m.query
		if m.searching {
			s += "▏"
		}
	}
	s += fmt.Sprintf(" · %d/%d", len(m.Visible()), len(m.all))
	if len(lines) > m.bodyHeight() {
		s += fmt.Sprintf(" · lines %d-%d of %d", m.offset+1, min(len(lines), m.offset+m.bodyHeight()), len(lines))
	}
	s += " · * remapped · / search · esc close"
	return s
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// View renders header + scrolled body.
func (m Model) View() string {
	lines := m.Lines()
	bh := m.bodyHeight()
	end := min(len(lines), m.offset+bh)
	body := lines[min(m.offset, end):end]
	out := append([]string{dim.Render(frame.Truncate(m.HeaderText(), m.w()))}, body...)
	return strings.Join(out, "\n")
}
