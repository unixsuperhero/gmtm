// Package pathpicker is mtm's built-in fuzzy path picker, modelled on
// snacks.nvim's file picker: a prompt, a fuzzy-ranked candidate list produced
// by fd (fdfind, or find as a fallback) relative to a base directory, and a
// preview pane. It only ever browses the local filesystem.
//
// Modes: the prompt starts in insert mode (typing filters). Escape switches to
// normal mode (j/k move); Escape again, q or ctrl+c cancels.
package pathpicker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"

	"github.com/unixsuperhero/gmtm/internal/ui/frame"
	"github.com/unixsuperhero/gmtm/internal/ui/readline"
)

// Options configures a picker.
type Options struct {
	ID       string // echoed in ChosenMsg/CancelMsg so hosts can route replies
	Mode     Mode
	Multiple bool
	Base     string // starting directory; default $HOME
	Title    string
	Finder   Finder // nil: NewFinder()
}

// ChosenMsg reports the chosen absolute paths.
type ChosenMsg struct {
	ID    string
	Paths []string
}

// CancelMsg reports that the picker was dismissed without a choice.
type CancelMsg struct{ ID string }

// ErrCanceled is returned by Run when the picker is dismissed.
var ErrCanceled = errors.New("path picker cancelled")

type listMsg struct {
	inst  uint64
	gen   int
	items []string
	err   error
}

type previewMsg struct {
	inst uint64
	gen  int
	pv   Preview
}

type row struct {
	rel   string // relative to base ("." = base itself), or absolute
	idx   []int  // matched byte indexes into rel
	isNew bool   // SaveAs: a name not in the listing
}

var instances atomic.Uint64

// Model is the picker. It is a pointer model: Update mutates and returns a Cmd.
type Model struct {
	opts   Options
	finder Finder
	inst   uint64
	home   string
	base   string
	w, h   int

	query  []rune
	cur    int
	normal bool
	tilde  bool // the last edit reset to home via "~"
	editor readline.Editor

	items    []string
	listing  bool
	listErr  string
	listGen  int
	focusRel string // highlight this entry after the next listing (going up)

	rows []row
	sel  int
	off  int

	marked    map[string]bool
	markOrder []string

	pv    Preview
	pvGen int
	pvFor string
	note  string
}

// New builds a picker. Call Init (or Relist) to start the first listing.
func New(o Options) *Model {
	m := &Model{opts: o, finder: o.Finder, inst: instances.Add(1), marked: map[string]bool{}}
	if m.finder == nil {
		m.finder = NewFinder()
	}
	m.home, _ = os.UserHomeDir()
	if m.home == "" {
		m.home = "/"
	}
	m.base = m.home
	if o.Base != "" {
		if b := expand(o.Base, m.home); isDir(b) {
			m.base = b
		} else {
			m.note = "base " + o.Base + " not found; using home"
		}
	}
	return m
}

// Init starts the first listing.
func (m *Model) Init() tea.Cmd { return m.relist() }

// SetSize sets the exact cell area View renders into.
func (m *Model) SetSize(w, h int) { m.w, m.h = w, h; m.scroll() }

// Base is the current base directory.
func (m *Model) Base() string { return m.base }

// Query is the current prompt text.
func (m *Model) Query() string { return string(m.query) }

// InputMode is "insert" or "normal".
func (m *Model) InputMode() string {
	if m.normal {
		return "normal"
	}
	return "insert"
}

// Counts returns matched and total candidate counts.
func (m *Model) Counts() (matched, total int) {
	n := 0
	for _, r := range m.rows {
		if !r.isNew && r.rel != "." {
			n++
		}
	}
	return n, len(m.items)
}

// Highlighted is the absolute path under the cursor ("" when none).
func (m *Model) Highlighted() string {
	if r, ok := m.current(); ok {
		return m.abs(r)
	}
	return ""
}

// Marked lists marked paths in marking order.
func (m *Model) Marked() []string { return append([]string(nil), m.markOrder...) }

// Listing reports whether a listing is in flight.
func (m *Model) Listing() bool { return m.listing }

// Note is the latest status note.
func (m *Model) Note() string { return m.note }

// ---- listing and filtering ----

