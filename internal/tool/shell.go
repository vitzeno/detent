package tool

import (
	"fmt"
	"regexp"
	"strings"
)

// quote wraps s so `sh -c` sees one literal argument.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// heredoc writes body to path byte for byte: the delimiter is quoted so
// nothing expands, and grown until the body cannot end it early.
func heredoc(path, body string) string {
	if body == "" {
		return ": > " + quote(path) // a heredoc would write a newline
	}
	delim := delimFor(body, "DETENT_EOF")
	// A heredoc always ends in a newline, so one the body lacks is cut off again.
	trim := ""
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
		trim = fmt.Sprintf(` && body=$(cat %[1]s) && printf '%%s' "$body" > %[1]s`, quote(path))
	}
	return fmt.Sprintf("cat > %s <<'%s'%s\n%s%s", quote(path), delim, trim, body, delim)
}

// delimFor grows base until no line of body could end its heredoc early.
func delimFor(body, base string) string {
	delim := base
	for n := 0; containsLine(body, delim); n++ {
		delim = fmt.Sprintf("%s_%d", base, n)
	}
	return delim
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

// keepStatus runs cmd | rest and exits with cmd's status when it is above ok,
// since a pipeline otherwise reports only its last stage.
func keepStatus(cmd, rest string, ok int) string {
	return fmt.Sprintf(`t=$(mktemp); { %s; echo $? > "$t"; } | %s; rc=$(cat "$t"); rm -- "$t"; [ "$rc" -le %d ] || exit "$rc"`,
		cmd, rest, ok)
}

// showDiff prints what changed in file since before, a copy taken first,
// then removes the copy and exits with rc, which the caller set from the change.
func showDiff(before, file string) string {
	return fmt.Sprintf(`[ $rc -eq 0 ] && diff -u -L %[2]s -L %[2]s "%[1]s" %[2]s | %[3]s; rm -- "%[1]s"; exit $rc`,
		before, path(file), window(1, diffWindow, diffMore, diffEmpty))
}

const (
	diffWindow = 400
	diffMore   = "[%d more lines of diff]"
	diffEmpty  = "[no change]"
)

// path guards a path awk or find would read as something else, since
// macOS's awk takes no "--": a flag, a find operator or an awk assignment.
func path(p string) string { return quote(guard(p)) }

// guard is p spelled so awk and find read it as a path, which is also
// how find prints what it finds under it.
func guard(p string) string {
	if strings.HasPrefix(p, "-") || strings.HasPrefix(p, "!") || strings.HasPrefix(p, "(") || awkAssignment.MatchString(p) {
		return "./" + p
	}
	return p
}

// awkAssignment is an operand awk takes as name=value rather than a file.
var awkAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
