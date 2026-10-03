package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFind_WalksUpToTheRepositoryRoot(t *testing.T) {
	root := tree(t, map[string]string{
		".git/HEAD":           "ref",
		"AGENTS.md":           "root rules",
		"svc/CLAUDE.md":       "service rules",
		"svc/api/AGENTS.md":   "api rules",
		"svc/api/x/README.md": "not read",
	})
	// Above the root, and so never read.
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(root), "AGENTS.md"), []byte("outside"), 0o644))

	files, err := Find(filepath.Join(root, "svc/api/x"), "")
	require.NoError(t, err)
	assert.Equal(t, []string{"../../../AGENTS.md", "../../CLAUDE.md", "../AGENTS.md"}, Paths(files))
	assert.Equal(t, "api rules", files[2].Text, "the nearest comes last")
}

func TestFind_PrefersAgentsOverClaudeInOneDirectory(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  string
	}{
		"both":                  {map[string]string{"AGENTS.md": "agents", "CLAUDE.md": "claude"}, "agents"},
		"claude only":           {map[string]string{"CLAUDE.md": "claude"}, "claude"},
		"agents empty, so none": {map[string]string{"AGENTS.md": " \n", "CLAUDE.md": "claude"}, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			files, err := Find(tree(t, tc.files), "")
			require.NoError(t, err)
			if tc.want == "" {
				assert.Empty(t, files)
				return
			}
			require.Len(t, files, 1)
			assert.Equal(t, tc.want, files[0].Text)
		})
	}
}

func TestFind_ReadsOnlyItsOwnDirectoryOutsideARepository(t *testing.T) {
	parent := tree(t, map[string]string{"AGENTS.md": "parent", "child/AGENTS.md": "child"})
	files, err := Find(filepath.Join(parent, "child"), "")
	require.NoError(t, err)
	assert.Equal(t, []string{"AGENTS.md"}, Paths(files))
}

// The nearest file matters most, so the budget cuts from the root down.
func TestFind_CutsTheOutermostFileFirst(t *testing.T) {
	big := strings.Repeat("line of root rules\n", MaxBytes/19+10)
	root := tree(t, map[string]string{".git/HEAD": "ref", "AGENTS.md": big, "sub/AGENTS.md": "near rules\n"})

	files, err := Find(filepath.Join(root, "sub"), "")
	require.NoError(t, err)
	require.Len(t, files, 2)
	assert.Equal(t, "near rules\n", files[1].Text)
	assert.True(t, files[0].Truncated)
	assert.LessOrEqual(t, len(files[0].Text)+len(files[1].Text), MaxBytes)
	assert.True(t, strings.HasSuffix(files[0].Text, "\n"), "cut on a whole line")
}

// The human's own file is the most general, so it comes first and any
// project file after it wins.
func TestFind_PutsTheGlobalFileFirst(t *testing.T) {
	home := tree(t, map[string]string{"AGENTS.md": "my rules"})
	global := filepath.Join(home, "AGENTS.md")
	project := tree(t, map[string]string{"CLAUDE.md": "project rules"})

	files, err := Find(project, global)
	require.NoError(t, err)
	require.Len(t, files, 2)
	assert.Equal(t, "my rules", files[0].Text)
	assert.Equal(t, "project rules", files[1].Text)

	files, err = Find(project, filepath.Join(home, "missing.md"))
	require.NoError(t, err)
	assert.Len(t, files, 1, "no global file is no error")

	files, err = Find(home, global)
	require.NoError(t, err)
	assert.Len(t, files, 1, "run from beside it, the file is read once")
}

// A cloned repository must not be able to point AGENTS.md at a key and have it sent to the model.
func TestFind_ReadsNoLinkOutOfTheRepository(t *testing.T) {
	root := tree(t, map[string]string{".git/HEAD": "ref", "docs/rules.md": "linked rules", "sub/CLAUDE.md": "near"})
	secret := filepath.Join(t.TempDir(), "credentials")
	require.NoError(t, os.WriteFile(secret, []byte("aws_secret"), 0o600))
	require.NoError(t, os.Symlink(secret, filepath.Join(root, "AGENTS.md")))

	files, err := Find(filepath.Join(root, "sub"), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside the repository")
	assert.Equal(t, []string{"CLAUDE.md"}, Paths(files), "the rest still load")

	require.NoError(t, os.Remove(filepath.Join(root, "AGENTS.md")))
	require.NoError(t, os.Symlink(filepath.Join("docs", "rules.md"), filepath.Join(root, "AGENTS.md")))
	files, err = Find(filepath.Join(root, "sub"), "")
	require.NoError(t, err)
	require.Len(t, files, 2)
	assert.Equal(t, "linked rules", files[0].Text, "a link within the repository is fine")
}

func TestFind_OneUnreadableFileKeepsTheRest(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	root := tree(t, map[string]string{".git/HEAD": "ref", "AGENTS.md": "root", "sub/AGENTS.md": "near"})
	require.NoError(t, os.Chmod(filepath.Join(root, "AGENTS.md"), 0))
	files, err := Find(filepath.Join(root, "sub"), "")
	require.Error(t, err)
	assert.Equal(t, []string{"AGENTS.md"}, Paths(files))
	assert.Equal(t, "near", files[0].Text)
}

func TestFind_SkipsADirectoryNamedLikeAFile(t *testing.T) {
	dir := tree(t, map[string]string{"AGENTS.md/x": "", "CLAUDE.md": "claude"})
	files, err := Find(dir, "")
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "claude", files[0].Text)
}

func TestFind_WalksUpFromARelativeDirectory(t *testing.T) {
	root := tree(t, map[string]string{".git/HEAD": "ref", "AGENTS.md": "root", "sub/AGENTS.md": "near"})
	t.Chdir(filepath.Join(root, "sub"))
	files, err := Find(".", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"../AGENTS.md", "AGENTS.md"}, Paths(files))
}

func TestPrompt_NamesEachFileAndSaysWhatWasCut(t *testing.T) {
	assert.Empty(t, Prompt(nil))
	p := Prompt([]File{{Path: "../AGENTS.md", Text: "be brief\n"}, {Path: "CLAUDE.md", Text: "use go", Truncated: true}})
	assert.Contains(t, p, "<instructions path=\"../AGENTS.md\">\nbe brief\n</instructions>")
	assert.Contains(t, p, "use go\n[kept the first 6 bytes, read the file for the rest]\n</instructions>")
	assert.Less(t, strings.Index(p, "be brief"), strings.Index(p, "use go"))
}

func TestCut_NeverSplitsACharacter(t *testing.T) {
	assert.Equal(t, "ab", cut("ab世界", 4))
	assert.Equal(t, "a\n", cut("a\nb世", 4))
}

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	// One level down, so a test can put a file above the root.
	dir := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for name, body := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	return dir
}
