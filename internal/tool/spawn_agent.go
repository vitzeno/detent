package tool

import "github.com/vitzeno/detent/event"

// SpawnAgentName is the tool a model delegates with, which the engine runs.
const SpawnAgentName = "spawn_agent"

// SpawnAgent hands a task to a subagent: another model with a transcript of
// its own, which only reads and replies with a report. It lowers to nothing
// a shell could run, since running one is running the engine.
type SpawnAgent struct{}

func (SpawnAgent) Name() string { return SpawnAgentName }

func (SpawnAgent) Describe() Spec {
	return Spec{
		Description: "Start a subagent: another model that works one task on its own and replies with a report. " +
			"It does not see this conversation, so the task must say everything it needs: what to find, " +
			"where to start and what to report. It can read and search files, list directories, search the " +
			"web and load skills. It cannot change anything or ask you questions. Several spawn_agent calls " +
			"in one step run at the same time, so start every one you need together. A subagent stops after " +
			"30 steps or when its time runs out, and reports what it found so far. Its report is all you see " +
			"of its work, and its paths and line numbers come from the files it read, so quote them rather " +
			"than reading those files again. Do not use it for what one or two reads answer.",
		Params: []Param{
			{Name: "task", Type: TypeString, Required: true,
				Desc: "the whole task, with everything the subagent needs to know"},
			{Name: "name", Type: TypeString,
				Desc: "a short label for the subagent, a word or two such as explore-auth"},
		},
		Mutability: event.MutRead,
		Delegates:  true,
		Group:      "agents",
	}
}

// Lower is what a human reads before approving, should anything flag it.
func (SpawnAgent) Lower(a Args) (string, error) { return event.Command(SpawnAgentName, a), nil }
