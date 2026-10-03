package tool

import (
	"fmt"
	"strings"

	"github.com/vitzeno/detent/event"
)

// Skill loads a skill found at startup by reading its SKILL.md where
// commands run, so loading one is a read like any other.
type Skill struct {
	Entries []SkillEntry
}

// SkillEntry is one skill the model may load, Dir as the runner sees it.
// Hidden leaves it out of the catalog, for one only the human asks for.
type SkillEntry struct {
	Name        string
	Description string
	Dir         string
	Hidden      bool
}

// catalogBudget bounds the list of skills in the tool's description.
const catalogBudget = 8000

func (Skill) Name() string { return "skill" }

func (s Skill) Describe() Spec {
	names := make([]string, len(s.Entries))
	for i, e := range s.Entries {
		names[i] = e.Name
	}
	return Spec{
		Description: "Load a skill: instructions for a kind of task, kept by the project or the human. " +
			"When a request matches a skill's description, load it before starting and follow it. " +
			"It lists its other files, read those only when it says to. " +
			"Load a skill again rather than working from a summary of it.\n\nSkills:\n" + s.catalog(),
		Params: []Param{
			{Name: "name", Type: TypeString, Desc: "the skill to load", Required: true, Enum: names},
		},
		Mutability:  event.MutRead,
		Group:       "skills",
		GroupDetail: fmt.Sprintf("%d skills", len(s.Entries)),
	}
}

func (s Skill) Lower(a Args) (string, error) {
	name := a.String("name")
	for _, e := range s.Entries {
		if e.Name != name {
			continue
		}
		file := e.Dir + "/SKILL.md"
		more := "[%d more lines, read on with read_file on " + strings.ReplaceAll(file, "%", "%%") + " at offset %d]"
		return window(1, 500, more, "[the skill is empty]") + " " + path(file) + " && find " + path(e.Dir) +
			" -type f ! -name SKILL.md | sort | awk 'NR == 1 { print \"\\n[other files in this skill]\" } NR <= 50'", nil
	}
	return "", fmt.Errorf("no skill named %q", name)
}

// catalog lists each skill on a line, shortening every description
// alike when the list would outgrow catalogBudget.
func (s Skill) catalog() string {
	for limit := 1024; ; limit /= 2 {
		var b strings.Builder
		for _, e := range s.Entries {
			if e.Hidden {
				continue
			}
			d := e.Description
			if len(d) > limit {
				d = strings.TrimSpace(strings.ToValidUTF8(d[:limit], "")) + "…"
			}
			fmt.Fprintf(&b, "- %s: %s\n", e.Name, d)
		}
		if b.Len() <= catalogBudget || limit <= 64 {
			return b.String()
		}
	}
}
