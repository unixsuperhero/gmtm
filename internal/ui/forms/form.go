// Package forms is mtm's single form interaction system: field kinds,
// normal/insert modality, navigation, suggestions, staging of user-extensible
// values, linked parent/child fields, fuzzy path picker requests and async
// completions bound to field IDs and edit generations.
package forms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// SubmitMsg is emitted after a successful submit. Staged lists, per field,
// new user-extensible values the caller must create in the same transaction.
type SubmitMsg struct {
	FormID string
	Values map[string]any
	Staged map[string][]string
}

// CancelMsg is emitted by Escape in normal mode; the orchestrator decides
// whether to confirm discarding a dirty draft.
type CancelMsg struct {
	FormID string
	Dirty  bool
}

// CompletionMsg carries async options for one field. Replies whose Gen is not
// the field's current generation are ignored. FormID may be empty.
type CompletionMsg struct {
	FormID  string
	FieldID string
	Gen     int
	Options []Option
	Err     error
}

// PickResultMsg carries fuzzy path picker output for a PickRequestMsg,
// bound like CompletionMsg: a stale Gen is ignored.
type PickResultMsg struct {
	FormID  string
	FieldID string
	Gen     int
	Paths   []string
	Err     error
}

// FieldDraft is the persisted state of one field.
type FieldDraft struct {
	Text        string   `json:"text,omitempty"`
	Items       []string `json:"items,omitempty"`
	Bool        *bool    `json:"bool,omitempty"`
	Answered    bool     `json:"answered,omitempty"`
	FromDefault bool     `json:"fromDefault,omitempty"`
}

// Draft is a JSON-serialisable snapshot of a form for persistence.
type Draft struct {
	FormID string                `json:"formId"`
	Focus  string                `json:"focus,omitempty"`
	Fields map[string]FieldDraft `json:"fields"`
}

type fieldState struct {
	f           Field
	in          textinput.Model
	ta          textarea.Model
	items       []string
	b, bset     bool
	answered    bool
	fromDefault bool
	gen         int
	async       []Option
	err, note   string
	cyc         []Option
	cycIdx      int
	hl          int
}

func (s *fieldState) text() string {
	if s.f.Kind == Multiline {
		return s.ta.Value()
	}
	return s.in.Value()
}

func (s *fieldState) blur() {
	s.in.Blur()
	if s.f.Kind == Multiline {
		s.ta.Blur()
	}
}

func (s *fieldState) setText(t string) {
	if s.f.Kind == Multiline {
		s.ta.SetValue(t)
		return
	}
	s.in.SetValue(t)
	s.in.CursorEnd()
}

// Form is the form model. Update returns commands that emit SubmitMsg,
// CancelMsg, CompletionMsg and PickRequestMsg.
type Form struct {
	ID, Title       string
	CompleteTimeout time.Duration
	Width           int

	fields  []*fieldState
	byID    map[string]*fieldState
	focus   int
	insert  bool
	finding bool
	finder  textinput.Model
	findHL  int
	initial string
	status  string
}

const reservedKeys = "jkhliagGx/ 0123456789"

