// Package defuse shows what a model or a command wrote as text, never as
// instructions to the terminal. Stdlib only, so the TUI and headless can share it.
package defuse

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Text shows control, bidi and other invisible characters as escapes,
// rather than letting them act on the terminal. Tabs become spaces.
func Text(s string) string { return clean(s, false) }

// Styled is Text that keeps colour: an SGR sequence (ESC [ … m) passes,
// and every other escape is shown rather than sent.
func Styled(s string) string { return clean(s, true) }

func clean(s string, keepSGR bool) string {
	if utf8.ValidString(s) && strings.IndexFunc(s, needsEscape) < 0 {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			// A lone byte such as 0x9b, which some terminals read as CSI.
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case keepSGR && r == 0x1b && sgrLen(s[i:]) > 0:
			n := sgrLen(s[i:])
			b.WriteString(s[i : i+n])
			size = n
		case !needsEscape(r):
			b.WriteString(s[i : i+size])
		case r == '\t':
			b.WriteString("    ")
		case r < 0x20 || r == 0x7f:
			b.WriteString("^" + string(r^0x40))
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
		i += size
	}
	return b.String()
}

// needsEscape reports a rune that moves the cursor, opens an escape sequence,
// reorders the text around it or hides itself.
func needsEscape(r rune) bool {
	// The zero-width joiner holds emoji sequences together.
	if r == '\n' || r == 0x200d {
		return false
	}
	return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
}

// sgrLen is the length of the SGR sequence s starts with, or 0.
func sgrLen(s string) int {
	if len(s) < 3 || s[0] != 0x1b || s[1] != '[' {
		return 0
	}
	for i := 2; i < len(s); i++ {
		switch c := s[i]; {
		case c == 'm':
			return i + 1
		case c >= '0' && c <= '9', c == ';', c == ':':
		default:
			return 0
		}
	}
	return 0
}
