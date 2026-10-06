package engine

import (
	"cmp"
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

// WithToolCallsPerStep caps how many tool calls one Step may ask for.
func WithToolCallsPerStep(n int) Option {
	return func(e *Engine) {
		if n > 0 {
			e.maxToolCalls = n
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
// every other, whatever order the options came in.
func WithJudge(j Judge, threshold float64) Option {
	return func(e *Engine) {
		if j != nil {
			e.judge = &jevHook{judge: j, threshold: threshold}
		}
	}
}

// WithAssessor registers another hook. It can widen the verdict and
// never narrow it, whatever it returns.
func WithAssessor(a Assessor) Option {
	return func(e *Engine) {
		if a != nil {
			e.extra = append(e.extra, a)
		}
	}
}

// WithWorktree checkpoints the human's own files, which the container
// snapshot never covers.
func WithWorktree(w Worktreer) Option { return func(e *Engine) { e.worktreer = w } }

// WithSessions lets ResumeSession continue a stored session, telling the
// model what did not come back with note. Without it a resume is refused.
func WithSessions(log SessionLog, note func([]event.Record) string) Option {
	return func(e *Engine) { e.sessions, e.resumeNote = log, note }
}

// WithInvoker wires what answers a tool call with no command. Without one
// those tool calls come back saying so, rather than running.
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

// WithChildModel runs subagents with m, whose prompt is a subagent's. Wire it
// exactly when spawn_agent is registered, or the tool answers that it is not.
func WithChildModel(m Completer) Option { return func(e *Engine) { e.childModel = m } }

// WithAgentLimits bounds subagents: how many a Turn may start, how far one's
// transcript may grow and how long one may run. Zero keeps a default.
func WithAgentLimits(perTurn, contextTokens int, timeout time.Duration) Option {
	return func(e *Engine) {
		e.maxAgents = cmp.Or(perTurn, e.maxAgents)
		e.childContext = cmp.Or(contextTokens, e.childContext)
		e.childTimeout = cmp.Or(timeout, e.childTimeout)
	}
}

// WithReviewer is the model a review's reviewer agent speaks through.
func WithReviewer(m Completer) Option { return func(e *Engine) { e.reviewer = m } }

// WithCommit names the commit the work tree was on, for SessionStarted.
func WithCommit(sha string) Option { return func(e *Engine) { e.commit = sha } }

// WithCommandTimeout bounds each tool call. Zero keeps the default.
func WithCommandTimeout(d time.Duration) Option {
	return func(e *Engine) {
		if d > 0 {
			e.commandTimeout = d
		}
	}
}

// WithFinishCheck turns the check before a changing Turn ends on or off. On by default.
func WithFinishCheck(on bool) Option {
	return func(e *Engine) { e.finishCheck = on }
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
