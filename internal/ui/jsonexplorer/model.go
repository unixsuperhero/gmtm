package jsonexplorer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
	"github.com/unixsuperhero/gmtm/internal/ui/readline"
)

// Mode is the Vim-like interaction mode shown in the frame.
type Mode string

const (
	ModeNormal Mode = "normal"
	ModeInsert Mode = "insert"
)

var filterCycle = []Kind{"", KString, KNumber, KBool, KNull, KObject, KArray}

// Model is the explorer's Bubble Tea model.
type Model struct {
	title   string
	root    *Node
	all     []*Node
	visible []*Node
	cursor  int
	top     int
	w, h    int
	mode    Mode
	input   textinput.Model
	editor  readline.Editor
	query   string
	matches []*Node // pre-order
	matchAt int     // index into matches, -1 none
	filter  Kind
	keep    map[*Node]bool // nodes kept by the type filter
	detail  viewport.Model
	detailN *Node
	note    string
}

// New builds a model from raw JSON; it returns a *ParseError for invalid input.
func New(data []byte, title string) (*Model, error) {
	root, err := Build(data)
	if err != nil {
		return nil, err
	}
	ti := textinput.New()
	ti.Prompt = "/"
	ti.CharLimit = 200
	m := &Model{title: title, root: root, all: Flatten(root), mode: ModeNormal, input: ti, matchAt: -1, w: 80, h: 24}
	m.detail = viewport.New(10, 3)
	m.rebuild(root)
	return m, nil
}

// --- state accessors (used by tests) ---

func (m *Model) Mode() Mode                       { return m.mode }
func (m *Model) Selected() *Node                  { return m.visible[m.cursor] }
func (m *Model) Visible() []*Node                 { return m.visible }
func (m *Model) Filter() Kind                     { return m.filter }
func (m *Model) MatchCount() (matched, total int) { return len(m.matches), len(m.all) }

// Breadcrumb is the path of the selected node.
func (m *Model) Breadcrumb() string { return m.Selected().Path }

// SetQuery runs the fuzzy search over keys and values.
func (m *Model) SetQuery(q string) {
	m.query = q
	m.matches, m.matchAt = nil, -1
	if q == "" {
		return
	}
	cands := make([]string, len(m.all))
	for i, n := range m.all {
		cands[i] = n.Key + " " + n.Text
	}
	idx := []int{}
	for _, r := range fuzzy.Find(q, cands) {
		idx = append(idx, r.Index)
	}
	sort.Ints(idx)
	for _, i := range idx {
		m.matches = append(m.matches, m.all[i])
	}
}

func (m *Model) isMatch(n *Node) bool {
	for _, x := range m.matches {
		if x == n {
			return true
		}
	}
	return false
}

// jump moves to the next (dir=1) or previous (dir=-1) match, wrapping.
func (m *Model) jump(dir int) {
	if len(m.matches) == 0 {
		return
	}
	cur := m.Selected()
	pos := indexOf(m.all, cur)
	next := -1
	if dir > 0 {
		for i, n := range m.matches {
			if indexOf(m.all, n) > pos {
				next = i
				break
			}
		}
		if next < 0 {
			next = 0
		}
	} else {
		for i := len(m.matches) - 1; i >= 0; i-- {
			if indexOf(m.all, m.matches[i]) < pos {
				next = i
				break
			}
		}
		if next < 0 {
			next = len(m.matches) - 1
		}
	}
	m.matchAt = next
	m.reveal(m.matches[next])
}

func indexOf(xs []*Node, n *Node) int {
	for i, x := range xs {
		if x == n {
			return i
		}
	}
	return -1
}

// reveal expands ancestors of n and selects it.
func (m *Model) reveal(n *Node) {
	for p := n.Parent; p != nil; p = p.Parent {
		p.Expanded = true
	}
	m.rebuild(n)
}

