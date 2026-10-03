package version_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/version"
)

func TestString_StartsWithTheNumber(t *testing.T) {
	got := version.String()
	require.NotEmpty(t, got)
	assert.True(t, strings.HasPrefix(got, version.Number),
		"%q should start with %q", got, version.Number)
}

// The number is what a human reads first, so it stays short and
// parseable rather than growing a v or a build into itself.
func TestNumber_IsBareSemver(t *testing.T) {
	assert.Regexp(t, `^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`, version.Number)
}
