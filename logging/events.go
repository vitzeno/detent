package logging

// The event vocabulary. An event name is the primary key of a log
// record: queries match on it, so it is a closed set rather than
// free text. The message field is decoration for humans.
//
// Names are dotted so a whole subsystem filters by prefix:
//
//	jq 'select(.event | startswith("view."))' session.jsonl
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

	// How the output pane got drawn. Every branch that silently
	// chooses something says which, because "it looked plain" is
	// otherwise unanswerable.
	ViewLookup   = "view.lookup"
	ViewSkipped  = "view.skipped"
	ViewInvalid  = "view.invalid"
	ViewFit      = "view.fit"
	ViewAccepted = "view.accepted"
	ViewDeclined = "view.declined"
	ViewDrawn    = "view.drawn"
)

// Field names every record may carry. Queries filter on these, so
// they are spelled once here rather than at each call site.
const (
	KeyComponent = "component"
	KeySession   = "session"
	KeyGoal      = "goal"
	KeyStep      = "step"
	KeyEvent     = "event"
	KeyReason    = "reason"
	KeyMS        = "ms"
)

// Component names. One per subsystem worth filtering to on its own.
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
