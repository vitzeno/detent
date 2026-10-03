package viewspec_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/viewspec"
)

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

// Inside a hunk, a removed "-- note" is a removed line, not a file header.
func TestDiff_ContentStartingWithDashesStaysInItsHunk(t *testing.T) {
	const sql = "--- a/q.sql\n+++ b/q.sql\n@@ -1,2 +1,2 @@\n--- old note\n+++ new note\n select 1;\n"
	got := draw(t, diffSpec, sql, 101)
	require.Len(t, got, 5)
	left, right, ok := strings.Cut(got[3], " │ ")
	require.True(t, ok, got[3])
	assert.Equal(t, "   1 -- old note", strings.TrimRight(left, " "))
	assert.Equal(t, "   1 ++ new note", strings.TrimRight(right, " "))

	painted := drawPainted(t, diffSpec, sql, 60)
	assert.True(t, strings.HasPrefix(painted[3], "danger:"), painted[3])
	assert.True(t, strings.HasPrefix(painted[4], "safe:"), painted[4])
	assert.True(t, strings.HasPrefix(painted[0], "muted:"), painted[0])
}

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
