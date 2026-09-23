package logging

// Names for records the bus never carries. Everything published as a
// fact is logged under its own event.Kind instead, so there is one
// vocabulary rather than two kept in step by hand.
const (
	SessionOpen = "session.open"

	// A model was asked something outside a Turn.
	LLMRequest = "llm.request"
	LLMReply   = "llm.reply"
	LLMError   = "llm.error"

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
	KeyTurn      = "turn"
	KeyStep      = "step"
	KeyCall      = "call"
	KeyEvent     = "event"
	KeyReason    = "reason"
	KeyMS        = "ms"
	KeyOrdinal   = "ordinal"
)

// Components worth filtering to on their own.
const (
	UI      = "ui"
	Engine  = "engine"
	LLM     = "llm"
	Judge   = "judge"
	Host    = "host"
	Sandbox = "sandbox"
	Viewgen = "viewgen"
)
