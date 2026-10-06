package tool

import "github.com/vitzeno/detent/event"

// SpawnAgent hands a task to a subagent: another model with a transcript of
// its own and every tool but this one, which replies with a report. It lowers
// to nothing a shell could run, since running one is running the engine.
type SpawnAgent struct{}

func (SpawnAgent) Name() string { return event.ToolSpawnAgent }

func (SpawnAgent) Describe() Spec {
	return Spec{
		Description: "Start a subagent: another model that works one task on its own and replies with a report. " +
			"It does not see this conversation, so the task must say everything it needs: what to do, " +
			"where to start and what to report. It has your tools but this one, so it can read, run " +
			"commands and edit files. It cannot ask you questions. Several spawn_agent calls in one step " +
			"run at the same time in your working directory, so start every one you need together and give " +
			"each its own files to change: never two the same files, installs or builds. A subagent stops " +
			"after 30 steps or when its time runs out, and reports what it did so far. Its report is all " +
			"you see of its work. It ends with the files the subagent read, listed from what actually ran, " +
			"and its paths and line numbers come from those, so quote them rather than reading the files " +
			"again. Do not use it for what one or two calls answer.",
		Params: []Param{
			{Name: "task", Type: TypeString, Required: true,
				Desc: "the whole task, with everything the subagent needs to know"},
			{Name: "name", Type: TypeString,
				Desc: "a short label for the subagent, a word or two such as explore-auth"},
		},
		Mutability: event.MutRead,
		Delegates:  true,
		// A report is markdown: the child is asked for headings, lists and file links.
		Renders: event.RendersMarkdown,
		Group:   "agents",
	}
}

// Lower is what a human reads before approving, should anything flag it.
func (SpawnAgent) Lower(a Args) (string, error) { return event.Command(event.ToolSpawnAgent, a), nil }
