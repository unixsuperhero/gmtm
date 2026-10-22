// Package readline supplies single-line Emacs editing and a per-editor kill buffer.
package readline

import (
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// Editor retains the most recently killed text for Ctrl+Y.
type Editor struct{ killed string }

// Edit handles editing keys, preserving text after the cursor. Path word
// operations treat a directory component (including spaces) as one word.
func (e *Editor) Edit(q []rune, pos int, key string, path bool) ([]rune, int, bool) {
	start, end := pos, pos
	backward := func() int {
		i := pos
		if path {
			for i > 0 && q[i-1] == '/' {
				i--
			}
			for i > 0 && q[i-1] != '/' {
				i--
			}
		} else {
			for i > 0 && unicode.IsSpace(q[i-1]) {
				i--
			}
			for i > 0 && !unicode.IsSpace(q[i-1]) {
				i--
			}
		}
		return i
	}
	forward := func() int {
		i := pos
		boundary := func(r rune) bool {
			if path {
				return r == '/'
			}
			return unicode.IsSpace(r)
		}
		for i < len(q) && boundary(q[i]) {
			i++
		}
		for i < len(q) && !boundary(q[i]) {
			i++
		}
		return i
	}
	kill := false
	switch key {
	case "ctrl+a", "home":
		return q, 0, true
	case "ctrl+e", "end":
		return q, len(q), true
	case "ctrl+b", "left":
		return q, max(0, pos-1), true
	case "ctrl+f", "right":
		return q, min(len(q), pos+1), true
	case "alt+b", "ctrl+left":
		return q, backward(), true
	case "alt+f", "ctrl+right":
		return q, forward(), true
	case "ctrl+h", "backspace":
		start = max(0, pos-1)
	case "ctrl+d", "delete":
		end = min(len(q), pos+1)
	case "ctrl+u":
		start, kill = 0, true
	case "ctrl+k":
		end, kill = len(q), true
	case "ctrl+w", "alt+backspace":
		start, kill = backward(), true
	case "alt+d":
		end, kill = forward(), true
	case "ctrl+y":
		y := []rune(e.killed)
		q = append(q, make([]rune, len(y))...)
		copy(q[pos+len(y):], q[pos:len(q)-len(y)])
		copy(q[pos:], y)
		return q, pos + len(y), true
	case "ctrl+t":
		if pos > 0 && len(q) > 1 {
			i := min(pos, len(q)-1)
			q[i-1], q[i] = q[i], q[i-1]
			pos = i + 1
		}
		return q, pos, true
	default:
		return q, pos, false
	}
	if start != end {
		if kill {
			e.killed = string(q[start:end])
		}
		q = append(q[:start], q[end:]...)
	}
	return q, start, true
}

// Update applies readline keys, delegating text entry and paste to textinput.
func (e *Editor) Update(input *textinput.Model, k tea.KeyMsg, path bool) tea.Cmd {
	switch k.String() {
	case "ctrl+a", "home":
		input.CursorStart()
		return nil
	case "ctrl+e", "end":
		input.CursorEnd()
		return nil
	case "ctrl+b", "left":
		input.SetCursor(input.Position() - 1)
		return nil
	case "ctrl+f", "right":
		input.SetCursor(input.Position() + 1)
		return nil
	case "alt+b", "ctrl+left", "alt+f", "ctrl+right", "ctrl+h", "backspace", "ctrl+d", "delete", "ctrl+u", "ctrl+k", "ctrl+w", "alt+backspace", "alt+d", "ctrl+y", "ctrl+t":
		q, pos, _ := e.Edit([]rune(input.Value()), input.Position(), k.String(), path)
		input.SetValue(string(q))
		input.SetCursor(pos)
		return nil
	default:
		var cmd tea.Cmd
		*input, cmd = input.Update(k)
		return cmd
	}
}