// rebuild recomputes the visible rows and puts the cursor on sel if shown.
func (m *Model) rebuild(sel *Node) {
	m.keep = nil
	if m.filter != "" {
		m.keep = map[*Node]bool{}
		var mark func(*Node) bool
		mark = func(n *Node) bool {
			k := n.Kind == m.filter
			for _, c := range n.Children {
				if mark(c) {
					k = true
				}
			}
			if k {
				m.keep[n] = true
			}
			return k
		}
		mark(m.root)
		m.keep[m.root] = true
	}
	m.visible = m.visible[:0]
	var walk func(*Node)
	walk = func(n *Node) {
		if m.keep != nil && !m.keep[n] {
			return
		}
		m.visible = append(m.visible, n)
		if n.IsContainer() && (n.Expanded || m.keep != nil) {
			for _, c := range n.Children {
				walk(c)
			}
		}
	}
	walk(m.root)
	m.cursor = 0
	for i, n := range m.visible {
		if n == sel {
			m.cursor = i
			break
		}
	}
	// if sel is hidden, fall back to the nearest visible ancestor
	if sel != nil && m.visible[m.cursor] != sel {
		for p := sel.Parent; p != nil; p = p.Parent {
			if i := indexOf(m.visible, p); i >= 0 {
				m.cursor = i
				break
			}
		}
	}
	m.clamp()
	m.syncDetail()
}

func (m *Model) layout() (listH, detailVH int) {
	inner := m.h - 2
	detailVH = inner / 3
	if detailVH < 2 {
		detailVH = 2
	}
	if detailVH > 10 {
		detailVH = 10
	}
	listH = inner - 3 - detailVH
	if listH < 1 {
		listH = 1
	}
	return
}

func (m *Model) tooSmall() bool { return m.w < 30 || m.h < 10 }

