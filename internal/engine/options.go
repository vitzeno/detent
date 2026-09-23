package engine

import "github.com/vitzeno/detent/event"

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

// WithParallelCalls caps read-only Calls running together.
func WithParallelCalls(n int) Option {
	return func(e *Engine) {
		if n > 0 {
			e.parallel = n
		}
	}
}

func WithContextTokens(n int) Option {
	return func(e *Engine) {
		if n > 0 {
			e.contextTokens = n
		}
	}
}

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

func WithSessionID(id event.ID) Option {
	return func(e *Engine) {
		if id != "" {
			e.session = id
		}
	}
}
