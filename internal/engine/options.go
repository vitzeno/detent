package engine

import (
	"time"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

// Option configures an Engine at New.
type Option func(*Engine)

// WithMaxSteps caps Steps per Turn. Soft: the bound asks rather than stops.
func WithMaxSteps(n int) Option {
	return func(e *Engine) {
		if n > 0 {
			e.maxSteps = n
		}
	}
}

// WithCallsPerStep caps how many Calls one Step may ask for.
func WithCallsPerStep(n int) Option {
	return func(e *Engine) {
		if n > 0 {
			e.maxCalls = n
		}
	}
}

// WithContextTokens sets the transcript budget, in tokens.
func WithContextTokens(n int) Option {
	return func(e *Engine) {
		if n > 0 {
			e.contextTokens = n
		}
	}
}

// WithSummarizer condenses the Steps compaction drops.
func WithSummarizer(s Summarizer) Option { return func(e *Engine) { e.summarizer = s } }

// WithJudge adds the classifier as the last hook in the chain, after
// the free ones.
func WithJudge(j Judge, threshold float64) Option {
	return func(e *Engine) {
		if j != nil {
			e.assessors = append(e.assessors, jevHook{judge: j, threshold: threshold})
		}
	}
}

// WithAssessor registers another hook. It can widen the verdict and
// never narrow it, whatever it returns.
func WithAssessor(a Assessor) Option {
	return func(e *Engine) {
		if a != nil {
			e.assessors = append(e.assessors, a)
		}
	}
}

// WithWorktree checkpoints the human's own files, which the container
// snapshot never covers.
func WithWorktree(w Worktreer) Option { return func(e *Engine) { e.worktreer = w } }

// WithInvoker wires what answers a Call with no command. Without one
// those Calls come back saying so, rather than running.
func WithInvoker(in Invoker) Option { return func(e *Engine) { e.invoker = in } }

// WithDescription is what the session says about itself at startup,
// for the fact published by Run.
func WithDescription(model, judge string, network, recorded bool) Option {
	return func(e *Engine) {
		e.modelName, e.judgeName = model, judge
		e.network, e.recorded = network, recorded
	}
}

// WithInstructions names the instruction files the model's prompt carries.
func WithInstructions(paths []string) Option {
	return func(e *Engine) { e.instructions = paths }
}

// WithSkills names the skills found at startup, for SessionStarted.
func WithSkills(s []event.SkillSummary) Option {
	return func(e *Engine) { e.skills = s }
}

// WithSessionID sets the session id, as a resume does.
func WithSessionID(id uuid.UUID) Option {
	return func(e *Engine) {
		if id != uuid.Nil {
			e.session = id
		}
	}
}

// WithStopGrace bounds how long a cancelled Run waits for the running
// Turn to publish what it owes.
func WithStopGrace(d time.Duration) Option {
	return func(e *Engine) {
		if d > 0 {
			e.stopGrace = d
		}
	}
}
