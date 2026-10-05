package model

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/vitzeno/detent/internal/usercommand"
)

// The project's instructions come after the built-in rules, so those stay first.
func TestSystemPrompt_EndsWithTheProjectsInstructions(t *testing.T) {
	c := NewClient("", "", "", WithInstructions("<instructions path=\"AGENTS.md\">\nuse tabs\n</instructions>", nil))
	got := c.systemPrompt()
	assert.True(t, strings.HasSuffix(got, "use tabs\n</instructions>"))
	assert.Less(t, strings.Index(got, "Work the request"), strings.Index(got, "use tabs"))
	assert.Equal(t, systemPrompt(LocalEnvironment()), NewClient("", "", "").systemPrompt(),
		"no instructions adds nothing")
}

// /context labels the prompt from these parts, so a piece added to the
// prompt but not listed here would be counted under nothing.
func TestPromptParts_AddUpToTheWholePrompt(t *testing.T) {
	for name, c := range map[string]*Client{
		"bare":              NewClient("", "", ""),
		"with instructions": NewClient("", "", "", WithInstructions("<instructions>x</instructions>", []string{"AGENTS.md"})),
		"set by the caller": NewClient("", "", "", WithSystemPrompt("you are a test")),
		"with subagents":    NewClient("", "", "", WithSubagents()),
		"a subagent": NewClient("", "", "", WithRole(RoleChild),
			WithInstructions("<instructions>x</instructions>", []string{"AGENTS.md"})),
	} {
		n := 0
		for _, p := range c.PromptParts() {
			n += p.Bytes
		}
		assert.Equal(t, len(c.systemPrompt()), n, name)
	}
}

// The rule to delegate is there only when there is something to delegate to.
func TestSystemPrompt_MentionsSubagentsOnlyWhenTheyExist(t *testing.T) {
	assert.NotContains(t, NewClient("", "", "").systemPrompt(), "spawn_agent")
	assert.Contains(t, NewClient("", "", "", WithSubagents()).systemPrompt(), "Hand broad reading to spawn_agent")
}

// A subagent has its own role, still told where it runs and still given the
// project's instructions, and never the root's rules.
func TestSystemPrompt_AChildHasItsOwnRoleAndTheProjectsInstructions(t *testing.T) {
	c := NewClient("", "", "", WithRole(RoleChild), WithSubagents(),
		WithInstructions("<instructions>use tabs</instructions>", []string{"AGENTS.md"}))
	got := c.systemPrompt()
	assert.Contains(t, got, "You are a subagent")
	assert.Contains(t, got, "Environment:")
	assert.Contains(t, got, "use tabs")
	assert.NotContains(t, got, "Work the request")
	assert.NotContains(t, got, "spawn_agent", "a child cannot spawn")
	assert.Equal(t, "subagent", c.PromptParts()[0].Name)
}

func TestBrief_WritesADurationAsAPersonWould(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Minute: "10m", 90 * time.Second: "1m30s", time.Hour: "1h",
		90 * time.Minute: "1h30m", 50 * time.Millisecond: "50ms",
	} {
		assert.Equal(t, want, Brief(d))
	}
}

func TestSystemPrompt_StatesTheCommandLimit(t *testing.T) {
	env := LocalEnvironment()
	assert.NotContains(t, systemPrompt(env), "is stopped")
	env.Timeout = 10 * time.Minute
	assert.Contains(t, systemPrompt(env), "A command still running after 10m is stopped.")
}

// The prompt promises a shape another package emits, so it is checked
// against that package's constant: prose drifting from it fails silently.
func TestSystemPrompt_NamesTheMarkerUsercommandActuallyWrites(t *testing.T) {
	assert.Contains(t, systemPrompt(LocalEnvironment()), usercommand.Marker)
	assert.Contains(t, systemPrompt(LocalEnvironment()), "read it rather than running it again",
		"and says what to do with it, which is the point of naming it")
}

// Point 4 says earlier steps are still on disk, which a resume makes
// false, so its exception must name the marker the note opens with.
func TestSystemPrompt_NamesTheResumeMarker(t *testing.T) {
	got := systemPrompt(LocalEnvironment())
	assert.Contains(t, got, ResumeMarker)
	assert.Contains(t, got, "is the exception, and says what survived")
}

func TestEnvironment_SaysWhatChanges(t *testing.T) {
	tests := []struct {
		name  string
		env   Environment
		wants []string
		nots  []string
	}{
		{
			name:  "unsandboxed says the files are real",
			env:   Environment{OS: "darwin", Arch: "arm64", Dir: "/x", Network: true},
			wants: []string{"own machine", "network is reachable"},
			nots:  []string{"container", "undo"},
		},
		{
			name:  "no network says so plainly",
			env:   Environment{OS: "linux", Arch: "amd64", Dir: "/x"},
			wants: []string{"no network", "Work with what is already here"},
			nots:  []string{"network is reachable"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := systemPrompt(tt.env)
			for _, w := range tt.wants {
				assert.Contains(t, got, w)
			}
			for _, n := range tt.nots {
				assert.NotContains(t, got, n)
			}
		})
	}
}

func TestSystemPrompt_SpeaksTheShellsDialect(t *testing.T) {
	sh := systemPrompt(Environment{OS: "linux", Arch: "amd64", Dir: "/x"})
	assert.Equal(t, sh, systemPrompt(Environment{OS: "linux", Arch: "amd64", Dir: "/x", Shell: ShellSh}),
		"sh named or not is the same prompt")
	assert.Contains(t, sh, "Each command runs through a fresh `sh -c` starting in that directory. ")
	assert.Contains(t, sh, "9. Prefer a specific tool over bash when one fits.")
	assert.Contains(t, sh, "Never through bash with echo, printf, cat, tee, sed -i or a > redirect")

	ps := systemPrompt(Environment{OS: "windows", Arch: "amd64", Dir: `C:\x`, Shell: ShellPwsh})
	assert.Contains(t, ps, "PowerShell 7")
	assert.Contains(t, ps, "9. Prefer a specific tool over powershell when one fits.")
	assert.Contains(t, ps, "Never through powershell with Set-Content")
	assert.NotContains(t, ps, "bash", "every mention of bash is replaced, or the model calls a tool it has not got")
	assert.NotContains(t, ps, "sh -c")

	gb := systemPrompt(Environment{OS: "windows", Arch: "amd64", Dir: `C:\x`, Shell: ShellGitBash})
	assert.Contains(t, gb, "Git Bash")
	assert.Contains(t, gb, "over bash when", "Git Bash keeps the bash tool")
}

func TestLocalEnvironment_DescribesThisMachine(t *testing.T) {
	env := LocalEnvironment()
	assert.NotEmpty(t, env.OS)
	assert.NotEmpty(t, env.Dir)
	assert.True(t, env.Network)
	assert.False(t, env.Undoable, "nothing checkpoints the user's own filesystem")
	assert.False(t, env.Sandboxed)
}
