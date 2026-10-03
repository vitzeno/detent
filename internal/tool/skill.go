package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
)

// Skill loads a skill found at startup by reading its SKILL.md where
// commands run, so loading one is a read like any other.
type Skill struct {
	Entries []SkillEntry
}

var _ Native = Skill{}

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
	e, err := s.entry(a)
	if err != nil {
		return "", err
	}
	file := e.Dir + "/SKILL.md"
	// -H lists a skill whose directory is a symlink, as a linked-in skill often is.
	return window(1, 500, skillMore(file), skillEmpty) + " " + path(file) + " && find -H " + path(e.Dir) +
		" -type f ! -name SKILL.md | sort | awk 'NR == 1 { print \"\\n" + skillOthers + "\" } NR <= " + strconv.Itoa(skillFiles) + "'", nil
}

// Run reads the skill here, in the same window and listing the same files.
func (s Skill) Run(ctx context.Context, a Args) capture.Result {
	e, err := s.entry(a)
	if err != nil {
		return failed(2, "skill: %v", err)
	}
	file := e.Dir + "/SKILL.md"
	f, err := os.Open(file)
	if err != nil {
		return failed(2, "skill: %v", err)
	}
	defer func() { _ = f.Close() }() // read only, so closing cannot lose anything
	out, err := windowLines(f, 1, 500, skillMore(file), skillEmpty)
	if err != nil {
		return capture.Result{ExitCode: 2, Stdout: out, Stderr: "skill: " + err.Error() + "\n"}
	}

	var others []string
	other := func(p string) {
		if filepath.Base(p) != "SKILL.md" {
			others = append(others, p)
		}
	}
	// The listing is a courtesy, so a directory it cannot read is left out as the command leaves it.
	if err := walkFiles(ctx, guard(e.Dir), nil, other, func(string, error) {}); err != nil {
		return failed(1, "skill: %v", err)
	}
	if len(others) > 0 {
		slices.Sort(others)
		out += "\n" + skillOthers + "\n" + strings.Join(others[:min(len(others), skillFiles)], "\n") + "\n"
	}
	return capture.Result{Stdout: out}
}

const (
	skillEmpty  = "[the skill is empty]"
	skillOthers = "[other files in this skill]"
	// skillFiles is how many of a skill's other files it lists.
	skillFiles = 50
)

// skillMore is the footer for a long SKILL.md, naming where to read on.
func skillMore(file string) string {
	return "[%d more lines, read on with read_file on " + strings.ReplaceAll(file, "%", "%%") + " at offset %d]"
}

// entry is the skill a call names.
func (s Skill) entry(a Args) (SkillEntry, error) {
	name := a.String("name")
	for _, e := range s.Entries {
		if e.Name == name {
			return e, nil
		}
	}
	return SkillEntry{}, fmt.Errorf("no skill named %q", name)
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
