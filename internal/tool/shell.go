package tool

import (
	"fmt"
	"strings"
)

// quote wraps s so `sh -c` sees one literal argument.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// heredoc writes body to path untouched: the delimiter is quoted so
// nothing expands, and grown until the body cannot end it early.
func heredoc(path, body string) string {
	if body == "" {
		return ": > " + quote(path) // a heredoc would write a newline
	}
	delim := "DETENT_EOF"
	for n := 0; containsLine(body, delim); n++ {
		delim = fmt.Sprintf("DETENT_EOF_%d", n)
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return fmt.Sprintf("cat > %s <<'%s'\n%s%s", quote(path), delim, body, delim)
}

// containsLine reports whether delim appears as a line of its own,
// which is the only place a heredoc would end early.
func containsLine(body, delim string) bool {
	for line := range strings.SplitSeq(body, "\n") {
		if strings.TrimRight(line, "\r") == delim {
			return true
		}
	}
	return false
}
