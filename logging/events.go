package logging

// The event vocabulary. An event is a record's primary key, so it is
// a closed set rather than free text; msg is decoration. Names are
// dotted so a subsystem filters by prefix.
const (
	// Goal lifecycle.
	GoalBegin  = "goal.begin"
	GoalEnd    = "goal.end"
	ProbeRun   = "probe.run"
	Compaction = "transcript.compact"

	// A model was asked something.
	LLMRequest = "llm.request"
	LLMReply   = "llm.reply"
	LLMError   = "llm.error"

	// Jev classified something.
	JudgePre  = "judge.pre"
	JudgePost = "judge.post"

	// What a command did.
	CmdPropose = "cmd.propose"
	CmdConfirm = "cmd.confirm"
	CmdRun     = "cmd.run"
	CmdDone    = "cmd.done"

	// Checkpoints and undo.
	Snapshot = "snapshot.take"
	Rollback = "snapshot.rollback"

	// How the output pane got drawn. Every silent branch says which,
	// because "it looked plain" is otherwise unanswerable.
	ViewLookup   = "view.lookup"
	ViewSkipped  = "view.skipped"
	ViewInvalid  = "view.invalid"
	ViewFit      = "view.fit"
	ViewAccepted = "view.accepted"
	ViewDeclined = "view.declined"
	ViewDrawn    = "view.drawn"
)

// Field names records carry. Spelled once so queries can rely on them.
const (
	KeyComponent = "component"
	KeySession   = "session"
	KeyGoal      = "goal"
	KeyStep      = "step"
	KeyEvent     = "event"
	KeyReason    = "reason"
	KeyMS        = "ms"
)

// Components worth filtering to on their own.
const (
	UI       = "ui"
	Agent    = "agent"
	LLM      = "llm"
	Judge    = "judge"
	Host     = "host"
	Sandbox  = "sandbox"
	Viewgen  = "viewgen"
	Resolver = "resolver"
)
