// Package forms implements a shared single-line editor above a field/value
// table. Enter :code to edit a field, Enter to apply it, and Ctrl+S to submit.
// New extensible values are staged only in the successful SubmitMsg.
package forms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/unixsuperhero/gmtm/internal/ui/readline"
)

// SubmitMsg is emitted after a successful submit. Staged lists, per field,
// new user-extensible values the caller must create in the same transaction.
type SubmitMsg struct {
	FormID string
	Values map[string]any
	Staged map[string][]string
}

// CancelMsg is emitted by Escape at the field table; the orchestrator decides
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
	code        string
	raw         string
	items       []string
	b, bset     bool
	answered    bool
	fromDefault bool
	gen         int
	async       []Option
	err, note   string
	hl          int
}

func (s *fieldState) text() string { return s.raw }

func (s *fieldState) setText(t string) { s.raw = t }

// Form is the form model. Update returns commands that emit SubmitMsg,
// CancelMsg, CompletionMsg and PickRequestMsg.
type Form struct {
	ID, Title       string
	CompleteTimeout time.Duration
	Width           int

	fields  []*fieldState
	byID    map[string]*fieldState
	byCode  map[string]int
	focus   int
	insert  bool
	input   textinput.Model
	editor  readline.Editor
	initial string
	status  string
}

// New rejects empty IDs, case-insensitive duplicate IDs and unknown parents.
// Codes use an unused lowercase letter where possible, then longer codes.
func New(id, title string, fields ...Field) (*Form, error) {
	f := &Form{ID: id, Title: title, byID: map[string]*fieldState{},
		byCode: map[string]int{}, CompleteTimeout: 5 * time.Second}
	ids := make(map[string]bool, len(fields))
	for _, fd := range fields {
		code := strings.ToLower(fd.ID)
		if code == "" || strings.ContainsAny(code, ": \t\r\n") {
			return nil, fmt.Errorf("forms: invalid field id %q", fd.ID)
		}
		if ids[code] {
			return nil, fmt.Errorf("forms: duplicate field id %q", fd.ID)
		}
		ids[code] = true
		code = fieldCode(code, f.byCode)
		if fd.Kind == Linked && fd.ParentID == "" {
			return nil, fmt.Errorf("forms: linked field %s needs ParentID", fd.ID)
		}
		s := &fieldState{f: fd, code: code, hl: -1}
		f.applyDefault(s)
		f.byCode[code] = len(f.fields)
		f.fields = append(f.fields, s)
		f.byID[fd.ID] = s
	}
	for _, s := range f.fields {
		if p := s.f.ParentID; p != "" && f.byID[p] == nil {
			return nil, fmt.Errorf("forms: field %s has unknown parent %s", s.f.ID, p)
		}
	}
	f.input = textinput.New()
	f.input.Prompt = "> "
	f.input.CharLimit = 0
	f.input.Cursor.SetMode(cursor.CursorStatic)
	f.input.Focus()
	if len(f.fields) == 1 {
		f.setInsert(true)
	}
	f.initial = f.draftKey()
	return f, nil
}

func fieldCode(id string, used map[string]int) string {
	for _, r := range id {
		if r < 'a' || r > 'z' {
			continue
		}
		code := string(r)
		if _, exists := used[code]; !exists {
			return code
		}
	}
	// Exhaust single-letter codes before allocating a longer code.
	for r := 'a'; r <= 'z'; r++ {
		if _, exists := used[string(r)]; !exists {
			return string(r)
		}
	}
	for code := []byte("aa"); ; {
		if _, exists := used[string(code)]; !exists {
			return string(code)
		}
		i := len(code) - 1
		for i >= 0 && code[i] == 'z' {
			code[i] = 'a'
			i--
		}
		if i < 0 {
			code = append(code, 'a')
		} else {
			code[i]++
		}
	}
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
		if s.f.Kind.choiceLike() {
			for i, item := range s.items {
				if o, ok := ExactMatch(item, f.allOptions(s)); ok {
					s.items[i] = o.Value
				}
			}
		}
	case string:
		if o, ok := ExactMatch(d, f.allOptions(s)); ok && s.f.Kind.choiceLike() {
			d = o.Display()
			if s.f.multi() {
				d = o.Value
			}
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
		s.hl = -1
	}
}