func (m *Model) relist() tea.Cmd {
	m.listGen++
	m.listing, m.listErr, m.items = true, "", nil
	m.refilter()
	f, base, mode, gen, inst := m.finder, m.base, m.opts.Mode, m.listGen, m.inst
	if mode == SaveAs {
		mode = Files
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), ListTimeout)
		defer cancel()
		items, err := f.List(ctx, base, mode)
		sortItems(items)
		return listMsg{inst: inst, gen: gen, items: items, err: err}
	}
}

// sortItems orders shallow paths first, then lexically, for a stable list.
func sortItems(items []string) {
	sort.SliceStable(items, func(a, b int) bool {
		da, db := strings.Count(items[a], "/"), strings.Count(items[b], "/")
		if da != db {
			return da < db
		}
		return items[a] < items[b]
	})
}

func (m *Model) refilter() {
	q := string(m.query)
	m.rows = m.rows[:0]
	if q == "" {
		if m.opts.Mode == Dirs {
			m.rows = append(m.rows, row{rel: "."})
		}
		for _, it := range m.items {
			m.rows = append(m.rows, row{rel: it})
		}
	} else {
		exact := false
		var found []row
		for _, mt := range fuzzy.Find(q, m.items) {
			found = append(found, row{rel: mt.Str, idx: append([]int(nil), mt.MatchedIndexes...)})
			exact = exact || mt.Str == q
		}
		if m.opts.Mode == SaveAs && !exact {
			m.rows = append(m.rows, row{rel: q, isNew: true})
		}
		m.rows = append(m.rows, found...)
	}
	m.sel = 0
	m.off = 0
}

func (m *Model) setBase(dir string) tea.Cmd {
	m.base = filepath.Clean(dir)
	m.query, m.cur = nil, 0
	return m.relist()
}

// ---- update ----

func emit(msg tea.Msg) tea.Cmd { return func() tea.Msg { return msg } }

// Update handles keys and this picker's async replies; others are ignored.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch v := msg.(type) {
	case listMsg:
		if v.inst != m.inst || v.gen != m.listGen {
			return nil // stale or foreign
		}
		m.listing, m.items = false, v.items
		if v.err != nil {
			m.listErr = v.err.Error()
		}
		m.refilter()
		if m.focusRel != "" {
			for i, r := range m.rows {
				if r.rel == m.focusRel {
					m.sel = i
					break
				}
			}
			m.focusRel = ""
			m.scroll()
		}
		return m.previewCmd()
	case previewMsg:
		if v.inst == m.inst && v.gen == m.pvGen {
			m.pv = v.pv
		}
		return nil
	case tea.KeyMsg:
		return m.key(v)
	}
	return nil
}

func (m *Model) key(k tea.KeyMsg) tea.Cmd {
	s := k.String()
	switch s {
	case "ctrl+c":
		return emit(CancelMsg{ID: m.opts.ID})
	case "esc":
		if m.normal {
			return emit(CancelMsg{ID: m.opts.ID})
		}
		m.normal = true
		return nil
	case "enter":
		return m.choose()
	case "tab":
		return m.descend()
	case "up", "ctrl+p":
		return m.move(-1)
	case "down", "ctrl+n", "ctrl+j":
		return m.move(1)
	case "pgdown":
		return m.move(max(1, m.listHeight()/2))
	case "pgup":
		return m.move(-max(1, m.listHeight()/2))
	case " ":
		if m.opts.Multiple {
			return m.toggle()
		}
	}
	if m.normal {
		return m.normalKey(s)
	}
	return m.insertKey(k, s)
}

func (m *Model) normalKey(s string) tea.Cmd {
	switch s {
	case "j":
		return m.move(1)
	case "k", "ctrl+k":
		return m.move(-1)
	case "ctrl+f":
		return m.move(max(1, m.listHeight()))
	case "ctrl+b":
		return m.move(-max(1, m.listHeight()))
	case "ctrl+d":
		return m.move(max(1, m.listHeight()/2))
	case "ctrl+u":
		return m.move(-max(1, m.listHeight()/2))
	case "g", "home":
		return m.move(-len(m.rows))
	case "G", "end":
		return m.move(len(m.rows))
	case "l":
		return m.descend()
	case "h", "-", "backspace":
		return m.up()
	case "~":
		m.focusRel = ""
		return m.setBase(m.home)
	case "i", "/":
		m.normal = false
	case "a":
		m.normal, m.cur = false, len(m.query)
	case "I":
		m.normal, m.cur = false, 0
	case "q":
		return emit(CancelMsg{ID: m.opts.ID})
	}
	return nil
}

