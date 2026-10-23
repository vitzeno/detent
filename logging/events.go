package logging

// Names for records the bus never carries. A fact is logged under its
// own event.Kind, so there is one vocabulary rather than two.
const (
	SessionOpen = "session.open"

	// A model was asked something outside a Turn.
	LLMRequest = "llm.request"
	LLMReply   = "llm.reply"
	LLMError   = "llm.error"
)

// Field names records carry. Spelled once so queries can rely on them.
const (
	KeyComponent   = "component"
	KeySession     = "session"
	KeyTurn        = "turn"
	KeyStep        = "step"
	KeyToolCall    = "tool_call"
	KeyAgent       = "agent"
	KeyUserCommand = "user_command"
	KeyEvent       = "event"
	KeyReason      = "reason"
	KeyMS          = "ms"
	KeyOrdinal     = "ordinal"
)

// Components worth filtering to on their own.
const (
	UI      = "ui"
	Engine  = "engine"
	LLM     = "llm"
	Judge   = "judge"
	Host    = "host"
	Sandbox = "sandbox"
	Store   = "store"
	// Bus is every fact Watch writes, whoever published it.
	Bus = "bus"
)
