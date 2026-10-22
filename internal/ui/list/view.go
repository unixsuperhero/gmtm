package list

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/unixsuperhero/gmtm/internal/ui/frame"
)

var (
	selStyle    = lipgloss.NewStyle().Bold(true).Reverse(true)
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	matchStyle  = lipgloss.NewStyle().Underline(true).Foreground(lipgloss.Color("11"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	activeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	pinStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))
	starStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
)

// layout counts body lines and the selected row's line span.
func (m Model) layout() (total, selStart, selEnd int) {
	selStart, selEnd = -1, -1
	for _, r := range m.res.Rows {
		if !r.Header && r.Index == m.sel {
			selStart = total
			selEnd = total
			if m.Detail != nil {
				selEnd++
				total++
			}
		}
		total++
	}
	return
}

func (m Model) ensureVisible() Model {
	bh := m.bodyHeight()
	total, s, e := m.layout()
	if s >= 0 {
		if s < m.offset {
			m.offset = s
			// keep the group header just above the selection when it fits
			if s > 0 && e-s+2 <= bh && m.lineIsHeader(s-1) {
				m.offset = s - 1
			}
		}
		if e >= m.offset+bh {
			m.offset = e - bh + 1
		}
	}
	if m.offset > total-bh {
		m.offset = total - bh
	}
	if m.offset < 0 {
		m.offset = 0
	}
	return m
}

func (m Model) lineIsHeader(line int) bool {
	n := 0
	for _, r := range m.res.Rows {
		if n == line {
			return r.Header
		}
		n++
		if !r.Header && r.Index == m.sel && m.Detail != nil {
			n++
		}
	}
	return false
}

func (m Model) w() int {
	if m.width <= 0 {
		return 80
	}
	return m.width
}

func highlight(label string, idx []int) string {
	if len(idx) == 0 {
		return label
	}
	hit := map[int]bool{}
	for _, i := range idx {
		hit[i] = true
	}
	var b strings.Builder
	for i, r := range label {
		if hit[i] {
			b.WriteString(matchStyle.Render(string(r)))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (m Model) rowLine(r Row, w int) string {
	if r.Header {
		return frame.Truncate(headerStyle.Render(fmt.Sprintf("▸ %s (%d)", r.Group, r.Count)), w)
	}
	id := r.Item.ID()
	selected := r.Index == m.sel
	mark := " "
	if selected {
		mark = ">"
	}
	pin, star := " ", " "
	if m.Pins[id] {
		pin = pinStyle.Render("^")
	}
	if m.Stars[id] {
		star = starStyle.Render("*")
	}
	indent := ""
	if len(m.res.Groups) > 0 {
		indent = " "
	}
	label := r.Item.Label()
	if !selected {
		label = highlight(label, r.Matches)
	}
	line := indent + mark + pin + star + " " + label
	if selected {
		return selStyle.Render(frame.Pad(line, w))
	}
	return frame.Truncate(line, w)
}

// BodyLines renders every body line (headers, rows, selected detail).
func (m Model) BodyLines() []string {
	w := m.w()
	var out []string
	for _, r := range m.res.Rows {
		out = append(out, m.rowLine(r, w))
		if !r.Header && r.Index == m.sel && m.Detail != nil {
			out = append(out, dimStyle.Render(frame.Truncate("     "+m.Detail(r.Item), w)))
		}
	}
	return out
}

// StatusText is the plain one-line status: query+counts, filters, sort, group, scope.
func (m Model) StatusText() string {
	var parts []string
	q := m.query
	if m.searching {
		q += "▏"
	}
	if q != "" || m.searching {
		parts = append(parts, "/"+q)
	}
	parts = append(parts, fmt.Sprintf("%d/%d", m.res.Matched, m.res.Total))
	if len(m.filters) > 0 {
		fs := m.Filters()
		names := make([]string, 0, len(fs))
		for k := range fs {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			parts = append(parts, k+"="+strings.Join(fs[k], ","))
		}
	}
	if m.sort.Field != "" {
		dir := "asc"
		if m.sort.Desc {
			dir = "desc"
		}
		parts = append(parts, "sort:"+m.sort.Field+" "+dir)
	}
	g := m.GroupConfig()
	if len(g.Fields) > 0 {
		parts = append(parts, "group:"+strings.Join(g.Fields, ">"))
	}
	if len(g.Scope) > 0 {
		parts = append(parts, "scope:"+strings.Join(g.Scope, ","))
	}
	if m.HasActive() {
		parts = append(parts, "["+m.Keys.Clear+" clear]")
	} else if !m.searching {
		parts = append(parts, "["+m.Keys.Search+" search · "+m.Keys.CycleGroup+"/"+m.Keys.CustomizeGroup+" group]")
	}
	return strings.Join(parts, "  ")
}

// FooterText is the position/overflow indicator.
func (m Model) FooterText() string {
	if len(m.res.Items) == 0 {
		if m.res.Total == 0 {
			return "empty"
		}
		return fmt.Sprintf("no matches (0 of %d)", m.res.Total)
	}
	total, _, _ := m.layout()
	s := fmt.Sprintf("%d of %d", m.sel+1, len(m.res.Items))
	if m.offset > 0 {
		s += " · more above"
	}
	if m.offset+m.bodyHeight() < total {
		s += " · more below"
	}
	return s
}

// View renders the list in its cell size.
func (m Model) View() string {
	w := m.w()
	h := m.height
	if h <= 0 {
		h = 10
	}
	bh := m.bodyHeight()
	body := m.BodyLines()
	end := min(len(body), m.offset+bh)
	var vis []string
	if m.offset < end {
		vis = body[m.offset:end]
	}
	for len(vis) < bh {
		vis = append(vis, "")
	}
	status := m.StatusText()
	st := dimStyle
	if m.HasActive() {
		st = activeStyle
	}
	var lines []string
	if h >= 3 {
		lines = append(lines, st.Render(frame.Truncate(status, w)))
	}
	lines = append(lines, vis...)
	if h >= 2 {
		lines = append(lines, dimStyle.Render(frame.Truncate(m.FooterText(), w)))
	}
	return strings.Join(frame.FitLines(strings.Join(lines, "\n"), w, h), "\n")
}