// New builds a form. It rejects duplicate IDs, unknown parents and shortcuts
// that collide with navigation keys or each other.
func New(id, title string, fields ...Field) (*Form, error) {
	f := &Form{ID: id, Title: title, byID: map[string]*fieldState{},
		CompleteTimeout: 5 * time.Second}
	short := map[rune]string{}
	for _, fd := range fields {
		if fd.ID == "" || f.byID[fd.ID] != nil {
			return nil, fmt.Errorf("forms: empty or duplicate field id %q", fd.ID)
		}
		if fd.Shortcut != 0 {
			if strings.ContainsRune(reservedKeys, fd.Shortcut) {
				return nil, fmt.Errorf("forms: shortcut %q of %s collides with navigation", fd.Shortcut, fd.ID)
			}
			if o, dup := short[fd.Shortcut]; dup {
				return nil, fmt.Errorf("forms: shortcut %q used by %s and %s", fd.Shortcut, o, fd.ID)
			}
			short[fd.Shortcut] = fd.ID
		}
		if fd.Kind == Linked && fd.ParentID == "" {
			return nil, fmt.Errorf("forms: linked field %s needs ParentID", fd.ID)
		}
		s := &fieldState{f: fd, in: textinput.New(), cycIdx: -1}
		s.in.Prompt = ""
		s.in.Cursor.SetMode(cursor.CursorStatic)
		if fd.Kind == Multiline {
			s.ta = textarea.New()
			s.ta.ShowLineNumbers = false
			s.ta.Prompt = "  "
			s.ta.SetWidth(72)
			s.ta.SetHeight(5)
			s.ta.Cursor.SetMode(cursor.CursorStatic)
		}
		f.applyDefault(s)
		f.fields = append(f.fields, s)
		f.byID[fd.ID] = s
	}
	for _, s := range f.fields {
		if p := s.f.ParentID; p != "" && f.byID[p] == nil {
			return nil, fmt.Errorf("forms: field %s has unknown parent %s", s.f.ID, p)
		}
	}
	f.finder = textinput.New()
	f.finder.Prompt = "find field: "
	f.finder.Cursor.SetMode(cursor.CursorStatic)
	f.initial = f.draftKey()
	return f, nil
}

func (f *Form) applyDefault(s *fieldState) {
	switch d := s.f.Default.(type) {
	case nil:
		return
	case bool:
		s.b, s.bset = d, true
	case float64:
		s.setText(strconv.FormatFloat(d, 'f', -1, 64))
	case int:
		s.setText(strconv.Itoa(d))
	case []string:
		s.items = append([]string(nil), d...)
	case string:
		if o, ok := ExactMatch(d, f.allOptions(s)); ok && s.f.Kind.choiceLike() {
			d = o.Display()
		}
		if s.f.multi() {
			s.items = []string{d}
		} else {
			s.setText(d)
		}
	}
	s.fromDefault = true
}

// SetOptions refreshes one field's static options without touching others.
func (f *Form) SetOptions(fieldID string, opts []Option) {
	if s := f.byID[fieldID]; s != nil {
		s.f.Options = opts
		s.cyc = nil
	}
}

// Insert reports whether the form is in insert mode.
func (f *Form) Insert() bool { return f.insert }

// Focused returns the focused field's ID.
func (f *Form) Focused() string {
	if len(f.fields) == 0 {
		return ""
	}
	return f.fields[f.focus].f.ID
}

// FocusField focuses a field by ID.
func (f *Form) FocusField(id string) {
	for i, s := range f.fields {
		if s.f.ID == id {
			f.setFocus(i)
		}
	}
}

// Gen returns a field's current edit generation (for async callers).
func (f *Form) Gen(fieldID string) int {
	if s := f.byID[fieldID]; s != nil {
		return s.gen
	}
	return -1
}

// Text returns a field's raw edit text.
func (f *Form) Text(fieldID string) string {
	if s := f.byID[fieldID]; s != nil {
		return s.text()
	}
	return ""
}

// FieldError returns a field's visible validation error.
func (f *Form) FieldError(fieldID string) string {
	if s := f.byID[fieldID]; s != nil {
		return s.err
	}
	return ""
}

// FieldNote returns a field's informational note (e.g. cleared child).
func (f *Form) FieldNote(fieldID string) string {
	if s := f.byID[fieldID]; s != nil {
		return s.note
	}
	return ""
}

// Dirty reports unsaved changes relative to the initial state.
func (f *Form) Dirty() bool { return f.draftKey() != f.initial }

// MarkClean makes the current state the new baseline.
func (f *Form) MarkClean() { f.initial = f.draftKey() }

