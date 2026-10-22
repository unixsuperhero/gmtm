package frame

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func plainLines(s string) []string { return strings.Split(ansi.Strip(s), "\n") }

func TestRenderCropsAndKeepsIndicators(t *testing.T) {
	cases := []struct {
		name    string
		f       Frame
		content string
		want    []string // substrings expected in top line
		bottom  []string
	}{
		{"basic", Frame{Width: 60, Height: 6, Mode: "normal", Project: "website", Server: "clean-mtm", Conn: "connected"},
			"hello", []string{"cmtm", "NORMAL", "website"}, []string{"clean-mtm", "connected"}},
		{"no project explicit", Frame{Width: 40, Height: 4, Mode: "insert", Project: "no-project", Server: "s", Conn: "disconnected"},
			"", []string{"cmtm", "INSERT", "no-project"}, []string{"disconnected"}},
		{"blank project is unknown", Frame{Width: 40, Height: 4, Mode: "live"},
			"", []string{"cmtm", "LIVE", "unknown"}, []string{"unknown"}},
		{"overlong content cropped", Frame{Width: 30, Height: 5, Mode: "help", Project: "p", Server: "clean-mtm", Conn: "connected"},
			strings.Repeat("界x", 50) + "\nline2\nline3\nline4\nline5\nline6", []string{"cmtm", "HELP"}, []string{"connected"}},
		{"long project truncated", Frame{Width: 30, Height: 3, Mode: "normal", Project: "a-very-long-project-name-indeed", Conn: "connected"},
			"", []string{"cmtm", "NORMAL", "a-ve"}, []string{"connected"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := c.f.Render(c.content)
			lines := plainLines(out)
			if len(lines) != c.f.Height {
				t.Fatalf("height %d want %d", len(lines), c.f.Height)
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w != c.f.Width {
					t.Fatalf("line %d width %d want %d: %q", i, w, c.f.Width, l)
				}
			}
			for _, s := range c.want {
				if !strings.Contains(lines[0], s) {
					t.Errorf("top %q missing %q", lines[0], s)
				}
			}
			for _, s := range c.bottom {
				if !strings.Contains(lines[len(lines)-1], s) {
					t.Errorf("bottom %q missing %q", lines[len(lines)-1], s)
				}
			}
			for _, l := range lines[1 : len(lines)-1] {
				if !strings.HasPrefix(l, "│") || !strings.HasSuffix(l, "│") {
					t.Errorf("content line breaks border: %q", l)
				}
			}
		})
	}
}

func TestTooSmall(t *testing.T) {
	cases := []struct {
		w, h  int
		small bool
	}{{80, 24, false}, {10, 24, true}, {80, 2, true}, {30, 3, false}, {0, 0, true}}
	for _, c := range cases {
		f := Frame{Width: c.w, Height: c.h, Mode: "normal", Project: "proj"}
		if got := f.TooSmall(); got != c.small {
			t.Errorf("%dx%d TooSmall=%v want %v", c.w, c.h, got, c.small)
		}
		out := ansi.Strip(f.Render("content"))
		if c.small {
			if c.w > 0 && c.h > 0 && !strings.Contains(strings.ReplaceAll(out, "\n", ""), "cmtm: t") {
				t.Errorf("%dx%d expected too-small text, got %q", c.w, c.h, out)
			}
			if w, h := f.Inner(); w != 0 || h != 0 {
				t.Errorf("inner should be 0 when too small")
			}
		} else if w, h := f.Inner(); w != c.w-2 || h != c.h-2 {
			t.Errorf("inner %dx%d", w, h)
		}
	}
}

func TestOverlayStaysInside(t *testing.T) {
	base := strings.Repeat(strings.Repeat(".", 20)+"\n", 6)
	out := Overlay(base, "AB\nCD", 20, 6)
	lines := plainLines(out)
	if len(lines) != 6 {
		t.Fatalf("lines %d", len(lines))
	}
	if !strings.Contains(lines[2], "AB") || !strings.Contains(lines[3], "CD") {
		t.Fatalf("not centred: %q", lines)
	}
	for _, l := range lines {
		if ansi.StringWidth(l) != 20 {
			t.Fatalf("width broke: %q", l)
		}
	}
	// oversized modal is cropped
	big := strings.Repeat(strings.Repeat("#", 40)+"\n", 10)
	for _, l := range plainLines(Overlay(base, big, 20, 6)) {
		if ansi.StringWidth(l) != 20 {
			t.Fatalf("oversize broke width: %q", l)
		}
	}
}

func TestPadWide(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want int
	}{{"界界界", 4, 4}, {"éé", 5, 5}, {"abc", 2, 2}}
	for _, c := range cases {
		if got := Width(Pad(c.in, c.w)); got != c.want {
			t.Errorf("Pad(%q,%d) width %d", c.in, c.w, got)
		}
	}
}