func (m *Model) insertKey(k tea.KeyMsg, s string) tea.Cmd {
	if (s == "backspace" || s == "ctrl+h") && len(m.query) == 0 {
		return m.up()
	}
	if q, pos, handled := m.editor.Edit(m.query, m.cur, s, true); handled {
		m.query, m.cur = q, pos
		return m.afterEdit()
	}
	if k.Type == tea.KeyRunes || k.Type == tea.KeySpace {
		rs := k.Runes
		if k.Type == tea.KeySpace {
			rs = []rune{' '}
		}
		q := make([]rune, 0, len(m.query)+len(rs))
		q = append(append(append(q, m.query[:m.cur]...), rs...), m.query[m.cur:]...)
		m.query, m.cur = q, m.cur+len(rs)
		return m.afterEdit()
	}
	return nil
}

// afterEdit applies the query's navigation forms ("~", absolute paths) and refilters.
func (m *Model) afterEdit() tea.Cmd {
	q := string(m.query)
	wasTilde := m.tilde
	m.tilde = false
	m.note = ""
	switch {
	case q == "~":
		m.tilde = true
		return m.setBase(m.home)
	case wasTilde && q == "/":
		// "~/" typed key by key: stay at home rather than jumping to root
		m.query, m.cur = nil, 0
		m.refilter()
		return m.previewCmd()
	case strings.HasPrefix(q, "/") || strings.HasPrefix(q, "~/"):
		exp := expand(q, m.home)
		i := strings.LastIndex(exp, "/")
		dir, rest := exp[:i+1], exp[i+1:]
		if isDir(dir) {
			cmd := m.setBase(dir)
			m.query = []rune(rest)
			m.cur = len(m.query)
			m.refilter()
			return cmd
		}
		m.note = "no such directory: " + dir
	}
	m.refilter()
	return m.previewCmd()
}

func (m *Model) current() (row, bool) {
	if m.sel < 0 || m.sel >= len(m.rows) {
		return row{}, false
	}
	return m.rows[m.sel], true
}

func (m *Model) abs(r row) string {
	if filepath.IsAbs(r.rel) {
		return filepath.Clean(r.rel)
	}
	return filepath.Join(m.base, r.rel)
}

func (m *Model) move(d int) tea.Cmd {
	if len(m.rows) == 0 {
		return nil
	}
	m.sel = max(0, min(len(m.rows)-1, m.sel+d))
	m.scroll()
	return m.previewCmd()
}

func (m *Model) choose() tea.Cmd {
	if m.opts.Multiple && len(m.markOrder) > 0 {
		return emit(ChosenMsg{ID: m.opts.ID, Paths: m.Marked()})
	}
	r, ok := m.current()
	if !ok {
		if m.listing {
			m.note = "still listing…"
		} else {
			m.note = "nothing matches"
		}
		return nil
	}
	return emit(ChosenMsg{ID: m.opts.ID, Paths: []string{m.abs(r)}})
}

func (m *Model) descend() tea.Cmd {
	r, ok := m.current()
	if !ok || r.isNew {
		return nil
	}
	p := m.abs(r)
	if !isDir(p) {
		p = filepath.Dir(p) // a file: descend into its directory
	}
	if p == m.base {
		m.note = "already at " + m.display(p)
		return nil
	}
	m.focusRel = ""
	return m.setBase(p)
}

func (m *Model) up() tea.Cmd {
	parent := filepath.Dir(m.base)
	if parent == m.base {
		m.note = "at filesystem root"
		return nil
	}
	m.focusRel = filepath.Base(m.base)
	return m.setBase(parent)
}

