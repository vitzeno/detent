package tool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkill_ReadsTheBodyAndListsItsFiles(t *testing.T) {
	dir := tree(t, map[string]string{
		"release/SKILL.md":       "---\nname: release\ndescription: d\n---\n# Release\nTag it.\n",
		"release/scripts/tag.sh": "git tag",
		"release/ref/notes.md":   "notes",
	})
	s := Skill{Entries: []SkillEntry{{Name: "release", Description: "d", Dir: dir + "/release"}}}

	out := run(t, s, Args{"name": "release"})
	assert.Contains(t, out, "# Release\nTag it.\n")
	assert.Contains(t, out, "[other files in this skill]\n"+dir+"/release/ref/notes.md\n"+dir+"/release/scripts/tag.sh\n")
}

func TestSkill_SaysWhereToReadOnInALongSkill(t *testing.T) {
	dir := tree(t, map[string]string{"big/SKILL.md": numbered(800)})
	out := run(t, Skill{Entries: []SkillEntry{{Name: "big", Dir: dir + "/big"}}}, Args{"name": "big"})
	assert.Contains(t, out, "[300 more lines, read on with read_file on "+dir+"/big/SKILL.md at offset 501]")
	assert.NotContains(t, out, "other files", "a skill with nothing else says nothing about it")
}

// The names are an enum, so under strict mode the endpoint refuses a
// skill that does not exist, and Prepare refuses it for any endpoint.
func TestSkill_OffersOnlyTheSkillsThatExist(t *testing.T) {
	r := Standard(Skill{Entries: []SkillEntry{{Name: "lint", Description: "run linters"}, {Name: "release", Description: "cut one"}}})

	raw, err := json.Marshal(r.Schemas())
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"enum":["lint","release"]`)
	assert.Contains(t, string(raw), `- lint: run linters\n- release: cut one`)

	_, err = r.Prepare("skill", map[string]any{"name": "deploy"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be one of: lint, release")
}

// A skill the human keeps for themselves can be loaded when they ask,
// so it stays in the enum, but the model is not told what it is for.
func TestSkill_HidesAManualSkillFromTheCatalog(t *testing.T) {
	s := Skill{Entries: []SkillEntry{{Name: "deploy", Description: "ship to prod", Hidden: true}, {Name: "lint", Description: "run linters"}}}
	assert.NotContains(t, s.Describe().Description, "ship to prod")
	assert.Equal(t, []string{"deploy", "lint"}, s.Describe().Params[0].Enum)
}

func TestSkill_RegistersWithTheBuiltIns(t *testing.T) {
	r := Standard(Skill{})
	r.Register(fakeMCP{"aaa__first"})
	names := r.Names()
	assert.Equal(t, "aaa__first", names[len(names)-1], "a skill is not sorted in among MCP tools")
}

func TestSkill_ShortensDescriptionsToFitTheCatalog(t *testing.T) {
	var entries []SkillEntry
	for i := range 40 {
		entries = append(entries, SkillEntry{Name: strings.Repeat("s", i+1), Description: strings.Repeat("word ", 200)})
	}
	got := Skill{Entries: entries}.catalog()
	assert.LessOrEqual(t, len(got), catalogBudget)
	assert.Equal(t, 40, strings.Count(got, "\n"), "every skill keeps its line")
}

type fakeMCP struct{ name string }

func (f fakeMCP) Name() string               { return f.name }
func (f fakeMCP) Describe() Spec             { return Spec{Description: "x", Executor: "mcp"} }
func (f fakeMCP) Lower(Args) (string, error) { return "", nil }

// awk -v reads escapes, so a backslash in the skill's path must reach the footer as one.
func TestSkill_KeepsABackslashInItsPath(t *testing.T) {
	dir := tree(t, map[string]string{`a\b/SKILL.md`: numbered(600)})
	out := run(t, Skill{Entries: []SkillEntry{{Name: "x", Dir: dir + `/a\b`}}}, Args{"name": "x"})
	assert.Contains(t, out, "read_file on "+dir+`/a\b/SKILL.md at offset 501`)
}

func TestSkill_FailsWhenItsFileIsGone(t *testing.T) {
	cmd, err := Skill{Entries: []SkillEntry{{Name: "x", Dir: t.TempDir()}}}.Lower(Args{"name": "x"})
	require.NoError(t, err)
	_, err = shIn("", cmd)
	assert.Error(t, err)
}