// Insert reports whether the shared input is editing a field.
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
// The shared editor is refreshed if the restored field is being edited.
func (f *Form) Restore(d Draft) {
	for _, s := range f.fields {
		s.gen++
		s.async = nil
	}
	for id, fd := range d.Fields {
		s := f.byID[id]
		if s == nil {
			continue
		}
		s.setText(fd.Text)
		s.items = append([]string(nil), fd.Items...)
		s.bset, s.b = fd.Bool != nil, fd.Bool != nil && *fd.Bool
		s.answered, s.fromDefault = fd.Answered, fd.FromDefault
		s.err, s.note, s.hl = "", "", -1
	}
	f.FocusField(d.Focus)
	if f.insert {
		f.loadInput()
	}
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
		if t != "" {
			switch strings.ToLower(t) {
			case "true", "yes":
				return true, true, false, nil, ""
			case "false", "no":
				return false, true, false, nil, ""
			default:
				return nil, false, false, nil, "choose true or false"
			}
		}
		return s.b, s.bset, false, nil, ""
	}
	if s.f.multi() {
		items := make([]string, 0, len(s.items)+1)
		pending := s.items
		if t != "" {
			pending = append(append([]string(nil), pending...), t)
		}
		for _, it := range pending {
			v := it
			if o, ok := ExactMatch(it, f.allOptions(s)); ok {
				v = o.Value
			} else if s.f.Kind == MultiSelect {
				return nil, false, false, nil, fmt.Sprintf("%q is not one of the allowed options", it)
			}
			duplicate := false
			for _, existing := range items {
				if existing == v {
					duplicate = true
					break
				}
			}
			if !duplicate {
				items = append(items, v)
				if s.f.Kind == Extensible {
					if _, ok := ExactMatch(v, f.allOptions(s)); !ok {
						staged = append(staged, v)
					}
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
	case s.f.Kind == Bool:
		return Rank(t, []Option{{Value: "true"}, {Value: "false"}})
	case s.f.Kind.choiceLike():
		return Rank(t, f.options(s))
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
		s.async, s.hl = m.Options, -1
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
		if f.insert && f.cur() == s {
			f.loadInput()
		}
	case tea.KeyMsg:
		return f.key(m)
	case pasteMsg:
		if m.gen != f.inputGeneration() {
			return nil
		}
		return f.updateInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(encodeLine(m.text)), Paste: true})
	default:
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(msg)
		return cmd
	}
	return nil
}

func (f *Form) cur() *fieldState { return f.fields[f.focus] }

func (f *Form) key(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "ctrl+s":
		return f.Submit()
	case "esc":
		if f.insert {
			f.setInsert(false)
			return nil
		}
		return emit(CancelMsg{FormID: f.ID, Dirty: f.Dirty()})
	case "ctrl+v":
		gen := f.inputGeneration()
		return func() tea.Msg {
			text, err := clipboard.ReadAll()
			if err != nil {
				return nil
			}
			return pasteMsg{text: text, gen: gen}
		}
	}
	if !f.insert {
		if k.String() == "enter" {
			code := strings.TrimSpace(f.input.Value())
			i, ok := f.byCode[strings.TrimPrefix(code, ":")]
			if !strings.HasPrefix(code, ":") || !ok {
				f.status = "unknown field code; enter :code from the table"
				return nil
			}
			f.setFocus(i)
			f.setInsert(true)
			f.status = ""
			return f.completeCmd(f.cur())
		}
		return f.updateInput(k)
	}
	s := f.cur()
	switch k.String() {
	case "ctrl+o":
		return f.pick(s)
	case "ctrl+n", "down":
		f.moveSuggestion(s, 1)
		return nil
	case "ctrl+p", "up":
		f.moveSuggestion(s, -1)
		return nil
	case "tab":
		if s.f.multi() {
			return f.acceptMulti(s, true)
		}
		f.chooseSuggestion(s)
		f.loadInput()
		return nil
	case "enter":
		if !f.applyEdit() {
			return nil
		}
		f.setInsert(false)
		return nil
	case "backspace":
		if s.f.multi() && f.input.Value() == "" && len(s.items) > 0 {
			s.items = s.items[:len(s.items)-1]
			return f.touched(s)
		}
	}
	// textinput sanitizes actual newlines. Escape them before they reach it,
	// including bracketed paste, so multiline values remain lossless.
	if k.Type == tea.KeyRunes {
		text := string(k.Runes)
		if k.Paste {
			text = encodeLine(text)
		} else {
			text = lineControls.Replace(text)
		}
		k.Runes = []rune(text)
	}
	return f.updateInput(k)
}

