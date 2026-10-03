package viewspec_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The package is meant to be extractable to its own repository, and
// this test is the only thing that keeps that true.
func TestPackage_DependsOnStdlibOnly(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/vitzeno/detent/viewspec").Output()
	require.NoError(t, err)
	for dep := range strings.FieldsSeq(string(out)) {
		// Stdlib paths have no dot in their first segment.
		first, _, _ := strings.Cut(dep, "/")
		if !strings.Contains(first, ".") {
			continue
		}
		require.True(t, strings.HasPrefix(dep, "github.com/vitzeno/detent/viewspec"),
			"viewspec must import stdlib only, found %q", dep)
	}
}