// Draft snapshots the form for persistence.
func (f *Form) Draft() Draft {
	d := Draft{FormID: f.ID, Focus: f.Focused(), Fields: map[string]FieldDraft{}}
	for _, s := range f.fields {
		fd := FieldDraft{Text: s.text(), Items: append([]string(nil), s.items...), Answered: s.answered, FromDefault: s.fromDefault}
		if s.bset {
			b := s.b
			fd.Bool = &b
		}
		d.Fields[s.f.ID] = fd
	}
	return d
}

// Restore loads a draft; unknown field IDs are ignored. Restoring bumps every
// generation so in-flight completions cannot overwrite restored values.
func (f *Form) Restore(d Draft) {
	for id, fd := range d.Fields {
		s := f.byID[id]
		if s == nil {
			continue
		}
		s.setText(fd.Text)
		s.items = append([]string(nil), fd.Items...)
		s.bset, s.b = fd.Bool != nil, fd.Bool != nil && *fd.Bool
		s.answered, s.fromDefault = fd.Answered, fd.FromDefault
		s.gen++
		s.err, s.note, s.cyc = "", "", nil
	}
	f.FocusField(d.Focus)
}

func (f *Form) draftKey() string {
	d := f.Draft()
	d.Focus = ""
	for k, v := range d.Fields {
		v.Answered, v.FromDefault = false, false
		d.Fields[k] = v
	}
	b, _ := json.Marshal(d)
	return string(b)
}

// ---- options and values ----

func (f *Form) allOptions(s *fieldState) []Option {
	var out []Option
	seen := map[string]bool{}
	add := func(o Option) {
		if !seen[o.Value] {
			seen[o.Value] = true
			out = append(out, o)
		}
	}
	if s.f.NoValue != nil && !s.f.multi() {
		add(*s.f.NoValue)
	}
	for _, o := range s.f.Options {
		add(o)
	}
	for _, o := range s.async {
		add(o)
	}
	return out
}

// options is allOptions narrowed by an explicitly chosen parent.
func (f *Form) options(s *fieldState) []Option {
	all := f.allOptions(s)
	p := f.byID[s.f.ParentID]
	if p == nil || !p.answered {
		return all
	}
	pv := f.single(p)
	if pv == "" {
		return all
	}
	var out []Option
	for _, o := range all {
		if o.Parent == "" || o.Parent == pv {
			out = append(out, o)
		}
	}
	return out
}

// single returns the selected option value of a single choice-like field.
func (f *Form) single(s *fieldState) string {
	if o, ok := ExactMatch(s.text(), f.allOptions(s)); ok {
		return o.Value
	}
	return ""
}

func (f *Form) isNone(s *fieldState, o Option) bool {
	return s.f.NoValue != nil && o.Value == s.f.NoValue.Value
}

// value computes a field's submit value. invalid is a visible validation error.
func (f *Form) value(s *fieldState) (v any, has, none bool, staged []string, invalid string) {
	t := strings.TrimSpace(s.text())
	switch s.f.Kind {
	case Text, Multiline:
		return s.text(), s.text() != "", false, nil, ""
	case Number:
		if t == "" {
			return nil, false, false, nil, ""
		}
		n, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return nil, false, false, nil, fmt.Sprintf("%q is not a number", t)
		}
		return n, true, false, nil, ""
	case Bool:
		return s.b, s.bset, false, nil, ""
	}
	if s.f.multi() {
		items := append([]string(nil), s.items...)
		if t != "" {
			if o, ok := ExactMatch(t, f.allOptions(s)); ok {
				items = append(items, o.Value)
			} else if s.f.Kind == MultiSelect {
				return nil, false, false, nil, fmt.Sprintf("%q is not one of the allowed options", t)
			} else {
				items = append(items, t)
			}
		}
		if s.f.Kind == Extensible {
			for _, it := range items {
				if _, ok := ExactMatch(it, f.allOptions(s)); !ok {
					staged = append(staged, it)
				}
			}
		}
		return items, len(items) > 0, false, staged, ""
	}
	if s.f.Kind == Path {
		return t, t != "", false, nil, ""
	}
	if t == "" {
		return nil, false, false, nil, ""
	}
	if o, ok := ExactMatch(t, f.allOptions(s)); ok {
		if f.isNone(s, o) {
			return None{Value: o.Value}, false, true, nil, ""
		}
		return o.Value, true, false, nil, ""
	}
	if s.f.Kind == Extensible {
		return t, true, false, []string{t}, ""
	}
	return nil, false, false, nil, fmt.Sprintf("%q is not one of the allowed options", t)
}

