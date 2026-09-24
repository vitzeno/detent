package engine

import "github.com/google/uuid"

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

func WithSessionID(id uuid.UUID) Option {
	return func(e *Engine) {
		if id != uuid.Nil {
			e.session = id
		}
	}
}