func (m *Model) clamp() {
	if m.cursor >= len(m.visible) {
		m.cursor = len(m.visible) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	listH, _ := m.layout()
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+listH {
		m.top = m.cursor - listH + 1
	}
	if m.top < 0 {
		m.top = 0
	}
}

func (m *Model) syncDetail() {
	n := m.Selected()
	_, vh := m.layout()
	iw := m.w - 2
	if iw < 1 {
		iw = 1
	}
	m.detail.Width, m.detail.Height = iw, vh
	if n != m.detailN {
		m.detailN = n
		m.detail.SetContent(lipgloss.NewStyle().Width(iw).Render(Pretty(n)))
		m.detail.GotoTop()
	}
}

func (m *Model) move(delta int) {
	m.cursor += delta
	m.clamp()
	m.syncDetail()
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.detailN = nil
		m.clamp()
		m.syncDetail()
	case tea.KeyMsg:
		if m.mode == ModeInsert {
			return m.updateInsert(msg)
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

func (m *Model) updateInsert(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = ModeNormal
		m.input.Blur()
		m.SetQuery("")
		m.input.SetValue("")
		return m, nil
	case "enter":
		m.mode = ModeNormal
		m.input.Blur()
		m.SetQuery(m.input.Value())
		m.jumpFrom()
		return m, nil
	}
	cmd := m.editor.Update(&m.input, msg, false)
	m.SetQuery(m.input.Value())
	return m, cmd
}

// jumpFrom selects the first match at or after the cursor.
func (m *Model) jumpFrom() {
	if len(m.matches) == 0 {
		return
	}
	pos := indexOf(m.all, m.Selected())
	for i, n := range m.matches {
		if indexOf(m.all, n) >= pos {
			m.matchAt = i
			m.reveal(n)
			return
		}
	}
	m.matchAt = 0
	m.reveal(m.matches[0])
}

func (m *Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	listH, _ := m.layout()
	n := m.Selected()
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "d":
		m.move(listH / 2)
	case "u":
		m.move(-(listH / 2))
	case "ctrl+f", "pgdown":
		m.move(listH)
	case "ctrl+b", "pgup":
		m.move(-listH)
	case "g", "home":
		m.cursor = 0
		m.clamp()
		m.syncDetail()
	case "G", "end":
		m.cursor = len(m.visible) - 1
		m.clamp()
		m.syncDetail()
	case "l", "right", "enter":
		if n.IsContainer() && !n.Expanded {
			n.Expanded = true
			m.rebuild(n)
		} else if n.IsContainer() && len(n.Children) > 0 && m.cursor+1 < len(m.visible) {
			m.move(1)
		}
	case "h", "left":
		if n.IsContainer() && n.Expanded && m.keep == nil {
			n.Expanded = false
			m.rebuild(n)
		} else if n.Parent != nil {
			m.rebuild(n.Parent)
		}
	case "/":
		m.mode = ModeInsert
		m.input.SetValue("")
		m.input.Focus()
		m.SetQuery("")
		return m, textinput.Blink
	case "n":
		m.jump(1)
	case "N":
		m.jump(-1)
	case "t":
		i := 0
		for j, k := range filterCycle {
			if k == m.filter {
				i = j
			}
		}
		m.filter = filterCycle[(i+1)%len(filterCycle)]
		m.rebuild(n)
	case "J":
		m.detail.ScrollDown(1)
	case "K":
		m.detail.ScrollUp(1)
	case "esc":
		m.SetQuery("")
		m.input.SetValue("")
		if m.filter != "" {
			m.filter = ""
			m.rebuild(n)
		}
	}
	return m, nil
}

// --- view ---

var (
	selStyle = lipgloss.NewStyle().Reverse(true)
	dimStyle = lipgloss.NewStyle().Faint(true)
	hitStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
)

func fit(s string, w int) string {
	if w < 1 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if pad := w - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

func (m *Model) row(n *Node, selected bool, w int) string {
	marker := "  "
	if n.IsContainer() {
		marker = "▸ "
		if n.Expanded || m.keep != nil {
			marker = "▾ "
		}
	}
	var body string
	if n.IsContainer() {
		body = n.Key + ": " + n.TypeLabel()
		if n.Parent == nil {
			body = n.TypeLabel()
		}
	} else {
		body = n.Key + ": " + n.Scalar + "  " + n.TypeLabel()
	}
	line := strings.Repeat("  ", n.Depth) + marker + body
	plain := fit(line, w)
	switch {
	case selected:
		return selStyle.Render(plain)
	case m.query != "" && m.isMatch(n):
		return hitStyle.Render(plain)
	}
	return plain
}

func (m *Model) frameTop() string {
	label := fmt.Sprintf("╭─ cmtm ─ mode: %s ─ project: no-project ─", m.mode)
	w := lipgloss.Width(label)
	if w > m.w-1 {
		label = ansi.Truncate(label, m.w-1, "")
		w = lipgloss.Width(label)
	}
	return label + strings.Repeat("─", max(0, m.w-w-1)) + "╮"
}

func (m *Model) View() string {
	if m.w < 12 {
		return fmt.Sprintf("cmtm %s\nno-project", m.mode)
	}
	iw := m.w - 2
	lines := []string{}
	if m.tooSmall() {
		lines = append(lines, fit("terminal too small", iw), fit("project: no-project", iw), fit("mode: "+string(m.mode), iw))
		for len(lines) < m.h-2 {
			lines = append(lines, fit("", iw))
		}
	} else {
		listH, _ := m.layout()
		lines = append(lines, fit("path: "+m.Breadcrumb(), iw))
		for i := 0; i < listH; i++ {
			if j := m.top + i; j < len(m.visible) {
				lines = append(lines, m.row(m.visible[j], j == m.cursor, iw))
			} else {
				lines = append(lines, fit("", iw))
			}
		}
		n := m.Selected()
		lines = append(lines, dimStyle.Render(fit("── detail: "+n.TypeLabel()+" ──", iw)))
		for _, l := range strings.Split(m.detail.View(), "\n") {
			lines = append(lines, fit(l, iw))
		}
		lines = append(lines, fit(m.footer(), iw))
	}
	for len(lines) > m.h-2 {
		lines = lines[:len(lines)-1]
	}
	var sb strings.Builder
	sb.WriteString(m.frameTop() + "\n")
	for _, l := range lines {
		sb.WriteString("│" + l + "│\n")
	}
	sb.WriteString("╰" + strings.Repeat("─", m.w-2) + "╯")
	return sb.String()
}

func (m *Model) footer() string {
	if m.mode == ModeInsert {
		return m.input.View() + fmt.Sprintf("  %d/%d matched", len(m.matches), len(m.all))
	}
	s := m.title
	if m.query != "" {
		cur := 0
		if m.matchAt >= 0 {
			cur = m.matchAt + 1
		}
		s += fmt.Sprintf("  /%s %d/%d matched (#%d)", m.query, len(m.matches), len(m.all), cur)
	}
	if m.filter != "" {
		s += "  type=" + string(m.filter)
	}
	return s + "  j/k h/l / n/N t q"
}

// Run shows the explorer full screen. Invalid JSON returns a *ParseError
// before any terminal state is touched.
func Run(data []byte, title string) error {
	m, err := New(data, title)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}
