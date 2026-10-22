package frame

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Identity is the UI name shown in the top border.
const Identity = "cmtm"

// Unknown is shown when the project cannot be determined.
const Unknown = "unknown"

// Frame describes the persistent application frame.
type Frame struct {
	Width, Height int
	Mode          string // e.g. "normal"; "" renders as "unknown"
	Project       string // "" renders as "unknown"; pass "no-project" explicitly
	Server        string
	Conn          string // connection state text, e.g. "connected"
}

var (
	borderStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	identityStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	modeStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	projectStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	serverStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
)

func connStyle(c string) lipgloss.Style {
	switch c {
	case "connected":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	case "connecting", "resynchronizing":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	case "disconnected":
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return Unknown
	}
	return s
}

// ModeText is the upper-cased mode label.
func (f Frame) ModeText() string { return strings.ToUpper(orUnknown(f.Mode)) }

// ProjectText is the project label, never blank.
func (f Frame) ProjectText() string { return orUnknown(f.Project) }

// minProjectCells is how much of the project name must survive truncation.
const minProjectCells = 4

// MinSize is the smallest terminal that can show border plus indicators.
func (f Frame) MinSize() (w, h int) {
	// "╭─ cmtm │ MODE │ proj… ─╮"
	p := Width(f.ProjectText())
	if p > minProjectCells {
		p = minProjectCells
	}
	return 2 + 1 + Width(f.topLabel(p)) + 1, 3
}

// TooSmall reports whether the frame cannot be drawn with its indicators.
func (f Frame) TooSmall() bool {
	mw, mh := f.MinSize()
	return f.Width < mw || f.Height < mh
}

// Inner is the content area inside the border (0,0 when too small).
func (f Frame) Inner() (w, h int) {
	if f.TooSmall() {
		return 0, 0
	}
	return f.Width - 2, f.Height - 2
}

// topLabel builds the plain top label with the project cut to pw cells (pw<0: full).
func (f Frame) topLabel(pw int) string {
	p := f.ProjectText()
	if pw >= 0 {
		p = Truncate(p, pw)
	}
	return " " + Identity + " │ " + f.ModeText() + " │ " + p + " "
}

func (f Frame) styledTop(pw int) string {
	p := f.ProjectText()
	if pw >= 0 {
		p = Truncate(p, pw)
	}
	sep := borderStyle.Render("│")
	return " " + identityStyle.Render(Identity) + " " + sep + " " + modeStyle.Render(f.ModeText()) + " " + sep + " " + projectStyle.Render(p) + " "
}

func (f Frame) top() string {
	inner := f.Width - 2
	// keep one "─" lead
	avail := inner - 1
	fixed := Width(f.topLabel(0))
	pw := avail - fixed
	if pw > Width(f.ProjectText()) {
		pw = Width(f.ProjectText())
	}
	label := f.styledTop(pw)
	fill := avail - Width(label)
	if fill < 0 {
		fill = 0
	}
	return borderStyle.Render("╭─") + label + borderStyle.Render(strings.Repeat("─", fill)+"╮")
}

func (f Frame) bottom() string {
	inner := f.Width - 2
	server := orUnknown(f.Server)
	conn := orUnknown(f.Conn)
	avail := inner - 2 // leading and trailing "─"
	label := ""
	// prefer " server: conn ", then " conn ", then nothing
	full := " " + server + ": " + conn + " "
	switch {
	case Width(full) <= avail:
		label = " " + serverStyle.Render(server) + ": " + connStyle(conn).Render(conn) + " "
	case Width(" "+conn+" ") <= avail:
		cw := avail - Width(" "+conn+" ")
		if cw >= 4 {
			label = " " + serverStyle.Render(Truncate(server, cw-2)) + ": " + connStyle(conn).Render(conn) + " "
		} else {
			label = " " + connStyle(conn).Render(conn) + " "
		}
	}
	fill := inner - 1 - Width(label)
	if fill < 0 {
		fill = 0
	}
	return borderStyle.Render("╰"+strings.Repeat("─", fill)) + label + borderStyle.Render("─╯")
}

// TooSmallText is the explicit message shown when the terminal is too small.
func (f Frame) TooSmallText() string {
	mw, mh := f.MinSize()
	msg := fmt.Sprintf("cmtm: terminal too small (%dx%d, need %dx%d) mode %s project %s",
		f.Width, f.Height, mw, mh, f.ModeText(), f.ProjectText())
	if f.Width <= 0 || f.Height <= 0 {
		return ""
	}
	// wrap the message hard into the available cells
	var lines []string
	r := []rune(msg)
	for len(r) > 0 && len(lines) < f.Height {
		n := f.Width
		if n > len(r) {
			n = len(r)
		}
		lines = append(lines, string(r[:n]))
		r = r[n:]
	}
	return strings.Join(FitLines(strings.Join(lines, "\n"), f.Width, f.Height), "\n")
}

// Render wraps content in the frame, cropping it to the inner area.
func (f Frame) Render(content string) string {
	if f.TooSmall() {
		return f.TooSmallText()
	}
	w, h := f.Inner()
	side := borderStyle.Render("│")
	var b strings.Builder
	b.WriteString(f.top())
	for _, l := range FitLines(content, w, h) {
		b.WriteString("\n" + side + l + side)
	}
	b.WriteString("\n" + f.bottom())
	return b.String()
}
