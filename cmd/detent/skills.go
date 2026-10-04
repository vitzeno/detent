package main

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/skills"
	"github.com/vitzeno/detent/internal/tool"
)

// skillMounts is where skills outside the working directory appear in the sandbox.
const skillMounts = "/opt/detent/skills"

// foundSkills is every skill, where each appears to the runner, and what
// the sandbox must mount read-only so it appears there at all.
type foundSkills struct {
	entries   []tool.SkillEntry
	summaries []event.SkillSummary
	mounts    map[string]string
	warnings  []string
}

// findSkills reads the skills from cwd up, from home, then the ones detent ships.
// In the sandbox one under cwd is under mountPoint, any other under a read-only mount.
func findSkills(cwd, home, mountPoint string, sandboxed bool) foundSkills {
	roots := skills.Roots(cwd, home)
	var warnings []string
	if home != "" {
		root, err := skills.Builtin(skills.BuiltinDir(home))
		if err != nil {
			warnings = append(warnings, "skills: the built-in skills could not be written: "+err.Error())
		} else {
			roots = append(roots, root)
		}
	}
	found, more := skills.Find(roots)
	out := foundSkills{warnings: append(warnings, more...), mounts: map[string]string{}}
	for _, s := range found {
		dir := s.Dir
		if sandboxed {
			dir = sandboxDir(s, cwd, mountPoint, roots, out.mounts)
		}
		out.entries = append(out.entries, tool.SkillEntry{
			Name: s.Name, Description: s.Description, Dir: dir, Hidden: !s.ModelInvocable})
		out.summaries = append(out.summaries, event.SkillSummary{
			Name: s.Name, Description: s.Description, Project: s.Project, Builtin: s.Builtin,
			UserInvocable: s.UserInvocable})
	}
	return out
}

// skillTools is the skill tool when there is a skill to load, else nothing.
func (f foundSkills) skillTools() []tool.Tool {
	if len(f.entries) == 0 {
		return nil
	}
	return []tool.Tool{tool.NewSkill(f.entries)}
}

// sandboxDir is s.Dir as the container sees it, adding its folder to mounts when it is outside cwd.
func sandboxDir(s skills.Skill, cwd, mountPoint string, roots []skills.Root, mounts map[string]string) string {
	if rel, err := filepath.Rel(cwd, s.Dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path.Join(mountPoint, filepath.ToSlash(rel))
	}
	// Numbered by the root's place in the search, so the paths are stable.
	for i, r := range roots {
		if r.Dir == s.Root {
			dest := fmt.Sprintf("%s/%d", skillMounts, i)
			mounts[s.Root] = dest
			return dest + "/" + filepath.Base(s.Dir)
		}
	}
	return s.Dir
}
