package event

import (
	"time"

	"github.com/google/uuid"
)

// A Session is the process lifetime and its one message log.

// SessionStarted is published once. Welcome, status and the log all
// read it, so it is the one description of a run.
type SessionStarted struct {
	fact
	Session  uuid.UUID `json:"Session"`
	Model    string    `json:"Model"`
	Judge    string    `json:"Judge"` // "" when no classifier is wired
	Sandbox  bool      `json:"Sandbox"`
	Network  bool      `json:"Network"`
	MaxSteps int       `json:"MaxSteps"`
	// Recorded is false when nothing is writing this session down, so
	// a front-end can say it will not be resumable.
	Recorded bool `json:"Recorded"`
	// Resumed is how many stored records this run began from.
	Resumed int `json:"Resumed"`
	// ContextTokens is the transcript budget, which a Step's
	// PromptTokens is measured against. Zero means nobody said.
	ContextTokens int `json:"ContextTokens"`
	// Instructions are the project files the system prompt carries.
	Instructions []string `json:"Instructions"`
	// Skills are what the skill tool can load and the human can ask for.
	Skills []SkillSummary `json:"Skills"`
}

func (SessionStarted) Kind() Kind { return SessionStartedKind }

// SkillSummary is one skill found at startup.
type SkillSummary struct {
	Name        string `json:"Name"`
	Description string `json:"Description"`
	// Project is false for one from the human's home directory, and Builtin
	// true for one detent ships.
	Project bool `json:"Project"`
	Builtin bool `json:"Builtin"`
	// UserInvocable is false for a skill only the model may load.
	UserInvocable bool `json:"UserInvocable"`
}

// SessionResumed marks where a stored session was picked up again.
// Not a count on SessionStarted: two resumes are two seams.
type SessionResumed struct {
	fact
	Session uuid.UUID `json:"Session"`
	// Records is how many were replayed to get here.
	Records int `json:"Records"`
	// Sandbox is this run's mode, carried so an old seam still says it
	// once later runs have published their own.
	Sandbox bool `json:"Sandbox"`
}

func (SessionResumed) Kind() Kind { return SessionResumedKind }

// SessionReset says the transcript was cleared, so a replay clears it too.
type SessionReset struct{ fact }

func (SessionReset) Kind() Kind { return SessionResetKind }

// SessionsListed answers ListSessions with what can be resumed.
type SessionsListed struct {
	fact
	Sessions []SessionSummary `json:"Sessions"`
}

func (SessionsListed) Kind() Kind { return SessionsListedKind }

// SessionSummary is one resumable session, as a listing shows it.
// Started is UTC, since a time crossing the wire keeps only its instant.
type SessionSummary struct {
	ID uuid.UUID `json:"ID"`
	// Name is what a human called it, "" until they do.
	Name    string    `json:"Name"`
	Started time.Time `json:"Started"`
	Model   string    `json:"Model"`
	Events  int       `json:"Events"`
}

// Notice is anything to say that is not about one tool call.
type Notice struct {
	fact
	Level string `json:"Level"` // info, warn, error
	Text  string `json:"Text"`
}

func (Notice) Kind() Kind { return NoticeKind }
