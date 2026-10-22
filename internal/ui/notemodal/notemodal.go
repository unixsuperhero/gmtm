// Package notemodal is the anytime New note modal: a focused multiline input
// with a visible, changeable destination project. Enter inserts a newline;
// Ctrl+Enter saves where the terminal can report it, Ctrl+S always saves.
package notemodal

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/unixsuperhero/gmtm/internal/domain"
	"github.com/unixsuperhero/gmtm/internal/ui/forms"
)

// SaveMsg asks the orchestrator to persist the note. NoProject is the
// explicit no-project choice (Project is then "").
type SaveMsg struct {
	Text      string
	Project   string
	NoProject bool
}

// CancelMsg is emitted by Escape in note normal mode.
type CancelMsg struct{ Dirty bool }

// Model is the note modal.
type Model struct {
	ta        textarea.Model
	insert    bool
	project   string
	noProject bool
	projects  []string

	choosing bool
	query    textinput.Model
	hl       int

	ctrlEnter bool
	saving    bool
	err       string
	status    string
	width     int
}

// New opens a note. prefill is the active project at invocation:
// a name, domain.SessionNoProject (explicit none) or domain.ProjectUnknown/"".
func New(prefill string, projects []string) *Model {
	m := &Model{projects: projects, insert: true, width: 72}
	switch prefill {
	case domain.SessionNoProject:
		m.noProject = true
	case domain.ProjectUnknown:
	default:
		m.project = prefill
	}
	m.ta = textarea.New()
	m.ta.ShowLineNumbers = false
	m.ta.Prompt = "│ "
	m.ta.CharLimit = 0
	m.ta.SetWidth(m.width)
	m.ta.SetHeight(8)
	m.ta.Cursor.SetMode(cursor.CursorStatic)
	m.ta.Focus()
	m.query = textinput.New()
	m.query.Prompt = "project: "
	m.query.Cursor.SetMode(cursor.CursorStatic)
	return m
}

// SetSize sizes the text area.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.ta.SetWidth(w)
	if h > 6 {
		m.ta.SetHeight(h - 6)
	}
}

// SetCtrlEnterSupported records whether the terminal distinguishes Ctrl+Enter.
// It is also set automatically once a distinct Ctrl+Enter sequence arrives.
func (m *Model) SetCtrlEnterSupported(ok bool) { m.ctrlEnter = ok }

// CtrlEnterSupported reports the current detection result.
func (m *Model) CtrlEnterSupported() bool { return m.ctrlEnter }

// Focus re-focuses the existing draft (repeat invocation does not nest).
func (m *Model) Focus() {
	m.choosing = false
	m.insert = true
	m.ta.Focus()
}

// SetError reports a failed save; the full draft and association are kept.
func (m *Model) SetError(msg string) {
	m.saving = false
	m.err = msg
	m.status = "save failed — draft kept"
}

// SetSaved reports a committed save.
func (m *Model) SetSaved() {
	m.saving = false
	m.err = ""
	m.status = "saved"
}

func (m *Model) Text() string   { return m.ta.Value() }
func (m *Model) Insert() bool   { return m.insert }
func (m *Model) Choosing() bool { return m.choosing }
func (m *Model) Err() string    { return m.err }
func (m *Model) Dirty() bool    { return strings.TrimSpace(m.ta.Value()) != "" }
func (m *Model) Unknown() bool  { return m.project == "" && !m.noProject }
func (m *Model) Destination() string {
	switch {
	case m.noProject:
		return domain.SessionNoProject
	case m.project == "":
		return domain.ProjectUnknown
	}
	return m.project
}

func emit(msg tea.Msg) tea.Cmd { return func() tea.Msg { return msg } }

// IsCtrlEnter recognises Ctrl+Enter sequences that Bubble Tea v1 does not
// decode (xterm modifyOtherKeys "CSI 27;5;13~" and CSI-u "CSI 13;5u"); they
// arrive as unknown-CSI messages. Plain Enter/ctrl+m are never Ctrl+Enter.
func IsCtrlEnter(msg tea.Msg) bool {
	if k, ok := msg.(tea.KeyMsg); ok {
		return k.String() == "ctrl+enter"
	}
	st, ok := msg.(fmt.Stringer)
	if !ok {
		return false
	}
	s := st.String()
	if !strings.HasPrefix(s, "?CSI[") || !strings.HasSuffix(s, "]?") {
		return false
	}
	var seq []byte
	for _, f := range strings.Fields(s[5 : len(s)-2]) {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 || n > 255 {
			return false
		}
		seq = append(seq, byte(n))
	}
	switch string(seq) {
	case "27;5;13~", "13;5u":
		return true
	}
	return false
}

