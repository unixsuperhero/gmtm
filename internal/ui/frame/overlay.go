package frame

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Overlay centres modal over base within a w×h area and returns w×h lines.
func Overlay(base, modal string, w, h int) string {
	mw, mh := blockSize(modal)
	if mw > w {
		mw = w
	}
	if mh > h {
		mh = h
	}
	return OverlayAt(base, modal, (w-mw)/2, (h-mh)/2, w, h)
}

func blockSize(s string) (w, h int) {
	lines := strings.Split(s, "\n")
	for _, l := range lines {
		if n := Width(l); n > w {
			w = n
		}
	}
	return w, len(lines)
}

// OverlayAt places modal with its top-left cell at (x, y) over base, cropped
// to the w×h area. Cells of base not covered by modal are preserved.
func OverlayAt(base, modal string, x, y, w, h int) string {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	baseLines := FitLines(base, w, h)
	mw, _ := blockSize(modal)
	for i, ml := range strings.Split(modal, "\n") {
		row := y + i
		if row >= h {
			break
		}
		if x >= w {
			break
		}
		seg := Pad(ml, min(mw, w-x))
		segW := Width(seg)
		bl := baseLines[row]
		left := ansi.Cut(bl, 0, x)
		right := ansi.Cut(bl, x+segW, w)
		line := left + "\x1b[0m" + seg + "\x1b[0m" + right
		baseLines[row] = Pad(line, w)
	}
	return strings.Join(baseLines, "\n")
}
