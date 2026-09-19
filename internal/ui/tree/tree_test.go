package tree

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// layout builds: root/a.txt, root/sub/b.txt, root/.hidden
func layout(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("b"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".hidden"), []byte("h"), 0o644))
	return root
}

func TestBuild_SkipsDotfilesAndSortsDirsFirst(t *testing.T) {
	root, truncated, err := Build(layout(t))
	require.NoError(t, err)
	assert.False(t, truncated)
	require.Len(t, root.Children, 2, "a.txt and sub, .hidden skipped")
	assert.Equal(t, "sub", root.Children[0].Name, "directories sort before files")
	assert.Equal(t, KindDir, root.Children[0].Kind)
	assert.Equal(t, "a.txt", root.Children[1].Name)
	assert.Equal(t, KindFile, root.Children[1].Kind)
	require.Len(t, root.Children[0].Children, 1)
	assert.Equal(t, "b.txt", root.Children[0].Children[0].Name)
}

func TestBuild_NotADirectoryIsAnError(t *testing.T) {
	root := layout(t)
	_, _, err := Build(filepath.Join(root, "a.txt"))
	assert.Error(t, err)
}

func TestModel_RootStartsExpandedChildrenCollapsed(t *testing.T) {
	rootPath := layout(t)
	root, _, err := Build(rootPath)
	require.NoError(t, err)
	m := New(root)
	// Root expanded by default shows its two direct children; sub itself
	// starts collapsed, so b.txt is not visible yet.
	assert.Equal(t, []string{filepath.Base(rootPath), "sub", "a.txt"}, rowNames(m))
}

func TestModel_ToggleExpandsAndCollapsesADir(t *testing.T) {
	rootPath := layout(t)
	root, _, err := Build(rootPath)
	require.NoError(t, err)
	m := New(root)
	m.Down() // cursor -> sub
	require.Equal(t, "sub", m.Selected().Name)

	m.Toggle()
	assert.Equal(t, []string{filepath.Base(rootPath), "sub", "b.txt", "a.txt"}, rowNames(m), "expanding sub reveals b.txt")

	m.Toggle()
	assert.Equal(t, []string{filepath.Base(rootPath), "sub", "a.txt"}, rowNames(m), "collapsing sub hides it again")
}

func TestModel_ToggleOnAFileIsANoOp(t *testing.T) {
	root, _, err := Build(layout(t))
	require.NoError(t, err)
	m := New(root)
	m.Down()
	m.Down() // cursor -> a.txt
	require.Equal(t, "a.txt", m.Selected().Name)
	before := rowNames(m)
	m.Toggle()
	assert.Equal(t, before, rowNames(m))
}

func TestModel_UpDownClampAtEnds(t *testing.T) {
	rootPath := layout(t)
	root, _, err := Build(rootPath)
	require.NoError(t, err)
	m := New(root)
	m.Up()
	assert.Equal(t, filepath.Base(rootPath), m.Selected().Name, "cannot move above the first row")

	for range 10 {
		m.Down()
	}
	assert.Equal(t, "a.txt", m.Selected().Name, "cannot move past the last visible row")
}

func TestModel_ViewKeepsCursorInView(t *testing.T) {
	root, _, err := Build(layout(t))
	require.NoError(t, err)
	m := New(root)
	m.Down() // sub
	m.Toggle()
	m.Down() // b.txt — rows are now [root, sub, b.txt, a.txt], cursor at 2

	lines := m.View(40, 2)
	require.Len(t, lines, 2)
	assert.Contains(t, lines[len(lines)-1], "b.txt", "a height-2 view must scroll to keep the cursor visible")
}

func TestModel_ViewTruncatesLongNames(t *testing.T) {
	root := t.TempDir()
	long := "this-is-a-very-long-filename-that-should-be-truncated.txt"
	require.NoError(t, os.WriteFile(filepath.Join(root, long), []byte("x"), 0o644))
	n, _, err := Build(root)
	require.NoError(t, err)
	m := New(n)

	lines := m.View(20, 5)
	for _, l := range lines {
		assert.LessOrEqual(t, len([]rune(l)), 20)
	}
}

func rowNames(m Model) []string {
	var out []string
	for _, r := range m.rows {
		out = append(out, r.Name)
	}
	return out
}
