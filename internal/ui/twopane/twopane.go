// Package twopane is the generic Jump/Context modal: an outline list on the
// left, a read-only preview of the highlighted item on the right, nested
// levels with per-level state, a breadcrumb, and one-level Escape.
package twopane

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
)

// Item is one outline row.
type Item struct {
	ID    string
	Title string
	Kind  string // free-form: "window", "file", "doc", "mode", …; used by the kind filter
	Depth int    // outline indentation
	// Nested items open a new level (mode or option list) in the same modal.
	Nested bool
	// Unavailable, when set, is shown instead of fetching a preview.
	Unavailable string
	Data        any
}

// Level is one list of items supplied by a Source.
type Level struct {
	Title       string // breadcrumb label
	Origin      string // originating context (server · host · session · window)
	Items       []Item
	Empty       string // shown when there are no items
	Unsupported string // the whole level is unsupported: shown verbatim
}

// Source supplies levels and previews. Calls run inside tea.Cmds with a
// deadline; replies are generation-checked.
type Source interface {
	Root() Level
	Children(ctx context.Context, item Item) (Level, error)
	Preview(ctx context.Context, item Item) (string, error)
}

// OpenMsg: Enter on a content (non-nested) item.
type OpenMsg struct {
	Item   Item
	Path   []string
	Origin string
}

// CloseMsg: Escape at the root level.
type CloseMsg struct{}

// PreviewMsg and ChildrenMsg are async replies; stale generations are ignored.
type PreviewMsg struct {
	Gen    int
	ItemID string
	Text   string
	Err    error
}

type ChildrenMsg struct {
	Gen    int
	Parent Item
	Level  Level
	Err    error
}

type levelState struct {
	lv       Level
	cursor   int
	scroll   int
	pscroll  int
	query    string
	querying bool
	kind     string // "" = all kinds
}

// Model is the modal.
type Model struct {
	Timeout time.Duration

	src       Source
	stack     []*levelState
	w, h      int
	preview   string
	pgen      int
	cgen      int
	loadingCh bool
	err       string
}

// New opens the modal at src.Root(). Call Init for the first preview.
func New(src Source, w, h int) *Model {
	m := &Model{src: src, w: w, h: h, Timeout: 5 * time.Second}
	m.stack = []*levelState{{lv: src.Root()}}
	return m
}

func (m *Model) SetSize(w, h int) { m.w, m.h = w, h }

// Init requests the preview of the initially highlighted item.
func (m *Model) Init() tea.Cmd { return m.refreshPreview() }

func (m *Model) top() *levelState { return m.stack[len(m.stack)-1] }

// Depth is the number of levels (root = 1).
func (m *Model) Depth() int { return len(m.stack) }

// Path is the breadcrumb titles.
func (m *Model) Path() []string {
	var p []string
	for _, l := range m.stack {
		p = append(p, l.lv.Title)
	}
	return p
}

// Query is the current level's query.
func (m *Model) Query() string { return m.top().query }

// Preview is the current preview text.
func (m *Model) Preview() string { return m.preview }

// Current returns the highlighted item.
func (m *Model) Current() (Item, bool) {
	vis := m.visible(m.top())
	l := m.top()
	if l.cursor < len(vis) {
		return vis[l.cursor], true
	}
	return Item{}, false
}

func (m *Model) visible(l *levelState) []Item {
	var items []Item
	for _, it := range l.lv.Items {
		if l.kind == "" || it.Kind == l.kind {
			items = append(items, it)
		}
	}
	if l.query == "" {
		return items
	}
	titles := make([]string, len(items))
	for i, it := range items {
		titles[i] = strings.ToLower(it.Title)
	}
	ms := fuzzy.Find(strings.ToLower(l.query), titles)
	idx := make([]int, len(ms))
	for i, mt := range ms {
		idx[i] = mt.Index
	}
	sort.Ints(idx) // keep outline order
	out := make([]Item, len(idx))
	for i, j := range idx {
		out[i] = items[j]
	}
	return out
}

func (m *Model) kinds(l *levelState) []string {
	seen := map[string]bool{}
	var ks []string
	for _, it := range l.lv.Items {
		if it.Kind != "" && !seen[it.Kind] {
			seen[it.Kind] = true
			ks = append(ks, it.Kind)
		}
	}
	return ks
}