func (f *Form) suggestions(s *fieldState) []Option {
	t := s.text()
	switch {
	case s.f.Kind == Path:
		if s.f.Path.Local() {
			return append(localPathOptions(t, s.f.Path.Mode), Rank(t, s.async)...)
		}
		return Rank(t, s.async)
	case s.f.Kind.choiceLike():
		in := map[string]bool{}
		for _, it := range s.items {
			in[it] = true
		}
		var out []Option
		for _, o := range Rank(t, f.options(s)) {
			if !in[o.Value] {
				out = append(out, o)
			}
		}
		return out
	case s.f.Complete != nil:
		return Rank(t, s.async)
	}
	return nil
}

// ---- update ----

func emit(m tea.Msg) tea.Cmd { return func() tea.Msg { return m } }

// Update handles keys and async replies.
func (f *Form) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case CompletionMsg:
		s := f.byID[m.FieldID]
		if s == nil || (m.FormID != "" && m.FormID != f.ID) || m.Gen != s.gen {
			return nil // stale or foreign
		}
		s.async, s.cyc = m.Options, nil
		if m.Err != nil {
			s.note = "completion failed: " + m.Err.Error()
		}
	case PickResultMsg:
		s := f.byID[m.FieldID]
		if s == nil || (m.FormID != "" && m.FormID != f.ID) || m.Gen != s.gen {
			return nil
		}
		switch {
		case errors.Is(m.Err, ErrPickerCanceled):
			s.note = "picker cancelled; value unchanged"
		case m.Err != nil:
			s.err = m.Err.Error() + " — type the path manually"
		case s.f.multi():
			s.items = append([]string(nil), m.Paths...)
			s.setText("")
			f.touched(s)
			s.note = fmt.Sprintf("picked %d path(s)", len(m.Paths))
		case len(m.Paths) > 0:
			s.setText(m.Paths[0])
			f.touched(s)
			s.note = "picked with fuzzy picker"
		}
	case tea.KeyMsg:
		return f.key(m)
	}
	return nil
}

func (f *Form) cur() *fieldState { return f.fields[f.focus] }

func (f *Form) key(k tea.KeyMsg) tea.Cmd {
	if len(f.fields) == 0 {
		if k.String() == "esc" {
			return emit(CancelMsg{FormID: f.ID, Dirty: false})
		}
		return nil
	}
	if f.finding {
		return f.finderKey(k)
	}
	s := f.cur()
	switch k.String() {
	case "ctrl+s":
		return f.Submit()
	case "ctrl+o":
		return f.pick(s)
	case "tab":
		f.setFocus(f.focus + 1)
		return nil
	case "shift+tab":
		f.setFocus(f.focus - 1)
		return nil
	}
	if f.insert {
		return f.insertKey(s, k)
	}
	return f.normalKey(s, k)
}

