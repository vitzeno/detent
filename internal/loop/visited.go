package loop

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vitzeno/detent/internal/exec"
)

// visitedKey builds a stable, argument-order-independent key for a
// (capability, args) pair. Oscillation detection needs this (§4.1, §4.5):
// "two kill_process calls against two different pids are not a repeat;
// against the same pid, they are."
func visitedKey(qualifiedName string, args exec.Args) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(qualifiedName)
	for _, k := range keys {
		fmt.Fprintf(&b, "|%s=%v", k, args[k])
	}
	return b.String()
}
