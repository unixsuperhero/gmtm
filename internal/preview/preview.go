// Package preview is the read-only terminal preview: capture → sanitise →
// render through x/vt → crop. It never sends input, focus, resize or leases;
// it only issues the read verbs get-window and capture-pane.
package preview

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/tuios"
)

// State of a preview result.
type State string

const (
	StateLoading     State = "loading"
	StateCurrent     State = "current"
	StateCached      State = "cached"
	StateEmpty       State = "empty"
	StateClosed      State = "closed"
	StateUnavailable State = "unavailable"
)

// Bounds.
const (
	DefaultLines   = 200
	MaxLines       = 2000
	MaxBytes       = 1 << 20
	MaxCols        = 1000
	CaptureTimeout = 3 * time.Second
)

// Opts for one capture.
type Opts struct {
	Lines  int  // recent lines to keep (default 200, max 2000)
	Styled bool // keep SGR styling in Rendered
}

// ResultMsg is the outcome of one capture; deliver through Tracker.Accept.
type ResultMsg struct {
	Target    domain.Target
	Gen       uint64
	Text      string   // plain text, trailing spaces trimmed
	Rendered  []string // one display line per row (SGR only when Styled)
	TakenAt   time.Time
	Truncated bool
	Err       error
	State     State
	AltScreen bool
	Cols      int // pane width used for rendering
	Rows      int // pane height reported by the daemon
}

// Service performs read-only captures through the shared client.
type Service struct {
	client tuios.Client
}

func New(client tuios.Client) *Service { return &Service{client: client} }

// now is swapped in tests.
var now = time.Now

// Capture returns a tea.Cmd that captures target once, bounded by CaptureTimeout.
func (s *Service) Capture(ctx context.Context, target domain.Target, gen uint64, opts Opts) tea.Cmd {
	return func() tea.Msg { return s.CaptureSync(ctx, target, gen, opts) }
}

// CaptureSync is the blocking form of Capture.
func (s *Service) CaptureSync(ctx context.Context, target domain.Target, gen uint64, opts Opts) ResultMsg {
	ctx, cancel := context.WithTimeout(ctx, CaptureTimeout)
	defer cancel()
	res := ResultMsg{Target: target, Gen: gen}
	if opts.Lines <= 0 {
		opts.Lines = DefaultLines
	}
	if opts.Lines > MaxLines {
		opts.Lines = MaxLines
	}

	var win map[string]any
	if err := s.client.Call(ctx, "get-window", map[string]any{"session": target.Session, "window": target.WindowID}, &win); err != nil {
		return failed(res, err)
	}
	res.Cols, res.Rows = intField(win, "width"), intField(win, "height")
	res.AltScreen = boolField(win, "alt_screen") || boolField(win, "alternate_screen")

	params := map[string]any{"session": target.Session, "window": target.WindowID, "styled": true}
	if res.AltScreen {
		params["source"] = "visible"
	} else {
		params["source"] = "recent"
		params["lines"] = opts.Lines
	}
	var cp struct {
		Content     string `json:"content"`
		HistoryRows int    `json:"history_rows"`
		AltScreen   bool   `json:"alt_screen"`
		Truncated   bool   `json:"truncated"`
	}
	if err := s.client.Call(ctx, "capture-pane", params, &cp); err != nil {
		return failed(res, err)
	}
	res.TakenAt = now()
	res.AltScreen = res.AltScreen || cp.AltScreen

	content := cp.Content
	if len(content) > MaxBytes {
		content = content[len(content)-MaxBytes:]
		if i := strings.IndexByte(content, '\n'); i >= 0 {
			content = content[i+1:]
		}
		res.Truncated = true
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) > opts.Lines {
		lines = lines[len(lines)-opts.Lines:]
		res.Truncated = true
	}
	if !res.AltScreen && len(lines) >= opts.Lines && cp.HistoryRows+res.Rows > len(lines) {
		res.Truncated = true
	}
	res.Truncated = res.Truncated || cp.Truncated

	styled, plain := Render(lines, res.Cols)
	if !opts.Styled {
		styled = plain
	}
	res.Rendered = styled
	res.Text = strings.TrimRight(strings.Join(plain, "\n"), "\n")
	if strings.TrimSpace(res.Text) == "" {
		res.State = StateEmpty
	} else {
		res.State = StateCurrent
	}
	return res
}

func failed(res ResultMsg, err error) ResultMsg {
	res.Err = err
	res.TakenAt = now()
	res.State = StateUnavailable
	var te *tuios.Error
	if errors.As(err, &te) {
		switch te.Code {
		case "window_not_found", "session_not_found", "no_such_window", "window_closed":
			res.State = StateClosed
		}
	}
	return res
}

// Render sanitises raw captured lines and draws them through a vt emulator
// of the given width (widened to fit the longest line), returning styled and
// plain display lines, one per input line.
func Render(raw []string, cols int) (styled, plain []string) {
	clean := make([]string, len(raw))
	for i, l := range raw {
		c := string(Sanitize([]byte(l)))
		clean[i] = strings.TrimRight(c, "\r")
		if w := ansi.StringWidth(clean[i]); w > cols {
			cols = w
		}
	}
	if cols <= 0 {
		cols = 80
	}
	if cols > MaxCols {
		cols = MaxCols
	}
	rows := len(clean)
	if rows == 0 {
		return nil, nil
	}
	// Sanitised input holds only text and SGR, which never makes the emulator
	// write a reply, so its reply pipe needs no reader.
	emu := vt.NewEmulator(cols, rows)
	for i, l := range clean {
		_, _ = emu.WriteString(l)
		if i < rows-1 {
			_, _ = emu.WriteString("\r\n")
		}
	}
	styled = splitScreen(emu.Render(), rows)
	plain = make([]string, rows)
	for i, l := range styled {
		plain[i] = strings.TrimRight(ansi.Strip(l), " ")
	}
	return styled, plain
}

func splitScreen(s string, rows int) []string {
	out := strings.Split(s, "\n")
	for i := range out {
		out[i] = strings.TrimSuffix(out[i], "\r")
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out[:rows]
}

// TooSmall is the rule for showing an explicit too-small state instead of a preview.
func TooSmall(w, h int) bool { return w < MinWidth || h < MinHeight }

const (
	MinWidth  = 10
	MinHeight = 1
)

// Crop fits lines into w×h without touching the target: keeps the last h
// lines (most recent output) and truncates each to w cells (styling preserved).
func Crop(lines []string, w, h int) []string {
	if w <= 0 || h <= 0 {
		return nil
	}
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if ansi.StringWidth(l) > w {
			l = ansi.Truncate(l, w, "")
		}
		out[i] = l
	}
	return out
}

func intField(m map[string]any, k string) int {
	switch v := m[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func boolField(m map[string]any, k string) bool { b, _ := m[k].(bool); return b }