func (f *Form) normalKey(s *fieldState, k tea.KeyMsg) tea.Cmd {
	ks := k.String()
	switch ks {
	case "esc":
		return emit(CancelMsg{FormID: f.ID, Dirty: f.Dirty()})
	case "j", "down":
		f.setFocus(f.focus + 1)
	case "k", "up":
		f.setFocus(f.focus - 1)
	case "g":
		f.setFocus(0)
	case "G":
		f.setFocus(len(f.fields) - 1)
	case "/":
		f.finding, f.findHL = true, 0
		f.finder.SetValue("")
		f.finder.Focus()
	case "i", "a", "enter":
		if s.f.Kind == Bool {
			return f.toggle(s)
		}
		f.setInsert(true)
	case "h", "left":
		return f.cycle(s, -1)
	case "l", "right":
		return f.cycle(s, 1)
	case " ":
		if s.f.Kind == MultiSelect {
			return f.toggleHL(s)
		}
		return f.cycle(s, 1)
	case "x":
		s.setText("")
		s.items, s.bset = nil, false
		return f.touched(s)
	default:
		if len(ks) == 1 && ks[0] >= '0' && ks[0] <= '9' {
			n := int(ks[0] - '1')
			if ks == "0" {
				n = 9
			}
			if n < len(f.fields) {
				f.setFocus(n)
			}
			return nil
		}
		if r := []rune(ks); len(r) == 1 {
			for i, o := range f.fields {
				if o.f.Shortcut == r[0] {
					f.setFocus(i)
					f.setInsert(true)
				}
			}
		}
	}
	return nil
}

func (f *Form) insertKey(s *fieldState, k tea.KeyMsg) tea.Cmd {
	ks := k.String()
	switch {
	case ks == "esc":
		f.setInsert(false)
		return nil
	case (ks == "ctrl+n" || ks == "ctrl+p") && s.f.Kind != Multiline:
		d := 1
		if ks == "ctrl+p" {
			d = -1
		}
		f.cycleSuggest(s, d)
		return nil
	case ks == "enter" && s.f.Kind != Multiline:
		if s.f.multi() {
			return f.addPending(s)
		}
		f.setInsert(false)
		f.setFocus(f.focus + 1)
		return nil
	case ks == "backspace" && s.f.multi() && s.text() == "" && len(s.items) > 0:
		s.items = s.items[:len(s.items)-1]
		return f.touched(s)
	}
	before := s.text()
	var cmd tea.Cmd
	if s.f.Kind == Multiline {
		s.ta, cmd = s.ta.Update(k)
	} else {
		s.in, cmd = s.in.Update(k)
	}
	if s.text() != before {
		return tea.Batch(cmd, f.touched(s))
	}
	return cmd
}

// touched records a user edit: bumps the generation, re-applies linking and
// requests async completion.
func (f *Form) touched(s *fieldState) tea.Cmd {
	s.answered, s.fromDefault = true, false
	s.gen++
	s.cyc, s.cycIdx = nil, -1
	s.err = ""
	f.relink(s)
	return f.completeCmd(s)
}

func (f *Form) relink(s *fieldState) {
	if p := f.byID[s.f.ParentID]; p != nil && !s.f.multi() {
		if o, ok := ExactMatch(s.text(), f.allOptions(s)); ok && o.Parent != "" && f.single(p) != o.Parent {
			label := o.Parent
			if po, ok := ExactMatch(o.Parent, f.allOptions(p)); ok {
				label = po.Display()
			}
			p.setText(label)
			p.answered, p.fromDefault = true, false
			p.gen++
			p.note = fmt.Sprintf("set from %s %q", s.f.Label, o.Display())
		}
	}
	pv := f.single(s)
	if pv == "" && strings.TrimSpace(s.text()) != "" {
		return // parent text invalid/unmatched: don't clear children on a typo
	}
	for _, c := range f.fields {
		if c.f.ParentID != s.f.ID || c.f.multi() {
			continue
		}
		o, ok := ExactMatch(c.text(), f.allOptions(c))
		if pv == "" || !ok || o.Parent == "" || o.Parent == pv {
			continue
		}
		c.setText("")
		c.gen++
		c.note = fmt.Sprintf("cleared %q: it belongs to %s %q, not %q", o.Display(), s.f.Label, o.Parent, pv)
	}
}

func (f *Form) completeCmd(s *fieldState) tea.Cmd {
	if s.f.Complete == nil {
		return nil
	}
	comp, id, gen, q, fid, tmo := s.f.Complete, s.f.ID, s.gen, s.text(), f.ID, f.CompleteTimeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tmo)
		defer cancel()
		opts, err := comp(ctx, q)
		return CompletionMsg{FormID: fid, FieldID: id, Gen: gen, Options: opts, Err: err}
	}
}

