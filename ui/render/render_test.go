package render

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorSeverity(t *testing.T) {
	assert.Equal(t, 0, ErrorSeverity("go build ./..."))
	assert.Equal(t, 2, ErrorSeverity("FAIL: TestX"))
	assert.Equal(t, 2, ErrorSeverity("panic: nil deref"))
	assert.Equal(t, 1, ErrorSeverity("warning: deprecated"))
	assert.Equal(t, 0, ErrorSeverity("all good"))
}

func TestDiffClass(t *testing.T) {
	assert.Equal(t, "add", DiffClass("+added"))
	assert.Equal(t, "del", DiffClass("-removed"))
	assert.Equal(t, "hunk", DiffClass("@@ -1 +1 @@"))
	assert.Equal(t, "meta", DiffClass("+++ b/f"))
	assert.Equal(t, "ctx", DiffClass(" context"))
}

func TestNumberLines(t *testing.T) {
	got := NumberLines([]string{"a", "b"})
	require.Len(t, got, 2)
	assert.True(t, strings.HasSuffix(got[0], "a"))
	assert.NotEqual(t, got[0], got[1])
}

func TestPrettyJSON(t *testing.T) {
	out, ok := JSON(`{"a":1,"b":[1,2]}`)
	require.True(t, ok)
	assert.Contains(t, out, "\n")
	_, ok = JSON("not json {")
	assert.False(t, ok)
	_, ok = JSON("   ")
	assert.False(t, ok)
}
