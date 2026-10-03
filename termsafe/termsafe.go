// Package termsafe defuses text a model or a command wrote before it
// reaches a terminal. Stdlib only, so the TUI and headless can share it.
package termsafe

import (
	"fmt"
	"strings"
	"unicode"
)

// Printable shows control characters and bidi overrides as escapes rather
// than letting them act on the terminal. Newlines stay, tabs become spaces.
func Printable(s string) string {
	if strings.IndexFunc(s, unsafe) < 0 {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case !unsafe(r):
			b.WriteRune(r)
		case r == '\t':
			b.WriteString("    ")
		case r < 0x20 || r == 0x7f:
			b.WriteString("^" + string(r^0x40))
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}

// unsafe is a rune that moves the cursor, opens an escape sequence or
// reorders the text drawn around it.
func unsafe(r rune) bool {
	if r == '\n' {
		return false
	}
	return unicode.IsControl(r) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}