type pasteMsg struct {
	text string
	gen  string
}

func (f *Form) inputGeneration() string {
	if f.insert {
		return fmt.Sprintf("%s/%d", f.Focused(), f.cur().gen)
	}
	return "selector/" + f.input.Value()
}

// encodeLine/decodeLine deliberately recognize only these four escapes.
// Unknown escapes and an unfinished trailing backslash are preserved.
var lineEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
var lineControls = strings.NewReplacer("\n", `\n`, "\r", `\r`, "\t", `\t`)

func encodeLine(s string) string { return lineEscaper.Replace(s) }

func decodeLine(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '\\':
				b.WriteByte('\\')
			default:
				b.WriteByte(s[i])
				continue
			}
			i++
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func (f *Form) updateInput(k tea.KeyMsg) tea.Cmd {
	before := f.input.Value()
	cmd := f.editor.Update(&f.input, k, f.insert && f.cur().f.Kind == Path)
	if f.input.Value() != before {
		f.status = ""
		if f.insert {
			s := f.cur()
			s.setText(decodeLine(f.input.Value()))
			if s.f.Kind == Bool {
				s.bset = false
			}
			return tea.Batch(cmd, f.touched(s))
		}
	}
	return cmd
}

func (f *Form) moveSuggestion(s *fieldState, delta int) {
	n := len(f.suggestions(s))
	if n == 0 {
		s.hl = -1
		return
	}
	if s.hl < 0 && delta < 0 {
		s.hl = 0
	}
	s.hl = (s.hl + delta + n) % n
}

func (f *Form) chooseSuggestion(s *fieldState) {
	options := f.suggestions(s)
	if s.hl >= 0 && s.hl < len(options) {
		o := options[s.hl]
		text := o.Value
		if s.f.Kind.choiceLike() {
			text = o.Display()
		}
		s.setText(text)
		f.touched(s)
	}
}

func (f *Form) acceptMulti(s *fieldState, toggle bool) tea.Cmd {
	f.chooseSuggestion(s)
	t := strings.TrimSpace(s.text())
	if t == "" {
		return nil
	}
	v := t
	if o, ok := ExactMatch(t, f.options(s)); ok {
		v = o.Value
	} else if s.f.Kind == MultiSelect {
		s.err = fmt.Sprintf("%q is not one of the allowed options", t)
		return nil
	}
	found := false
	for i, it := range s.items {
		if it == v {
			if toggle {
				s.items = append(s.items[:i], s.items[i+1:]...)
			}
			found = true
			break
		}
	}
	if !found {
		s.items = append(s.items, v)
	}
	s.setText("")
	cmd := f.touched(s)
	f.loadInput()
	return cmd
}

func (f *Form) applyEdit() bool {
	if !f.insert {
		return true
	}
	s := f.cur()
	f.chooseSuggestion(s)
	_, _, _, _, invalid := f.value(s)
	if invalid != "" {
		s.err = invalid
		f.loadInput()
		return false
	}
	if s.f.multi() {
		f.acceptMulti(s, false)
	} else if s.f.Kind == Bool {
		v, has, _, _, _ := f.value(s)
		s.b, s.bset = v.(bool), has
		s.setText("")
		f.touched(s)
	} else {
		f.touched(s)
	}
	f.loadInput()
	return true
}

// touched records a user edit: bumps the generation, re-applies linking and
// requests async completion.
func (f *Form) touched(s *fieldState) tea.Cmd {
	s.answered, s.fromDefault = true, false
	s.gen++
	s.hl = -1
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
	if len(f.fields) == 0 {
		return
	}
	n := len(f.fields)
	f.focus = ((i % n) + n) % n
	if f.insert {
		f.loadInput()
	}
}

func (f *Form) loadInput() {
	s := f.cur()
	text := s.text()
	if s.f.Kind == Bool && text == "" && s.bset {
		text = strconv.FormatBool(s.b)
	}
	f.input.SetValue(encodeLine(text))
	f.input.CursorEnd()
}

func (f *Form) setInsert(on bool) {
	f.insert = on
	if on {
		f.cur().hl = -1
		f.loadInput()
	} else {
		f.input.SetValue("")
	}
}

