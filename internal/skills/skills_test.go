package skills

import (
	"cmp"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFind_ReadsNameAndDescription(t *testing.T) {
	root := tree(t, map[string]string{
		"release/SKILL.md":       skill("release", "Cut a release.\n  Tags and pushes."),
		"release/scripts/tag.sh": "git tag",
		"notes/README.md":        "a directory with no SKILL.md is not a skill",
		"loose.md":               "neither is a file",
		"lint/SKILL.md":          skill("lint", "Run the linters"),
	})
	got, warnings := Find([]Root{{Dir: root, Project: true}})
	assert.Empty(t, warnings)
	require.Len(t, got, 2)
	assert.Equal(t, "lint", got[0].Name, "sorted by name")
	assert.Equal(t, "release", got[1].Name)
	assert.Equal(t, "Cut a release. Tags and pushes.", got[1].Description, "folded to one line for the catalog")
	assert.Equal(t, filepath.Join(root, "release"), got[1].Dir)
	assert.True(t, got[1].ModelInvocable)
	assert.True(t, got[1].UserInvocable)
	assert.True(t, got[1].Project)
}

// A project skill beats the human's own of the same name, as everywhere else.
func TestFind_TheFirstRootToNameASkillWins(t *testing.T) {
	project := tree(t, map[string]string{"deploy/SKILL.md": skill("deploy", "the project's")})
	home := tree(t, map[string]string{"deploy/SKILL.md": skill("deploy", "mine"), "mine/SKILL.md": skill("mine", "only mine")})

	got, warnings := Find([]Root{{Dir: project, Project: true}, {Dir: home}})
	require.Len(t, got, 2)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "shadowed by "+filepath.Join(project, "deploy"), "the human is told why theirs does nothing")
	assert.Equal(t, "the project's", got[0].Description)
	assert.False(t, got[1].Project)
}

// The standard's guide: load what can be loaded, warn, and skip only
// what has no description or will not parse at all.
func TestFind_IsLenient(t *testing.T) {
	cases := map[string]struct {
		file   string
		dir    string
		loads  bool
		name   string
		warned string
	}{
		"unquoted colon": {
			file: "---\nname: colon\ndescription: Use when: the build breaks\n---\nbody", loads: true, name: "colon"},
		"no name": {
			file: "---\ndescription: d\n---\n", loads: true, name: "no-name", warned: "has no name"},
		"name not its directory": {
			file: skill("other", "d"), dir: "elsewhere", loads: true, name: "other", warned: "lives in"},
		"name breaks the rules": {
			file: skill("Bad_Name", "d"), loads: true, name: "Bad_Name", warned: "not a valid skill name"},
		"no description": {file: "---\nname: no-description\n---\n", warned: "has no description"},
		"no frontmatter": {file: "# just markdown\n", warned: "no frontmatter"},
		"broken yaml":    {file: "---\nname: [unclosed\ndescription: d\n---\n", warned: "broken-yaml"},
		"crlf":           {file: "---\r\nname: crlf\r\ndescription: d\r\n---\r\nbody", loads: true, name: "crlf"},
	}
	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			dir := tc.dir
			if dir == "" {
				dir = cmp.Or(tc.name, strings.ReplaceAll(label, " ", "-"))
			}
			got, warnings := Find([]Root{{Dir: tree(t, map[string]string{dir + "/SKILL.md": tc.file}), Project: true}})
			if tc.loads {
				require.Len(t, got, 1)
				assert.Equal(t, tc.name, got[0].Name)
			} else {
				assert.Empty(t, got)
			}
			if tc.warned == "" {
				assert.Empty(t, warnings)
			} else {
				require.Len(t, warnings, 1)
				assert.Contains(t, warnings[0], tc.warned)
			}
		})
	}
}

func TestFind_ReadsWhoMayAskForIt(t *testing.T) {
	root := tree(t, map[string]string{
		"by-hand/SKILL.md":  "---\nname: by-hand\ndescription: d\ndisable-model-invocation: true\n---\n",
		"by-model/SKILL.md": "---\nname: by-model\ndescription: d\nuser-invocable: false\n---\n",
	})
	got, _ := Find([]Root{{Dir: root, Project: true}})
	require.Len(t, got, 2)
	assert.False(t, got[0].ModelInvocable)
	assert.True(t, got[0].UserInvocable)
	assert.True(t, got[1].ModelInvocable)
	assert.False(t, got[1].UserInvocable)
}

// A cloned repository must not be able to point a skill at the human's home and have it listed.
func TestFind_SkipsAProjectSkillLinkedOutOfTheRepository(t *testing.T) {
	repo := tree(t, map[string]string{"skills/inside/SKILL.md": skill("inside", "fine"), "elsewhere/linked/SKILL.md": skill("linked", "fine too")})
	outside := tree(t, map[string]string{"away/SKILL.md": skill("away", "outside")})
	require.NoError(t, os.Symlink(filepath.Join(outside, "away"), filepath.Join(repo, "skills", "away")))
	require.NoError(t, os.Symlink(filepath.Join(repo, "elsewhere", "linked"), filepath.Join(repo, "skills", "linked")))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "skills", "file"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(outside, "away", "SKILL.md"), filepath.Join(repo, "skills", "file", "SKILL.md")))

	got, warnings := Find([]Root{{Dir: filepath.Join(repo, "skills"), Project: true, Within: repo}})
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	assert.Equal(t, []string{"inside", "linked"}, names)
	assert.Len(t, warnings, 2)
	for _, w := range warnings {
		assert.Contains(t, w, "links outside the repository")
	}

	// The human's own skills are theirs to link wherever they like.
	got, _ = Find([]Root{{Dir: filepath.Join(repo, "skills")}})
	assert.Len(t, got, 3, "file links to away, so it is shadowed rather than skipped")
}

func TestRoots_WalkUpToTheRepositoryThenHome(t *testing.T) {
	repo := tree(t, map[string]string{".git/HEAD": "ref", "sub/x": ""})
	roots := Roots(filepath.Join(repo, "sub"), "/home/ada")
	var dirs []string
	for _, r := range roots {
		dirs = append(dirs, strings.TrimPrefix(r.Dir, repo))
	}
	assert.Equal(t, []string{
		"/sub/.agents/skills", "/sub/.claude/skills", "/.agents/skills", "/.claude/skills",
		"/home/ada/.agents/skills", "/home/ada/.claude/skills",
	}, dirs)
	assert.True(t, roots[3].Project)
	assert.False(t, roots[4].Project)
}

func TestRoots_FenceProjectSkillsToTheRepository(t *testing.T) {
	repo := tree(t, map[string]string{".git/HEAD": "ref", "sub/x": ""})
	for _, r := range Roots(filepath.Join(repo, "sub"), "/home/me") {
		if r.Project {
			assert.Equal(t, repo, r.Within)
		} else {
			assert.Empty(t, r.Within)
		}
	}
}

func skill(name, description string) string {
	return "---\nname: " + name + "\ndescription: |\n  " + strings.ReplaceAll(description, "\n", "\n  ") + "\n---\n# " + name + "\n"
}

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	return dir
}
