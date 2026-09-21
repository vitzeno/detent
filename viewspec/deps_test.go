package viewspec_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The package is meant to be extractable to its own repository, and
// the only thing that keeps that true is this test. Every design
// decision in docs/design/viewspec.md is defensible by argument; this
// one is enforceable, so it is enforced.
func TestPackage_DependsOnStdlibOnly(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/vitzeno/detent/viewspec").Output()
	require.NoError(t, err)
	for _, dep := range strings.Fields(string(out)) {
		// Stdlib paths have no dot in their first segment.
		first, _, _ := strings.Cut(dep, "/")
		if !strings.Contains(first, ".") {
			continue
		}
		require.True(t, strings.HasPrefix(dep, "github.com/vitzeno/detent/viewspec"),
			"viewspec must import stdlib only, found %q", dep)
	}
}
