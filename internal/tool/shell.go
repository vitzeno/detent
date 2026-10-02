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

// outputBudget keeps a windowed result under capture.MaxOutputBytes, so
// its footer is never what the capture cuts off.
const outputBudget = 7 * 1024

// window prints max lines from start, stopping early at outputBudget bytes. more
// gets the count left and the line to resume from, empty is for no input.
func window(start, max int, more, empty string) string {
	return fmt.Sprintf("awk -v s=%d -v n=%d -v b=%d -v more=%s -v empty=%s %s",
		start, max, outputBudget, quote(more+`\n`), quote(empty+`\n`), quote(windowScript))
}

const windowScript = `NR < s { next }
!stop && NR >= s + n { stop = NR }
!stop && used > 0 && used + length($0) + 1 > b { stop = NR }
stop { next }
{ line = length($0) > b ? substr($0, 1, b) " [line cut]" : $0; print line; used += length(line) + 1 }
END {
  if (stop) printf more, NR - stop + 1, stop
  else if (NR < s) printf empty, NR
}`

// showDiff prints what changed in file since before, a copy taken first,
// then removes the copy and exits with rc, which the caller set from the change.
func showDiff(before, file string) string {
	return fmt.Sprintf(`[ $rc -eq 0 ] && diff -u -L %[2]s -L %[2]s "%[1]s" %[2]s | %[3]s; rm -- "%[1]s"; exit $rc`,
		before, path(file), window(1, 400, "[%d more lines of diff]", "[no change]"))
}

// path guards a path awk or find would read as a flag, since macOS's awk
// takes no "--".
func path(p string) string {
	if strings.HasPrefix(p, "-") {
		p = "./" + p
	}
	return quote(p)
}