func (f *Form) cycleSuggest(s *fieldState, d int) {
	if s.cyc == nil {
		s.cyc, s.cycIdx = f.suggestions(s), -1
	}
	n := len(s.cyc)
	if n == 0 {
		return
	}
	if s.cycIdx < 0 && d < 0 {
		s.cycIdx = 0
	}
	s.cycIdx = (s.cycIdx + d + n) % n
	s.setText(s.cyc[s.cycIdx].Display())
	s.answered, s.fromDefault = true, false
	s.gen++
	s.err = ""
	f.relink(s)
}

func (f *Form) cycle(s *fieldState, d int) tea.Cmd {
	switch {
	case s.f.Kind == Bool:
		return f.toggle(s)
	case s.f.Kind == MultiSelect:
		if n := len(f.options(s)); n > 0 {
			s.hl = (s.hl + d + n) % n
		}
		return nil
	case !s.f.Kind.choiceLike() || s.f.multi():
		return nil
	}
	opts := f.options(s)
	if len(opts) == 0 {
		return nil
	}
	i := -1
	for j, o := range opts {
		if o.is(s.text()) {
			i = j
		}
	}
	if i < 0 && d < 0 {
		i = 0
	}
	s.setText(opts[(i+d+len(opts))%len(opts)].Display())
	return f.touched(s)
}

func (f *Form) toggle(s *fieldState) tea.Cmd {
	s.b, s.bset = !s.b, true
	return f.touched(s)
}

func (f *Form) toggleHL(s *fieldState) tea.Cmd {
	opts := f.options(s)
	if s.hl >= len(opts) {
		return nil
	}
	v := opts[s.hl].Value
	for i, it := range s.items {
		if it == v {
			s.items = append(s.items[:i], s.items[i+1:]...)
			return f.touched(s)
		}
	}
	s.items = append(s.items, v)
	return f.touched(s)
}

func (f *Form) addPending(s *fieldState) tea.Cmd {
	t := strings.TrimSpace(s.text())
	if t == "" {
		return nil
	}
	v := t
	if o, ok := ExactMatch(t, f.allOptions(s)); ok {
		v = o.Value
	} else if s.f.Kind == MultiSelect {
		s.err = fmt.Sprintf("%q is not one of the allowed options", t)
		return nil
	}
	for _, it := range s.items {
		if it == v {
			s.setText("")
			return f.touched(s)
		}
	}
	s.items = append(s.items, v)
	s.setText("")
	return f.touched(s)
}

func (f *Form) pick(s *fieldState) tea.Cmd {
	if s.f.Kind != Path {
		return nil
	}
	if !s.f.Path.Local() {
		s.note = fmt.Sprintf("no local picker for remote host %q; type the path as it exists on %s", s.f.Path.Host, s.f.Path.Host)
		return nil
	}
	v := s.text()
	if v == "" && len(s.items) > 0 {
		v = s.items[len(s.items)-1]
	}
	s.note = "fuzzy " + s.f.Path.Mode.String() + " picker open…"
	return emit(PickRequestMsg{FormID: f.ID, FieldID: s.f.ID, Gen: s.gen, Mode: s.f.Path.Mode,
		Multiple: s.f.Path.Multiple, Base: pickBase(v), Title: s.f.Label})
}

func (f *Form) setFocus(i int) {
	n := len(f.fields)
	i = ((i % n) + n) % n
	f.cur().blur()
	f.focus = i
	f.setInsert(f.insert)
}

func (f *Form) setInsert(on bool) {
	s := f.cur()
	if s.f.Kind == Bool {
		on = false
	}
	f.insert = on
	if on {
		s.in.Focus()
		if s.f.Kind == Multiline {
			s.ta.Focus()
		}
	} else {
		s.blur()
	}
}