func (m *Model) refreshPreview() tea.Cmd {
	m.pgen++
	m.top().pscroll = 0
	it, ok := m.Current()
	switch {
	case !ok:
		m.preview = "(nothing selected)"
		return nil
	case it.Unavailable != "":
		m.preview = "unavailable: " + it.Unavailable
		return nil
	}
	m.preview = "loading preview…"
	gen, src, tmo := m.pgen, m.src, m.Timeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tmo)
		defer cancel()
		txt, err := src.Preview(ctx, it)
		return PreviewMsg{Gen: gen, ItemID: it.ID, Text: txt, Err: err}
	}
}

func (m *Model) move(d int) tea.Cmd {
	l := m.top()
	n := len(m.visible(l))
	if n == 0 {
		return nil
	}
	c := l.cursor + d
	if c < 0 {
		c = 0
	}
	if c >= n {
		c = n - 1
	}
	if c == l.cursor {
		return nil
	}
	l.cursor = c
	return m.refreshPreview()
}

// Update handles keys and async replies.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	switch v := msg.(type) {
	case PreviewMsg:
		if v.Gen != m.pgen {
			return nil
		}
		if v.Err != nil {
			m.preview = "preview unavailable: " + v.Err.Error()
		} else if v.Text == "" {
			m.preview = "(empty)"
		} else {
			m.preview = v.Text
		}
	case ChildrenMsg:
		if v.Gen != m.cgen || !m.loadingCh {
			return nil
		}
		m.loadingCh = false
		if v.Err != nil {
			m.err = fmt.Sprintf("cannot open %q: %v", v.Parent.Title, v.Err)
			return nil
		}
		lv := v.Level
		if lv.Title == "" {
			lv.Title = v.Parent.Title
		}
		if lv.Origin == "" {
			lv.Origin = m.top().lv.Origin
		}
		m.stack = append(m.stack, &levelState{lv: lv})
		return m.refreshPreview()
	case tea.KeyMsg:
		return m.key(v)
	}
	return nil
}

func (m *Model) key(k tea.KeyMsg) tea.Cmd {
	l := m.top()
	ks := k.String()
	if ks == "esc" {
		return m.back()
	}
	if l.querying {
		switch ks {
		case "enter", "down", "tab":
			l.querying = false
			return nil
		case "backspace":
			if r := []rune(l.query); len(r) > 0 {
				l.query = string(r[:len(r)-1])
			}
		case "ctrl+u":
			l.query = ""
		default:
			if k.Type == tea.KeyRunes || k.Type == tea.KeySpace {
				l.query += string(k.Runes)
			} else {
				return nil
			}
		}
		l.cursor, l.scroll = 0, 0
		return m.refreshPreview()
	}
	m.err = ""
	switch ks {
	case "j", "down", "ctrl+n":
		return m.move(1)
	case "k", "up", "ctrl+p":
		return m.move(-1)
	case "g", "home":
		return m.move(-1 << 30)
	case "G", "end":
		return m.move(1 << 30)
	case "pgdown":
		return m.move(m.bodyH())
	case "pgup":
		return m.move(-m.bodyH())
	case "J", "ctrl+d":
		l.pscroll += max(1, m.bodyH()/2)
		m.clampPreview()
	case "K", "ctrl+u":
		l.pscroll = max(0, l.pscroll-max(1, m.bodyH()/2))
	case "/":
		l.querying = true
	case "f":
		ks := append([]string{""}, m.kinds(l)...)
		for i, kd := range ks {
			if kd == l.kind {
				l.kind = ks[(i+1)%len(ks)]
				break
			}
		}
		l.cursor, l.scroll = 0, 0
		return m.refreshPreview()
	case "enter":
		return m.enter()
	}
	return nil
}

func (m *Model) back() tea.Cmd {
	m.err = ""
	if m.loadingCh {
		m.loadingCh = false
		m.cgen++
		return nil
	}
	if len(m.stack) == 1 {
		return func() tea.Msg { return CloseMsg{} }
	}
	m.stack = m.stack[:len(m.stack)-1]
	return m.refreshPreview()
}

func (m *Model) enter() tea.Cmd {
	it, ok := m.Current()
	if !ok {
		return nil
	}
	if it.Unavailable != "" {
		m.err = "unavailable: " + it.Unavailable
		return nil
	}
	if !it.Nested {
		path, origin := m.Path(), m.top().lv.Origin
		return func() tea.Msg { return OpenMsg{Item: it, Path: path, Origin: origin} }
	}
	m.cgen++
	m.loadingCh = true
	gen, src, tmo := m.cgen, m.src, m.Timeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tmo)
		defer cancel()
		lv, err := src.Children(ctx, it)
		return ChildrenMsg{Gen: gen, Parent: it, Level: lv, Err: err}
	}
}

// ---- view ----

func (m *Model) bodyH() int { return max(1, m.h-3) }

