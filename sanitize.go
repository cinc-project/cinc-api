package cinc

import (
	"strings"
	"unicode/utf8"
)

// sanitizeServerText makes text a server sent safe to print to a terminal.
// A misbehaving server could otherwise clear the screen, retitle the window,
// hide or recolor text, overwrite earlier output with a carriage return, or
// reorder what the reader sees with a bidi override. It removes ESC-introduced
// sequences (CSI, OSC and the other string sequences, ended by BEL or ST,
// and two-byte escapes), replaces every other C0 or C1 control character but
// newline and tab with a space, and drops the Unicode bidi controls. Invalid
// UTF-8 becomes U+FFFD.
func sanitizeServerText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == 0x1b:
			i = skipEscape(s, i+1)
			continue
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			b.WriteByte(' ')
		case isBidiControl(r):
		default:
			b.WriteRune(r) // utf8.RuneError for an invalid byte
		}
		i += size
	}
	return b.String()
}

// skipEscape returns the index just past the escape sequence whose ESC ends
// at s[i]. An unterminated sequence runs to the end of s, as a terminal would
// swallow it.
func skipEscape(s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[': // CSI: parameter bytes, then intermediates, then a final byte
		for i++; i < len(s); i++ {
			c := s[i]
			if c >= 0x40 && c <= 0x7e {
				return i + 1
			}
			if c < 0x20 || c > 0x3f {
				return i // malformed; leave c to the caller
			}
		}
		return i
	case ']', 'P', 'X', '^', '_': // OSC, DCS, SOS, PM, APC: to BEL or ST
		for i++; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b {
				if i+1 < len(s) && s[i+1] == '\\' {
					return i + 2
				}
				return i // a new escape aborts the string; the caller removes it
			}
		}
		return i
	default: // intermediates, then a final byte
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
			i++
		}
		return i
	}
}

// isBidiControl reports whether r is a Unicode bidi control: the embeddings
// and overrides U+202A-U+202E, the isolates U+2066-U+2069, and the marks
// LRM, RLM and ALM.
func isBidiControl(r rune) bool {
	return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) ||
		r == 0x200e || r == 0x200f || r == 0x061c
}