func (f *Form) finderKey(k tea.KeyMsg) tea.Cmd {
	matches := f.finderMatches()
	switch k.String() {
	case "esc":
		f.finding = false
	case "ctrl+n", "down":
		if len(matches) > 0 {
			f.findHL = (f.findHL + 1) % len(matches)
		}
	case "ctrl+p", "up":
		if len(matches) > 0 {
			f.findHL = (f.findHL - 1 + len(matches)) % len(matches)
		}
	case "enter":
		f.finding = false
		if f.findHL < len(matches) {
			f.FocusField(matches[f.findHL].Value)
		}
	default:
		var cmd tea.Cmd
		f.finder, cmd = f.finder.Update(k)
		f.findHL = 0
		return cmd
	}
	return nil
}

func (f *Form) finderMatches() []Option {
	opts := make([]Option, len(f.fields))
	for i, s := range f.fields {
		opts[i] = Option{Value: s.f.ID, Label: s.f.Label}
	}
	return Rank(f.finder.Value(), opts)
}

// Submit validates every field. On failure it keeps the draft, marks errors
// and focuses the first invalid field; on success it emits SubmitMsg.
func (f *Form) Submit() tea.Cmd {
	vals, staged := map[string]any{}, map[string][]string{}
	bad, first := 0, -1
	for i, s := range f.fields {
		v, has, none, st, invalid := f.value(s)
		s.err = invalid
		if invalid == "" && s.f.Required && !has && !none {
			s.err = "required: choose a value"
			if s.f.NoValue != nil {
				s.err += " or " + s.f.NoValue.Display()
			}
		}
		if s.err == "" && has && s.f.Validate != nil {
			if err := s.f.Validate(v); err != nil {
				s.err = err.Error()
			}
		}
		if s.err != "" {
			bad++
			if first < 0 {
				first = i
			}
			continue
		}
		if has || none {
			vals[s.f.ID] = v
		}
		if len(st) > 0 {
			staged[s.f.ID] = st
		}
	}
	if bad > 0 {
		f.status = fmt.Sprintf("%d field(s) need attention; draft kept", bad)
		f.setFocus(first)
		return nil
	}
	f.status = ""
	return emit(SubmitMsg{FormID: f.ID, Values: vals, Staged: staged})
}

// ---- view ----

func (f *Form) labelOf(s *fieldState, v string) string {
	if o, ok := ExactMatch(v, f.allOptions(s)); ok {
		return o.Display()
	}
	return v
}

func (f *Form) display(s *fieldState, focused bool) string {
	if focused && f.insert {
		if s.f.Kind == Multiline {
			return "\n" + s.ta.View()
		}
		if s.f.multi() {
			return f.itemsText(s) + " " + s.in.View()
		}
		return s.in.View()
	}
	switch {
	case s.f.Kind == Bool:
		if !s.bset {
			return "(unset)"
		}
		if s.b {
			return "yes"
		}
		return "no"
	case s.f.multi():
		t := f.itemsText(s)
		if p := s.text(); p != "" {
			t += " " + p
		}
		if strings.TrimSpace(t) == "" {
			return "(none)"
		}
		return t
	}
	t := s.text()
	if t == "" {
		return "(empty)"
	}
	if s.f.Kind == Multiline && !focused {
		if lines := strings.Split(t, "\n"); len(lines) > 1 {
			return fmt.Sprintf("%s … (+%d lines)", lines[0], len(lines)-1)
		}
	}
	if s.f.Kind == Multiline {
		return "\n    " + strings.ReplaceAll(t, "\n", "\n    ")
	}
	return t
}

func (f *Form) itemsText(s *fieldState) string {
	var parts []string
	for _, it := range s.items {
		parts = append(parts, "["+f.labelOf(s, it)+"]")
	}
	return strings.Join(parts, " ")
}

