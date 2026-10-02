package viewspec_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

const edit = `--- a/main.go
+++ b/main.go
@@ -10,4 +10,4 @@
 func main() {
-	fmt.Println("hi")
+	fmt.Println("hello")
 	os.Exit(0)
+	// done
`

var diffSpec = viewspec.Spec{Version: viewspec.Version, Parse: viewspec.Parse{Kind: "none"},
	Blocks: []viewspec.Block{{Kind: "diff"}}}

// Wide enough, the old file is on the left and the new on the right, a
// changed line beside what replaced it, each numbered as in its own file.
func TestDiff_SideBySideWhenThereIsRoom(t *testing.T) {
	got := draw(t, diffSpec, edit, 101)
	require.Len(t, got, 7)
	assert.Equal(t, "--- a/main.go", got[0])
	assert.Equal(t, "@@ -10,4 +10,4 @@", got[2])
	row := func(i int) (string, string) {
		left, right, ok := strings.Cut(got[i], " │ ")
		require.True(t, ok, got[i])
		return strings.TrimRight(left, " "), strings.TrimRight(right, " ")
	}
	l, r := row(3)
	assert.Equal(t, "  10 func main() {", l)
	assert.Equal(t, "  10 func main() {", r)
	l, r = row(4)
	assert.Equal(t, `  11     fmt.Println("hi")`, l, "the removed line, its tab expanded")
	assert.Equal(t, `  11     fmt.Println("hello")`, r, "beside what replaced it")
	l, r = row(6)
	assert.Empty(t, strings.TrimSpace(l), "an added line has nothing opposite")
	assert.Equal(t, "  13     // done", r)
	assert.Equal(t, strings.Index(got[3], "│"), strings.Index(got[4], "│"), "the halves line up")
}

func TestDiff_InlineWhenTooNarrowToSplit(t *testing.T) {
	got := draw(t, diffSpec, edit, 60)
	assert.Equal(t, strings.Split(strings.TrimSuffix(edit, "\n"), "\n"), got)
}