func (m *Model) widths() (int, int) {
	lw := m.w * 2 / 5
	if lw < 12 {
		lw = min(12, m.w)
	}
	rw := m.w - lw - 3
	if rw < 1 {
		rw = 1
	}
	return lw, rw
}

func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

func sanitize(s string) []string {
	s = ansi.Strip(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\t", "    ")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r >= 0x20 && r != 0x7f && !(r >= 0x80 && r < 0xa0) {
			return r
		}
		return -1
	}, s)
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func (m *Model) clampPreview() {
	l := m.top()
	n := len(sanitize(m.preview))
	maxS := max(0, n-(m.bodyH()-2))
	if l.pscroll > maxS {
		l.pscroll = maxS
	}
}

// window computes visible rows for n entries with overflow indicator rows.
func window(n, cursor, scroll, h int) (start, avail int, overflow bool) {
	if n <= h {
		return 0, n, false
	}
	avail = max(1, h-2)
	if cursor < scroll {
		scroll = cursor
	}
	if cursor >= scroll+avail {
		scroll = cursor - avail + 1
	}
	if scroll > n-avail {
		scroll = n - avail
	}
	return max(0, scroll), avail, true
}

func (m *Model) leftLines(lw, bh int) []string {
	l := m.top()
	if l.lv.Unsupported != "" {
		return []string{"unsupported: " + l.lv.Unsupported}
	}
	vis := m.visible(l)
	if len(vis) == 0 {
		switch {
		case len(l.lv.Items) > 0:
			return []string{"no matches (" + fmt.Sprint(len(l.lv.Items)) + " hidden by query/filter)"}
		case l.lv.Empty != "":
			return []string{l.lv.Empty}
		}
		return []string{"(no items)"}
	}
	start, avail, over := window(len(vis), l.cursor, l.scroll, bh)
	l.scroll = start
	var out []string
	if over {
		out = append(out, fmt.Sprintf("↑ %d more", start))
	}
	for i := start; i < start+avail && i < len(vis); i++ {
		it := vis[i]
		mark := "  "
		if i == l.cursor {
			mark = "> "
		}
		t := mark + strings.Repeat("  ", it.Depth) + it.Title
		if it.Nested {
			t += " ▸"
		}
		if it.Unavailable != "" {
			t += " (unavailable)"
		}
		out = append(out, t)
	}
	if over {
		out = append(out, fmt.Sprintf("↓ %d more", max(0, len(vis)-start-avail)))
	}
	return out
}

func (m *Model) rightLines(bh int) []string {
	lines := sanitize(m.preview)
	l := m.top()
	if len(lines) <= bh {
		return lines
	}
	avail := max(1, bh-2)
	l.pscroll = min(l.pscroll, len(lines)-avail)
	out := []string{fmt.Sprintf("↑ %d lines above", l.pscroll)}
	out = append(out, lines[l.pscroll:l.pscroll+avail]...)
	return append(out, fmt.Sprintf("↓ %d lines below", len(lines)-l.pscroll-avail))
}

// View renders exactly h lines, each at most w cells wide.
func (m *Model) View() string {
	l := m.top()
	lw, rw := m.widths()
	bh := m.bodyH()
	var b strings.Builder
	b.WriteString(fit(strings.Join(m.Path(), " › "), m.w) + "\n")
	info := "origin: " + l.lv.Origin
	if l.lv.Origin == "" {
		info = "origin: (not provided)"
	}
	if l.query != "" || l.querying {
		info += "  /" + l.query
		if l.querying {
			info += "▏"
		}
	}
	if l.kind != "" {
		info += "  kind:" + l.kind
	}
	info += fmt.Sprintf("  %d/%d", len(m.visible(l)), len(l.lv.Items))
	if m.loadingCh {
		info += "  opening…"
	}
	if m.err != "" {
		info += "  ! " + m.err
	}
	b.WriteString(fit(info, m.w) + "\n")
	left, right := m.leftLines(lw, bh), m.rightLines(bh)
	for i := 0; i < bh; i++ {
		var a, c string
		if i < len(left) {
			a = left[i]
		}
		if i < len(right) {
			c = right[i]
		}
		b.WriteString(fit(fit(a, lw)+" │ "+fit(c, rw), m.w) + "\n")
	}
	help := "j/k move · enter open · / query · f kind · J/K scroll preview · esc back"
	if len(m.stack) == 1 {
		help = "j/k move · enter open · / query · f kind · J/K scroll preview · esc close"
	}
	b.WriteString(fit(help, m.w))
	return b.String()
}