// View renders the form.
func (f *Form) View() string {
	var b strings.Builder
	mode := "NORMAL"
	if f.insert {
		mode = "INSERT"
	}
	b.WriteString(fmt.Sprintf("%s  [%s]", f.Title, mode))
	if f.Dirty() {
		b.WriteString("  ● unsaved changes")
	}
	if f.status != "" {
		b.WriteString("  ! " + f.status)
	}
	b.WriteString("\n")
	for i, s := range f.fields {
		focused := i == f.focus
		mark := "  "
		if focused {
			mark = "> "
		}
		num := " "
		if i < 10 {
			num = strconv.Itoa((i + 1) % 10)
		}
		label := s.f.Label
		if s.f.Required {
			label += "*"
		}
		if s.f.Shortcut != 0 {
			label += " (" + string(s.f.Shortcut) + ")"
		}
		line := fmt.Sprintf("%s%s %-18s %s", mark, num, label, f.display(s, focused))
		if !focused && f.Width > 0 {
			line = ansi.Truncate(line, f.Width, "…")
		}
		b.WriteString(line + "\n")
		f.details(&b, s, focused)
	}
	if f.finding {
		b.WriteString("\n" + f.finder.View() + "\n")
		for i, o := range f.finderMatches() {
			m := "   "
			if i == f.findHL {
				m = " > "
			}
			b.WriteString(m + o.Display() + "\n")
		}
	}
	b.WriteString("\n" + f.help())
	return b.String()
}

func (f *Form) details(b *strings.Builder, s *fieldState, focused bool) {
	sub := func(t string) { b.WriteString("      " + t + "\n") }
	v, has, none, staged, invalid := f.value(s)
	_ = v
	if none {
		sub("explicit choice: " + s.f.NoValue.Display())
	} else if s.f.Required && !has && invalid == "" {
		sub("missing (required)")
	}
	for _, st := range staged {
		sub("+ new: " + st + "  (created on submit)")
	}
	if s.err != "" {
		sub("! " + s.err)
	} else if invalid != "" && s.answered {
		sub("! " + invalid)
	}
	if s.note != "" {
		sub("· " + s.note)
	}
	if !focused {
		return
	}
	if s.f.Hint != "" {
		sub("hint: " + s.f.Hint)
	}
	if s.f.Default != nil {
		sub(fmt.Sprintf("default: %v", s.f.Default))
	}
	if s.f.Kind == Choice || s.f.Kind == Linked || s.f.Kind == MultiSelect {
		sub("closed list: only listed options are valid")
	}
	if s.f.Kind == Path {
		if s.f.Path.Local() {
			sub("host: local — ctrl+o opens the fuzzy " + s.f.Path.Mode.String() + " picker; typing completes")
		} else {
			sub(fmt.Sprintf("host: %s (remote) — no local picker for remote host; enter a path on %s", s.f.Path.Host, s.f.Path.Host))
		}
	}
	if s.f.Kind == MultiSelect && !f.insert {
		var parts []string
		for i, o := range f.options(s) {
			box := "[ ]"
			for _, it := range s.items {
				if it == o.Value {
					box = "[x]"
				}
			}
			m := ""
			if i == s.hl {
				m = ">"
			}
			parts = append(parts, m+box+" "+o.Display())
		}
		sub(strings.Join(parts, "  "))
		return
	}
	if sug := f.suggestions(s); len(sug) > 0 {
		if len(sug) > 6 {
			sug = sug[:6]
		}
		var parts []string
		for i, o := range sug {
			t := o.Display()
			if s.cyc != nil && i == s.cycIdx {
				t = ">" + t
			}
			parts = append(parts, t)
		}
		sub("↳ " + strings.Join(parts, " · ") + "   (ctrl+n/ctrl+p)")
	}
}

func (f *Form) help() string {
	if f.finding {
		return "type to find field · ↑/↓ ctrl+n/p · enter jump · esc close"
	}
	if f.insert {
		return "esc normal · tab/shift+tab field · ctrl+n/p suggestions · enter next/add · ctrl+o picker · ctrl+s submit"
	}
	return "j/k field · 1-9 jump · / find · i edit · h/l cycle · space toggle · x clear · ctrl+o picker · ctrl+s submit · esc cancel"
}
