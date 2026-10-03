package termsafe

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A proposed command is model text, so nothing in it may drive the
// terminal on the very row a human reads to approve it.
func TestPrintable_DefusesControls(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain text is untouched", "ls -la", "ls -la"},
		{"newlines stay", "a\nb", "a\nb"},
		{"tabs become spaces", "a\tb", "a    b"},
		{"escape is shown, not sent", "\x1b[31mred\x1b[0m", "^[[31mred^[[0m"},
		{"carriage return cannot overwrite", "rm -rf ~\rls", "rm -rf ~^Mls"},
		{"delete", "a\x7fb", "a^?b"},
		{"C1 introducer", "a\u009bb", `a\u009bb`},
		{"bidi override", "a\u202eb", `a\u202eb`},
		{"bidi isolate", "a\u2066b", `a\u2066b`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Printable(tt.in))
		})
	}
}

// ui imports it, and ui may import nothing heavier than the standard library here.
func TestPackage_DependsOnStdlibOnly(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/vitzeno/detent/termsafe").Output()
	require.NoError(t, err)
	for dep := range strings.FieldsSeq(string(out)) {
		// Stdlib paths have no dot in their first segment.
		first, _, _ := strings.Cut(dep, "/")
		if strings.Contains(first, ".") {
			require.Equal(t, "github.com/vitzeno/detent/termsafe", dep, "termsafe must import stdlib only")
		}
	}
}
