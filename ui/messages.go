package ui

// What arrives back on the update loop. Every one of these is produced
// by a tea.Cmd in commands.go and consumed by a case in Model.route.

type beginGoalMsg struct {
	goal string
	res  *GoalResult
	err  error
}

type proposeMsg struct {
	proposal Proposal
	pre      PreJudgment
	used     Usage
	err      error
}

type execDoneMsg struct {
	ec  *ExecutedCommand
	err error
}

type judgeMsg struct {
	row  *stepRow
	post PostJudgment
}

type saveDoneMsg struct {
	row     *stepRow
	content string
	err     error
}

type rollbackDoneMsg struct {
	target *goalBlock
	local  int // step's position within target, what the harness takes
	step   int // the session-wide number the human typed
	ok     bool
	err    error
}

type welcomeTickMsg struct{}
