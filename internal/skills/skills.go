// Package skills finds Agent Skills (agentskills.io): directories holding
// a SKILL.md, whose name and description the model sees before it loads one.
package skills

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// maxHead bounds what is read of a SKILL.md at startup, where only its header is wanted.
const maxHead = 64 * 1024

var validName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Skill is one SKILL.md and what its frontmatter says about it.
type Skill struct {
	Name        string
	Description string
	// Dir is the skill's directory on this machine, and Root the folder it was found in.
	Dir  string
	Root string
	// Project is false for a skill from the human's home directory.
	Project bool
	// ModelInvocable is false when only the human may ask for it, UserInvocable false when only the model may.
	ModelInvocable bool
	UserInvocable  bool
}

// Root is one folder of skills, Project false when it is the human's own.
// Within, when set, is where every skill in it must resolve to.
type Root struct {
	Dir     string
	Project bool
	Within  string
}

// Roots are where skills are looked for, the first to name one winning: each
// directory from dir up to its repository root, then the human's own.
func Roots(dir, home string) []Root {
	var out []Root
	dirs := upToRoot(dir)
	// A repository's skill may link only within it, never out to the rest of this machine.
	repo := dirs[len(dirs)-1]
	for _, d := range dirs {
		out = append(out, Root{Dir: filepath.Join(d, ".agents", "skills"), Project: true, Within: repo},
			Root{Dir: filepath.Join(d, ".claude", "skills"), Project: true, Within: repo})
	}
	if home != "" {
		out = append(out, Root{Dir: filepath.Join(home, ".agents", "skills")},
			Root{Dir: filepath.Join(home, ".claude", "skills")})
	}
	return out
}

// Find reads every skill under roots, sorted by name. One with something
// wrong loads with a warning, and only one that cannot load is left out.
func Find(roots []Root) ([]Skill, []string) {
	var found []Skill
	var warnings []string
	seen := map[string]string{}
	for _, root := range roots {
		entries, err := os.ReadDir(root.Dir)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				warnings = append(warnings, fmt.Sprintf("skills: %s: %v", root.Dir, err))
			}
			continue
		}
		for _, e := range entries {
			dir := filepath.Join(root.Dir, e.Name())
			if !isDir(dir) {
				continue
			}
			if root.Within != "" && (!inside(dir, root.Within) || !inside(filepath.Join(dir, "SKILL.md"), root.Within)) {
				warnings = append(warnings, fmt.Sprintf("skills: %s links outside the repository, so it was skipped", dir))
				continue
			}
			s, warn, ok := load(dir)
			warnings = append(warnings, warn...)
			if !ok {
				continue
			}
			if winner, dup := seen[s.Name]; dup {
				warnings = append(warnings, fmt.Sprintf("skills: %s is shadowed by %s, which has the same name", dir, winner))
				continue
			}
			seen[s.Name] = dir
			s.Root, s.Project = root.Dir, root.Project
			found = append(found, s)
		}
	}
	slices.SortFunc(found, func(a, b Skill) int { return strings.Compare(a.Name, b.Name) })
	return found, warnings
}

// frontmatter is what detent reads of SKILL.md's header. allowed-tools is
// deliberately absent: nothing in detent pre-approves a tool call.
type frontmatter struct {
	Name                   string `yaml:"name"`
	Description            string `yaml:"description"`
	DisableModelInvocation bool   `yaml:"disable-model-invocation"`
	UserInvocable          *bool  `yaml:"user-invocable"`
}

// load reads dir/SKILL.md. ok is false only with no SKILL.md, no description,
// or a header that will not parse even leniently.
func load(dir string) (Skill, []string, bool) {
	path := filepath.Join(dir, "SKILL.md")
	text, err := readHead(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Skill{}, nil, false
	}
	if err != nil {
		return Skill{}, []string{fmt.Sprintf("skills: %s: %v", path, err)}, false
	}
	head, ok := header(text)
	if !ok {
		return Skill{}, []string{fmt.Sprintf("skills: %s has no frontmatter, so it was skipped", path)}, false
	}
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(head), &fm); err != nil {
		// The commonest slip: a description with a colon in it, unquoted.
		if yaml.Unmarshal([]byte(quoteValues(head)), &fm) != nil {
			return Skill{}, []string{fmt.Sprintf("skills: %s: %v", path, err)}, false
		}
	}
	if strings.TrimSpace(fm.Description) == "" {
		return Skill{}, []string{fmt.Sprintf("skills: %s has no description, so it was skipped", path)}, false
	}

	var warnings []string
	base := filepath.Base(dir)
	// Folded like the description, since a newline in a name would split its catalog line.
	name := strings.Join(strings.Fields(fm.Name), " ")
	switch {
	case name == "":
		name = base
		warnings = append(warnings, fmt.Sprintf("skills: %s has no name, so it is called %s", path, base))
	case name != base:
		warnings = append(warnings, fmt.Sprintf("skills: %s is named %s but lives in %s", path, name, base))
	}
	if !validName.MatchString(name) || len(name) > 64 {
		warnings = append(warnings, fmt.Sprintf("skills: %q is not a valid skill name, loading it anyway", name))
	}
	return Skill{
		Name: name, Description: strings.Join(strings.Fields(fm.Description), " "), Dir: dir,
		ModelInvocable: !fm.DisableModelInvocation,
		UserInvocable:  fm.UserInvocable == nil || *fm.UserInvocable,
	}, warnings, true
}

// readHead reads the start of a file, enough for any sane header.
func readHead(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxHead))
	return string(b), err
}

// header is the YAML between a leading --- line and the next one.
func header(text string) (string, bool) {
	text = strings.TrimPrefix(text, "\ufeff")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[1:i], "\n"), true
		}
	}
	return "", false
}

// quoteValues quotes each top-level "key: value" whose value is plain text.
// Go's %q escapes are all valid in a YAML double-quoted scalar.
func quoteValues(head string) string {
	lines := strings.Split(head, "\n")
	for i, l := range lines {
		k, v, ok := strings.Cut(l, ": ")
		if !ok || strings.HasPrefix(l, " ") || strings.ContainsAny(k, " \t") {
			continue
		}
		v = strings.TrimSpace(v)
		if v == "" || strings.ContainsAny(v[:1], `"'|>[{`) {
			continue
		}
		lines[i] = k + ": " + fmt.Sprintf("%q", v)
	}
	return strings.Join(lines, "\n")
}

// upToRoot is dir and each parent up to the one holding .git, nearest
// first, or dir alone outside a repository.
func upToRoot(dir string) []string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	var dirs []string
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		dirs = append(dirs, d)
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return dirs
		}
		if filepath.Dir(d) == d {
			return dirs[:1]
		}
	}
}

// inside reports whether p, links followed, is dir or under it.
func inside(p, dir string) bool {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		// Missing is load's to report, and nothing outside was reached.
		return errors.Is(err, fs.ErrNotExist)
	}
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	rel, err := filepath.Rel(dir, resolved)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