func (m *Model) toggle() tea.Cmd {
	r, ok := m.current()
	if !ok || r.isNew {
		return nil
	}
	p := m.abs(r)
	if m.marked[p] {
		delete(m.marked, p)
		for i, q := range m.markOrder {
			if q == p {
				m.markOrder = append(m.markOrder[:i], m.markOrder[i+1:]...)
				break
			}
		}
	} else {
		m.marked[p] = true
		m.markOrder = append(m.markOrder, p)
	}
	return m.move(1)
}

func (m *Model) previewCmd() tea.Cmd {
	r, ok := m.current()
	target := ""
	if ok {
		target = m.abs(r)
	}
	if target == m.pvFor && target != "" {
		return nil
	}
	m.pvGen++
	m.pvFor = target
	gen, inst := m.pvGen, m.inst
	if !ok {
		m.pv = Preview{}
		return nil
	}
	isNew := r.isNew
	return func() tea.Msg {
		if isNew {
			if _, err := os.Stat(target); err != nil {
				return previewMsg{inst: inst, gen: gen, pv: Preview{Path: target, Note: "new file"}}
			}
		}
		return previewMsg{inst: inst, gen: gen, pv: LoadPreview(target)}
	}
}

// ---- view ----

var (
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	baseStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	matchStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	selStyle   = lipgloss.NewStyle().Reverse(true).Bold(true)
	newStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

func (m *Model) listHeight() int { return max(0, m.h-3) }

func (m *Model) previewWidth() int {
	if m.w < 60 {
		return 0
	}
	return m.w * 40 / 100
}

func (m *Model) scroll() {
	lh := m.listHeight()
	if lh <= 0 {
		return
	}
	if m.sel < m.off {
		m.off = m.sel
	}
	if m.sel >= m.off+lh {
		m.off = m.sel - lh + 1
	}
	m.off = max(0, min(m.off, max(0, len(m.rows)-lh)))
}

func (m *Model) display(p string) string {
	if m.home != "/" && (p == m.home || strings.HasPrefix(p, m.home+"/")) {
		return "~" + p[len(m.home):]
	}
	return p
}

// View renders exactly SetSize's area: no wrapping, overflow marked.
func (m *Model) View() string {
	w, h := m.w, m.h
	if w <= 0 || h <= 0 {
		return ""
	}
	if w < 20 || h < 4 {
		return strings.Join(frame.FitLines("path picker: too small", w, h), "\n")
	}
	lines := make([]string, 0, h)
	lines = append(lines, m.header(w), m.prompt(w))
	lh := m.listHeight()
	pw := m.previewWidth()
	lw := w
	if pw > 0 {
		lw = w - pw - 1
	}
	list := m.listLines(lw, lh)
	var pv []string
	if pw > 0 {
		pv = frame.FitLines(m.previewText(), pw, lh)
	}
	for i := 0; i < lh; i++ {
		l := list[i]
		if pw > 0 {
			l += dimStyle.Render("│") + pv[i]
		}
		lines = append(lines, l)
	}
	lines = append(lines, m.footer(w))
	return strings.Join(lines, "\n")
}

func (m *Model) header(w int) string {
	title := m.opts.Title
	if title == "" {
		title = "pick " + m.opts.Mode.String()
		if m.opts.Multiple {
			title += "s"
		}
	}
	right := ""
	if n := len(m.markOrder); n > 0 {
		right = fmt.Sprintf(" · %d marked", n)
	}
	lead := title + " · "
	avail := w - frame.Width(lead) - frame.Width(right)
	base := leftTrunc(m.display(m.base), avail)
	return frame.Pad(titleStyle.Render(frame.Truncate(title, w))+dimStyle.Render(" · ")+baseStyle.Render(base)+right, w)
}

func (m *Model) prompt(w int) string {
	mode := "I"
	if m.normal {
		mode = "N"
	}
	matched, total := m.Counts()
	right := fmt.Sprintf(" %d/%d", matched, total)
	switch {
	case m.listing:
		right = " listing…" + right
	case len(m.items) >= MaxEntries:
		right = " capped" + right
	}
	lead := "[" + mode + "] > "
	q := string(m.query)
	if !m.normal {
		q = string(m.query[:m.cur]) + "█" + string(m.query[m.cur:])
	}
	avail := w - frame.Width(lead) - frame.Width(right)
	if avail < 1 {
		return frame.Pad(lead+q, w)
	}
	q = leftTrunc(q, avail)
	return lead + frame.Pad(q, avail) + dimStyle.Render(right)
}

func (m *Model) listLines(w, h int) []string {
	out := make([]string, h)
	for i := 0; i < h; i++ {
		ri := m.off + i
		if ri >= len(m.rows) {
			msg := ""
			if i == 0 {
				switch {
				case m.listing:
					msg = "  listing " + m.display(m.base) + "…"
				case m.listErr != "" && len(m.rows) == 0:
					msg = errStyle.Render("  " + m.listErr)
				case len(m.rows) == 0:
					msg = dimStyle.Render("  no matches")
				}
			}
			out[i] = frame.Pad(msg, w)
			continue
		}
		out[i] = m.rowLine(m.rows[ri], ri == m.sel, w)
	}
	return out
}

func (m *Model) rowLine(r row, sel bool, w int) string {
	mark := "  "
	if m.marked[m.abs(r)] {
		mark = "● "
	}
	if sel {
		mark = "▌" + mark[len(mark)-1:]
		if m.marked[m.abs(r)] {
			mark = "▌●"
		}
	}
	text := r.rel
	switch {
	case r.isNew:
		text = "+ new file: " + text
	case r.rel == ".":
		text = "./ (this directory)"
	case m.opts.Mode == Dirs:
		text += "/"
	}
	tw := w - frame.Width(mark)
	if sel {
		return mark + selStyle.Render(frame.Pad(leftTrunc(Sanitize(text), tw), tw))
	}
	if r.isNew {
		return mark + newStyle.Render(frame.Pad(leftTrunc(Sanitize(text), tw), tw))
	}
	return mark + frame.Pad(highlight(text, r.idx, tw), tw)
}

func (m *Model) previewText() string {
	pv := m.pv
	var b strings.Builder
	head := " " + m.display(pv.Path)
	if pv.Note != "" {
		head += " · " + pv.Note
	}
	b.WriteString(dimStyle.Render(head))
	for _, l := range pv.Lines {
		b.WriteString("\n " + l)
	}
	return b.String()
}

func (m *Model) footer(w int) string {
	var parts []string
	if m.note != "" {
		parts = append(parts, m.note)
	} else if m.listErr != "" {
		parts = append(parts, m.listErr)
	}
	if lh := m.listHeight(); len(m.rows) > lh {
		above, below := m.off, len(m.rows)-m.off-lh
		parts = append(parts, fmt.Sprintf("↑%d ↓%d", above, max(0, below)))
	}
	hint := "↵ choose · tab into · ⌫ up · ~ home · esc normal"
	if m.normal {
		hint = "j/k move · ↵ choose · l/tab into · h up · i insert · esc/q cancel"
	}
	if m.opts.Multiple {
		hint = "space mark · " + hint
	}
	parts = append(parts, hint)
	return dimStyle.Render(frame.Pad(strings.Join(parts, " · "), w))
}

// ---- helpers ----

func expand(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return home + p[1:]
	}
	return p
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// leftTrunc keeps the tail of s within w cells, prefixing "…" when cut.
func leftTrunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if frame.Width(s) <= w {
		return s
	}
	rs := []rune(s)
	for len(rs) > 0 && frame.Width(string(rs))+1 > w {
		rs = rs[1:]
	}
	return "…" + string(rs)
}

// highlight styles matched bytes of s and fits it to w cells (tail kept).
func highlight(s string, idx []int, w int) string {
	s = strings.ToValidUTF8(s, "�")
	if frame.Width(s) > w || len(idx) == 0 {
		return leftTrunc(Sanitize(s), w)
	}
	hit := map[int]bool{}
	for _, i := range idx {
		hit[i] = true
	}
	var b strings.Builder
	for i, r := range s {
		if r < ' ' || r == 0x7f {
			continue
		}
		if hit[i] {
			b.WriteString(matchStyle.Render(string(r)))
		} else {
			b.WriteRune(r)
		}
	}
	return ansi.Truncate(b.String(), w, "")
}
