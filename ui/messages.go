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

// viewMsg carries a generated view back from the Driver. It arrives
// after the row is already drawn: the render_kind fallback renders at
// once and this upgrades it, so a slow or dead endpoint costs the
// upgrade and nothing else.
type viewMsg struct {
	row  *stepRow
	view GeneratedView
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
	// revertedFiles records whether the human's own files went back
	// too, so the notice can say which of the two happened.
	revertedFiles bool
}

type welcomeTickMsg struct{}
