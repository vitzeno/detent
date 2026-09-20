package host

import (
	"fmt"
	"strings"
)

// Result is a command's captured outcome. Possibly removable if
// headless mode (-goal) goes away; not yet settled either way.
type Result struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Truncated bool
}

// Summary renders a one-line result.
func (r Result) Summary() string {
	lines := 0
	for _, s := range []string{r.Stdout, r.Stderr} {
		if s == "" {
			continue
		}
		lines += strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
	}
	s := fmt.Sprintf("exit %d, %d lines", r.ExitCode, lines)
	if r.Truncated {
		s += " (truncated)"
	}
	return s
}
