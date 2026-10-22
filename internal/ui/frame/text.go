package frame

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Width is the display width of s in terminal cells (ANSI aware).
func Width(s string) int { return ansi.StringWidth(s) }

// Truncate cuts s to at most w cells, ending with "…" when cut.
func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// Pad truncates s to w cells and right-pads it with spaces to exactly w cells.
func Pad(s string, w int) string {
	s = Truncate(s, w)
	if n := w - ansi.StringWidth(s); n > 0 {
		s += strings.Repeat(" ", n)
	}
	return s
}

// FitLines returns exactly h lines of exactly w cells from s.
func FitLines(s string, w, h int) []string {
	if h <= 0 {
		return nil
	}
	src := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, h)
	for i := 0; i < h; i++ {
		line := ""
		if i < len(src) {
			line = src[i]
		}
		out[i] = Pad(line, w)
	}
	return out
}
