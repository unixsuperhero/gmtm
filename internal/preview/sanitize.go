package preview

// Sanitizer is a streaming filter for untrusted terminal output. It always
// drops OSC (title, clipboard/52, hyperlinks/8, colours...), DCS, SOS, PM and
// APC strings, stray C1 controls and invalid UTF-8. Sequences may be split
// across Write calls.
//
// In SGR-only mode (preview) every CSI except plain SGR (`CSI <digits;:> m`)
// and every other escape/control is dropped; only text, \n, \t and SGR remain.
// In control mode (live) CSI and two-byte ESC sequences plus the usual C0
// controls are kept so a terminal emulator can lay out the screen.
type Sanitizer struct {
	keepControl bool
	st          sstate
	seq         []byte // current CSI / ESC sequence being collected
	u8          []byte // pending UTF-8 bytes
	u8need      int
	strEsc      bool // saw ESC inside a string (possible ST)
}

type sstate uint8

const (
	sGround sstate = iota
	sEsc
	sEscInter
	sCSI
	sString // OSC / DCS / SOS / PM / APC: dropped until ST or BEL
)

const maxSeq = 256

// NewSanitizer returns a filter. keepControl=false keeps only text and SGR.
func NewSanitizer(keepControl bool) *Sanitizer { return &Sanitizer{keepControl: keepControl} }

// Sanitize filters a complete buffer in SGR-only mode.
func Sanitize(b []byte) []byte { return NewSanitizer(false).Write(b) }

// Write filters p and returns the bytes that are safe to forward.
func (s *Sanitizer) Write(p []byte) []byte {
	out := make([]byte, 0, len(p))
	for _, c := range p {
		out = s.step(out, c)
	}
	return out
}

func (s *Sanitizer) step(out []byte, c byte) []byte {
	// CAN / SUB abort any sequence.
	if (c == 0x18 || c == 0x1a) && s.st != sGround {
		s.reset()
		return out
	}
	switch s.st {
	case sGround:
		return s.ground(out, c)
	case sEsc:
		return s.esc(out, c)
	case sEscInter:
		if c == 0x1b {
			s.reset()
			s.st = sEsc
			s.seq = append(s.seq, c)
			return out
		}
		s.seq = append(s.seq, c)
		if c >= 0x30 && c <= 0x7e {
			if s.keepControl {
				out = append(out, s.seq...)
			}
			s.reset()
		} else if c < 0x20 || len(s.seq) > maxSeq {
			s.reset()
		}
		return out
	case sCSI:
		if c == 0x1b {
			s.reset()
			s.st = sEsc
			s.seq = append(s.seq, c)
			return out
		}
		if c < 0x20 { // C0 inside CSI: execute-in-place in real terminals; keep it simple and drop
			return out
		}
		s.seq = append(s.seq, c)
		if c >= 0x40 && c <= 0x7e {
			if s.keepControl || isSGR(s.seq) {
				out = append(out, s.seq...)
			}
			s.reset()
		} else if len(s.seq) > maxSeq {
			s.reset()
		}
		return out
	case sString:
		if s.strEsc {
			s.strEsc = false
			if c == '\\' {
				s.reset()
				return out
			}
			if c == 0x1b {
				s.strEsc = true
			}
			return out
		}
		switch c {
		case 0x07:
			s.reset()
		case 0x1b:
			s.strEsc = true
		case 0x9c: // raw 8-bit ST
			s.reset()
		}
		return out
	}
	return out
}

func (s *Sanitizer) ground(out []byte, c byte) []byte {
	if s.u8need > 0 {
		if c&0xc0 == 0x80 {
			s.u8 = append(s.u8, c)
			if len(s.u8) == s.u8need {
				out = s.rune(out)
			}
			return out
		}
		s.u8, s.u8need = s.u8[:0], 0 // invalid: drop pending, reprocess c
	}
	switch {
	case c == 0x1b:
		s.st = sEsc
		s.seq = append(s.seq[:0], c)
	case c == '\n' || c == '\t':
		out = append(out, c)
	case c < 0x20:
		if s.keepControl && (c == '\r' || c == '\b' || c == 0x0e || c == 0x0f) {
			out = append(out, c)
		}
	case c == 0x7f:
	case c < 0x80:
		out = append(out, c)
	case c >= 0xc2 && c <= 0xdf:
		s.u8, s.u8need = append(s.u8[:0], c), 2
	case c >= 0xe0 && c <= 0xef:
		s.u8, s.u8need = append(s.u8[:0], c), 3
	case c >= 0xf0 && c <= 0xf4:
		s.u8, s.u8need = append(s.u8[:0], c), 4
	default: // stray continuation, raw C1, invalid lead
	}
	return out
}

// rune handles one complete UTF-8 sequence in s.u8.
func (s *Sanitizer) rune(out []byte) []byte {
	b := s.u8
	s.u8need = 0
	if len(b) == 2 && b[0] == 0xc2 && b[1] >= 0x80 && b[1] <= 0x9f {
		switch b[1] {
		case 0x9b: // CSI
			s.st = sCSI
			s.seq = append(s.seq[:0], 0x1b, '[')
		case 0x9d, 0x90, 0x98, 0x9e, 0x9f: // OSC DCS SOS PM APC
			s.st = sString
		}
		s.u8 = s.u8[:0]
		return out
	}
	out = append(out, b...)
	s.u8 = s.u8[:0]
	return out
}

func (s *Sanitizer) esc(out []byte, c byte) []byte {
	switch {
	case c == '[':
		s.st = sCSI
		s.seq = append(s.seq, c)
	case c == ']' || c == 'P' || c == 'X' || c == '^' || c == '_':
		s.st = sString
		s.seq = s.seq[:0]
	case c == 0x1b:
		s.seq = append(s.seq[:0], c)
	case c >= 0x20 && c <= 0x2f:
		s.st = sEscInter
		s.seq = append(s.seq, c)
	case c >= 0x30 && c <= 0x7e:
		if s.keepControl {
			out = append(out, 0x1b, c)
		}
		s.reset()
	default:
		s.reset()
	}
	return out
}

func (s *Sanitizer) reset() {
	s.st = sGround
	s.seq = s.seq[:0]
	s.strEsc = false
}

// isSGR reports whether seq is ESC [ <digits ; :>* m.
func isSGR(seq []byte) bool {
	if len(seq) < 3 || seq[len(seq)-1] != 'm' {
		return false
	}
	for _, c := range seq[2 : len(seq)-1] {
		if !(c >= '0' && c <= '9' || c == ';' || c == ':') {
			return false
		}
	}
	return true
}
