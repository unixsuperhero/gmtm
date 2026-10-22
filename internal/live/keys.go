package live

import (
	tea "github.com/charmbracelet/bubbletea"
)

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

var special = map[tea.KeyType]string{
	tea.KeySpace:          " ",
	tea.KeyUp:             "\x1b[A",
	tea.KeyDown:           "\x1b[B",
	tea.KeyRight:          "\x1b[C",
	tea.KeyLeft:           "\x1b[D",
	tea.KeyShiftTab:       "\x1b[Z",
	tea.KeyHome:           "\x1b[H",
	tea.KeyEnd:            "\x1b[F",
	tea.KeyPgUp:           "\x1b[5~",
	tea.KeyPgDown:         "\x1b[6~",
	tea.KeyCtrlPgUp:       "\x1b[5;5~",
	tea.KeyCtrlPgDown:     "\x1b[6;5~",
	tea.KeyDelete:         "\x1b[3~",
	tea.KeyInsert:         "\x1b[2~",
	tea.KeyCtrlUp:         "\x1b[1;5A",
	tea.KeyCtrlDown:       "\x1b[1;5B",
	tea.KeyCtrlRight:      "\x1b[1;5C",
	tea.KeyCtrlLeft:       "\x1b[1;5D",
	tea.KeyCtrlHome:       "\x1b[1;5H",
	tea.KeyCtrlEnd:        "\x1b[1;5F",
	tea.KeyShiftUp:        "\x1b[1;2A",
	tea.KeyShiftDown:      "\x1b[1;2B",
	tea.KeyShiftRight:     "\x1b[1;2C",
	tea.KeyShiftLeft:      "\x1b[1;2D",
	tea.KeyShiftHome:      "\x1b[1;2H",
	tea.KeyShiftEnd:       "\x1b[1;2F",
	tea.KeyCtrlShiftUp:    "\x1b[1;6A",
	tea.KeyCtrlShiftDown:  "\x1b[1;6B",
	tea.KeyCtrlShiftRight: "\x1b[1;6C",
	tea.KeyCtrlShiftLeft:  "\x1b[1;6D",
	tea.KeyCtrlShiftHome:  "\x1b[1;6H",
	tea.KeyCtrlShiftEnd:   "\x1b[1;6F",
	tea.KeyF1:             "\x1bOP",
	tea.KeyF2:             "\x1bOQ",
	tea.KeyF3:             "\x1bOR",
	tea.KeyF4:             "\x1bOS",
	tea.KeyF5:             "\x1b[15~",
	tea.KeyF6:             "\x1b[17~",
	tea.KeyF7:             "\x1b[18~",
	tea.KeyF8:             "\x1b[19~",
	tea.KeyF9:             "\x1b[20~",
	tea.KeyF10:            "\x1b[21~",
	tea.KeyF11:            "\x1b[23~",
	tea.KeyF12:            "\x1b[24~",
	tea.KeyF13:            "\x1b[1;2P",
	tea.KeyF14:            "\x1b[1;2Q",
	tea.KeyF15:            "\x1b[1;2R",
	tea.KeyF16:            "\x1b[1;2S",
	tea.KeyF17:            "\x1b[15;2~",
	tea.KeyF18:            "\x1b[17;2~",
	tea.KeyF19:            "\x1b[18;2~",
	tea.KeyF20:            "\x1b[19;2~",
}

// KeyBytes converts a Bubble Tea key to the bytes a terminal would send
// (normal cursor-key mode). Pastes are bracketed; Alt prefixes ESC.
// Returns nil for keys with no byte form.
func KeyBytes(k tea.KeyMsg) []byte {
	var s string
	switch {
	case k.Type == tea.KeyRunes:
		s = string(k.Runes)
		if k.Paste {
			return []byte(pasteStart + s + pasteEnd)
		}
	case k.Type >= 0 && k.Type <= 31, k.Type == 127:
		// C0 controls and DEL: ctrl+a..z, enter(\r), tab, esc, backspace(0x7f), ctrl+z ...
		s = string(rune(k.Type))
	default:
		var ok bool
		if s, ok = special[k.Type]; !ok {
			return nil
		}
	}
	if k.Alt {
		s = "\x1b" + s
	}
	return []byte(s)
}
