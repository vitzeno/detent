package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorSeverity(t *testing.T) {
	assert.Equal(t, 0, errorSeverity("go build ./..."))
	assert.Equal(t, 2, errorSeverity("FAIL: TestX"))
	assert.Equal(t, 2, errorSeverity("panic: nil deref"))
	assert.Equal(t, 1, errorSeverity("warning: deprecated"))
	assert.Equal(t, 0, errorSeverity("all good"))
}

func TestDiffClass(t *testing.T) {
	assert.Equal(t, "add", diffClass("+added"))
	assert.Equal(t, "del", diffClass("-removed"))
	assert.Equal(t, "hunk", diffClass("@@ -1 +1 @@"))
	assert.Equal(t, "meta", diffClass("+++ b/f"))
	assert.Equal(t, "ctx", diffClass(" context"))
}

func TestNumberLines(t *testing.T) {
	got := numberLines([]string{"a", "b"})
	require.Len(t, got, 2)
	assert.True(t, strings.HasSuffix(got[0], "a"))
	assert.NotEqual(t, got[0], got[1])
}

func TestPrettyJSON(t *testing.T) {
	out, ok := prettyJSON(`{"a":1,"b":[1,2]}`)
	require.True(t, ok)
	assert.Contains(t, out, "\n")
	_, ok = prettyJSON("not json {")
	assert.False(t, ok)
	_, ok = prettyJSON("   ")
	assert.False(t, ok)
}