// Submit validates every field. On failure it keeps the draft, marks errors
// and focuses the first invalid field; on success it emits SubmitMsg.
func (f *Form) Submit() tea.Cmd {
	if !f.applyEdit() {
		return nil
	}
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
		f.setInsert(false)
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

func (f *Form) display(s *fieldState) string {
	switch {
	case s.f.Kind == Bool:
		if s.text() != "" {
			return encodeLine(s.text())
		}
		if !s.bset {
			return "(unset)"
		}
		return strconv.FormatBool(s.b)
	case s.f.multi():
		var parts []string
		for _, it := range s.items {
			parts = append(parts, "["+encodeLine(f.labelOf(s, it))+"]")
		}
		if s.text() != "" {
			parts = append(parts, encodeLine(s.text()))
		}
		if len(parts) == 0 {
			return "(none)"
		}
		return strings.Join(parts, " ")
	}
	if s.text() == "" {
		return "(empty)"
	}
	return encodeLine(s.text())
}

// View renders exactly one editor, followed by a two-column table.
// Multiline values are displayed as escapes, never as extra field rows.
func (f *Form) View() string {
	var b strings.Builder
	if f.Width > 3 {
		f.input.Width = f.Width - 3
	}
	b.WriteString(f.input.View() + "\n\n" + f.Title)
	if f.Dirty() {
		b.WriteString("  unsaved changes")
	}
	if f.status != "" {
		b.WriteString("  ! " + f.status)
	}
	b.WriteString("\n")
	row := func(left, right string) {
		width := 28
		if f.Width > 0 {
			width = f.Width / 2
			if width < 8 {
				width = 8
			}
		}
		left = ansi.Truncate(left, width, "…")
		line := left + strings.Repeat(" ", width-ansi.StringWidth(left)) + " | " + right
		if f.Width > 0 {
			line = ansi.Truncate(line, f.Width, "…")
		}
		b.WriteString(line + "\n")
	}
	if !f.insert {
		row("Code / Field", "Current value")
		for _, s := range f.fields {
			label := ":" + s.code + " " + s.f.Label
			if s.f.Required {
				label += "*"
			}
			value := f.display(s)
			if s.err != "" {
				value += "  ! " + s.err
			}
			if s.note != "" {
				value += "  " + encodeLine(s.note)
			}
			row(label, value)
		}
		b.WriteString("\n:code enter edit · ctrl+s submit · esc cancel")
		return b.String()
	}
	s := f.cur()
	row(":"+s.code+" "+s.f.Label, f.display(s))
	if s.f.Hint != "" {
		b.WriteString(encodeLine(s.f.Hint) + "\n")
	}
	options := f.suggestions(s)
	if len(options) > 0 {
		row("Known option", "Details")
		start := 0
		if s.hl >= 8 {
			start = s.hl - 7
		}
		for i := start; i < len(options) && i < start+8; i++ {
			o := options[i]
			mark := "  "
			if i == s.hl {
				mark = "> "
			}
			if s.f.multi() {
				mark += "[ ] "
				for _, it := range s.items {
					if it == o.Value {
						mark = strings.Replace(mark, "[ ]", "[x]", 1)
						break
					}
				}
			}
			row(mark+encodeLine(o.Display()), encodeLine(o.Hint))
		}
	}
	_, _, none, staged, invalid := f.value(s)
	if none {
		b.WriteString("explicit choice: " + s.f.NoValue.Display() + "\n")
	}
	for _, st := range staged {
		b.WriteString("+ new: " + encodeLine(st) + " (created only on successful submit)\n")
	}
	if s.err != "" {
		b.WriteString("! " + s.err + "\n")
	} else if invalid != "" && s.answered {
		b.WriteString("! " + invalid + "\n")
	}
	if s.note != "" {
		b.WriteString(encodeLine(s.note) + "\n")
	}
	b.WriteString("\nenter apply · ↑/↓ ctrl+n/p select option · tab accept")
	if s.f.multi() {
		b.WriteString("/toggle item · empty backspace remove last")
	}
	if s.f.Kind == Path {
		b.WriteString(" · ctrl+o picker")
	}
	b.WriteString(" · ctrl+s submit · esc fields\nEscapes: \\n newline · \\r return · \\t tab · \\\\ backslash")
	return b.String()
}