// Update handles input. It never forwards keys anywhere else.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	if IsCtrlEnter(msg) {
		m.ctrlEnter = true
		return m.save()
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if m.choosing {
		return m.chooseKey(k)
	}
	switch k.String() {
	case "ctrl+s":
		return m.save()
	case "ctrl+t":
		m.openChooser()
		return nil
	}
	if m.insert {
		if k.String() == "esc" {
			m.insert = false
			m.ta.Blur()
			return nil
		}
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(k)
		return cmd
	}
	switch k.String() {
	case "esc":
		return emit(CancelMsg{Dirty: m.Dirty()})
	case "i", "a", "enter":
		m.Focus()
	case "p":
		m.openChooser()
	case "up", "down", "left", "right":
		m.ta.Focus()
		m.ta, _ = m.ta.Update(k)
		m.ta.Blur()
	}
	return nil
}

func (m *Model) save() tea.Cmd {
	if m.saving {
		return nil
	}
	if m.Unknown() {
		m.err = "destination project is unknown: choose a project or no-project before saving"
		m.openChooser()
		return nil
	}
	if strings.TrimSpace(m.ta.Value()) == "" {
		m.err = "note is empty"
		return nil
	}
	m.saving, m.err, m.status = true, "", "saving…"
	return emit(SaveMsg{Text: m.ta.Value(), Project: m.project, NoProject: m.noProject})
}

func (m *Model) options() []forms.Option {
	opts := []forms.Option{{Value: domain.SessionNoProject, Label: "no-project", Hint: "explicitly no project"}}
	for _, p := range m.projects {
		opts = append(opts, forms.Option{Value: p})
	}
	return forms.Rank(m.query.Value(), opts)
}

func (m *Model) openChooser() {
	m.choosing, m.hl = true, 0
	m.query.SetValue("")
	m.query.Focus()
}

func (m *Model) chooseKey(k tea.KeyMsg) tea.Cmd {
	opts := m.options()
	switch k.String() {
	case "esc":
		m.choosing = false
		m.query.Blur()
	case "ctrl+n", "down":
		if len(opts) > 0 {
			m.hl = (m.hl + 1) % len(opts)
		}
	case "ctrl+p", "up":
		if len(opts) > 0 {
			m.hl = (m.hl - 1 + len(opts)) % len(opts)
		}
	case "enter":
		if m.hl < len(opts) {
			m.choose(opts[m.hl].Value)
		}
	default:
		var cmd tea.Cmd
		m.query, cmd = m.query.Update(k)
		m.hl = 0
		return cmd
	}
	return nil
}

func (m *Model) choose(v string) {
	if v == domain.SessionNoProject {
		m.project, m.noProject = "", true
	} else {
		m.project, m.noProject = v, false
	}
	m.choosing = false
	m.query.Blur()
	if strings.HasPrefix(m.err, "destination project") {
		m.err = ""
	}
}

// View renders the modal body (the orchestrator draws the frame).
func (m *Model) View() string {
	var b strings.Builder
	mode := "NORMAL"
	if m.insert {
		mode = "INSERT"
	}
	b.WriteString(fmt.Sprintf("New note  [%s]\n", mode))
	switch {
	case m.noProject:
		b.WriteString("Destination: no-project (explicit, no project association)\n")
	case m.project == "":
		b.WriteString("Destination: unknown — choose a project or no-project before saving (ctrl+t)\n")
	default:
		b.WriteString("Destination: " + m.project + "\n")
	}
	if m.choosing {
		b.WriteString(m.query.View() + "\n")
		for i, o := range m.options() {
			mark := "   "
			if i == m.hl {
				mark = " > "
			}
			b.WriteString(mark + o.Display() + "\n")
		}
		b.WriteString("enter choose · ↑/↓ ctrl+n/p · esc back\n")
		return b.String()
	}
	b.WriteString(m.ta.View() + "\n")
	if m.err != "" {
		b.WriteString("! " + m.err + "\n")
	}
	if m.status != "" {
		b.WriteString("· " + m.status + "\n")
	}
	b.WriteString(m.help())
	return b.String()
}

func (m *Model) help() string {
	save := "ctrl+enter save · ctrl+s save"
	if !m.ctrlEnter {
		save = "ctrl+s save (ctrl+enter is not distinguishable from enter in this terminal; enter inserts a newline)"
	}
	if m.insert {
		return "enter newline · " + save + " · ctrl+t project · esc normal mode"
	}
	return "i edit · p/ctrl+t project · " + save + " · esc close"
}
