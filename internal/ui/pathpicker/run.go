package pathpicker

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/ui/frame"
)

type runner struct {
	m        *Model
	w, h     int
	result   []string
	canceled bool
}

func (r *runner) Init() tea.Cmd { return r.m.Init() }

func (r *runner) fr() frame.Frame {
	return frame.Frame{Width: r.w, Height: r.h, Mode: r.m.InputMode(), Project: frame.Unknown, Server: "local", Conn: "filesystem"}
}

func (r *runner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		r.w, r.h = v.Width, v.Height
		r.m.SetSize(r.fr().Inner())
		return r, nil
	case ChosenMsg:
		r.result = v.Paths
		return r, tea.Quit
	case CancelMsg:
		r.canceled = true
		return r, tea.Quit
	}
	return r, r.m.Update(msg)
}

func (r *runner) View() string { return r.fr().Render(r.m.View()) }

// Run shows the picker full-screen inside the cmtm frame and returns the
// chosen absolute paths, or ErrCanceled.
func Run(o Options) ([]string, error) {
	r := &runner{m: New(o)}
	if _, err := tea.NewProgram(r, tea.WithAltScreen()).Run(); err != nil {
		return nil, err
	}
	if r.canceled || len(r.result) == 0 {
		return nil, ErrCanceled
	}
	return r.result, nil
}
