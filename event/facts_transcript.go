package event

import "github.com/google/uuid"

// Appended is what went into the transcript, verbatim. Rebuilding it
// from CallEnded would mean reproducing formatResult forever.
type Appended struct {
	fact
	Turn     uuid.UUID `json:"Turn"`
	Step     uuid.UUID `json:"Step"`
	Messages []Message `json:"Messages"`
}

func (Appended) Kind() Kind { return AppendedKind }

// Compacted says the front of the transcript was replaced by one note.
// Dropped counts the messages that went.
type Compacted struct {
	fact
	Turn    uuid.UUID `json:"Turn"`
	Dropped int       `json:"Dropped"`
	Note    string    `json:"Note"`
}

func (Compacted) Kind() Kind { return CompactedKind }

// ContextMeasured is what the model's request is made of: what every Step
// resends, and the history compaction folds, oldest first.
type ContextMeasured struct {
	fact
	// Budget is where history compacts. Total is the endpoint's own count
	// when Exact, and otherwise an estimate scaled by the last one.
	Budget  int           `json:"Budget"`
	Total   int           `json:"Total"`
	Exact   bool          `json:"Exact"`
	Fixed   []ContextPart `json:"Fixed"`
	History []ContextPart `json:"History"`
	// Growth is how much history an average recent Step added.
	Growth int `json:"Growth"`
}

func (ContextMeasured) Kind() Kind { return MeasuredKind }

// ContextPart is one slice of a request, in tokens.
type ContextPart struct {
	Name   string `json:"Name"`
	Detail string `json:"Detail"`
	Tokens int    `json:"Tokens"`
	// N is the request a history part belongs to, 0 for the summary or notes.
	N int `json:"N"`
	// Open is the request still running, which compaction will not touch.
	Open bool `json:"Open"`
	// Largest is a history part's biggest message, a command that printed it or a reply.
	Largest       string `json:"Largest"`
	LargestTokens int    `json:"LargestTokens"`
}
