package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/tool"
)

// A review is a Turn the human starts and no request: a reviewer agent reads the
// diff the human sees and comments on it, and nothing reaches the root's transcript.

// reviewerName is the author the reviewer's comments carry.
const reviewerName = "reviewer"

// reviewRun is the review a reviewer works for, and the diff it answers from.
type reviewRun struct {
	v       event.ReviewChanges
	diff    tool.ReviewDiff
	comment tool.ReviewComment
}

// startReview runs a reviewer on v's diff. A prompt sent meanwhile waits in the
// Turn's inbox and is a request of its own once the review ends.
func (e *Engine) startReview(ctx context.Context, v event.ReviewChanges, done chan struct{}) {
	switch {
	case e.reviewer == nil:
		e.notice("warn", "a reviewer needs subagents on: set subagents: true in your config")
		return
	case len(v.Files) == 0:
		e.notice("info", "nothing to review: these changes are empty")
		return
	}
	tctx, cancel := context.WithCancel(ctx)
	// Neither counted nor kept among past Turns, since it is no request to undo.
	t := newTurnState(0, reviewLabel(v), cancel)
	t.review = v.Review
	e.mu.Lock()
	e.cur = t
	e.mu.Unlock()
	go func() {
		defer cancel()
		defer func() { done <- struct{}{} }()
		e.runReview(tctx, t, v)
	}()
}

// runReview is the reviewer's whole run. Its report is the review's summary,
// a comment on no line.
func (e *Engine) runReview(ctx context.Context, t *turnState, v event.ReviewChanges) {
	e.bus.Publish(event.TurnStarted{Turn: t.id, Prompt: t.prompt, Review: v.Review, Files: len(v.Files)})
	tools := e.root.tools.Only(tool.ReviewReads...)
	run := &reviewRun{v: v, diff: tool.NewReviewDiff(v.Files), comment: tool.NewReviewComment(v.Files)}
	for _, rt := range []tool.Tool{run.diff, run.comment} {
		if err := tools.Register(rt); err != nil {
			e.notice("error", "could not start the reviewer: "+err.Error())
			e.endReview(t, event.EndError, err.Error())
			return
		}
	}
	reviewer := newAgent(uuid.Must(uuid.NewV7()), reviewerName, e.reviewer, tools, e.extra...)
	reviewer.review = run

	cctx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	e.track(reviewer.id, stop)
	defer e.untrack(reviewer.id)
	e.bus.Publish(event.AgentStarted{Agent: reviewer.id, Turn: t.id, Name: reviewer.name, Task: t.prompt})

	end := e.runChild(cctx, t, reviewer, reviewTask(v))
	reason, why, report := e.report(ctx, cctx, t, reviewer, &end)
	e.bus.Publish(event.AgentEnded{Agent: reviewer.id, Reason: reason, Why: why, Usage: end.used})
	// Its report is read by the human, not a parent model, so a stop is said plainly.
	switch {
	case report == stoppedUnstarted:
		report = "Stopped before it started."
	case reason == event.AgentStopped:
		report = "Stopped before it finished. " + strings.TrimPrefix(report, stoppedPrefix)
	}
	if reason != event.AgentAborted && strings.TrimSpace(report) != "" {
		e.bus.Publish(run.commented(event.ReviewComment{ID: uuid.Must(uuid.NewV7()), Author: reviewerName,
			Body: strings.TrimSpace(report)}))
	}
	e.endReview(t, end.reason, why)
}

// endReview ends a review's Turn. It never settles or takes notes, which are the
// root's: a prompt sent during it is still in the inbox, for turnDone.
func (e *Engine) endReview(t *turnState, why event.EndReason, summary string) {
	t.ended.Store(true)
	e.bus.Publish(event.TurnEnded{Turn: t.id, Reason: why, Summary: summary, Usage: t.usage()})
}

// answer runs a call the engine answers from what it holds: a review tool,
// which only a reviewer has.
func (e *Engine) answer(a *agent, p *toolCallPlan) {
	p.ended = true
	e.bus.Publish(event.ToolCallStarted{ToolCall: p.id, Runner: "review"})
	start := time.Now()
	var out event.Result
	if a.review == nil {
		out.Err = string(p.prepared.Tool) + " only answers during a review"
	} else if text, err := a.review.answer(e, p.prepared); err != nil {
		out.Err = err.Error()
	} else {
		out.Stdout = text
	}
	e.bus.Publish(event.ToolCallEnded{ToolCall: p.id, Result: out, Took: time.Since(start)})
	p.finish(formatResult(event.Command(p.prepared.Tool, p.prepared.Args), out))
}

// answer is a review tool's reply. A comment that passes its check is recorded
// at once, so the human sees it while the reviewer works.
func (r *reviewRun) answer(e *Engine, c tool.Call) (string, error) {
	switch c.Tool {
	case event.ToolReviewDiff:
		return r.diff.Answer(c.Args)
	case event.ToolReviewComment:
		comment, err := r.comment.Check(c.Args)
		if err != nil {
			return "", err
		}
		comment.ID, comment.Author = uuid.Must(uuid.NewV7()), reviewerName
		e.bus.Publish(r.commented(comment))
		return fmt.Sprintf("Commented on %s:%d.", comment.Path, comment.Start), nil
	default:
		return "", fmt.Errorf("%s is not a review tool", c.Tool)
	}
}

func (r *reviewRun) commented(c event.ReviewComment) event.ReviewCommented {
	v := r.v
	return event.ReviewCommented{Review: v.Review, Reviewed: v.Reviewed, Base: v.Base, Head: v.Head,
		Scope: v.Scope, Against: v.Against, Op: event.CommentAdded, Comment: c}
}

// reviewLabel is what the review's Turn is called.
func reviewLabel(v event.ReviewChanges) string {
	switch v.Scope {
	case event.ScopeSession:
		return "review of the session"
	case event.ScopeSince:
		return fmt.Sprintf("review of your edits since request %d", v.Request)
	case event.ScopeBranch:
		return "review of this branch against " + v.Against
	case event.ScopeRequest:
	}
	return fmt.Sprintf("review of request %d", v.Request)
}

// reviewTask is what the reviewer is first told: whose changes these are, what
// they were for, and which files changed, read one at a time with review_diff.
func reviewTask(v event.ReviewChanges) string {
	var b strings.Builder
	switch v.Scope {
	case event.ScopeSession:
		b.WriteString("Review every change an agent made this session.\n")
	case event.ScopeSince:
		b.WriteString("Review the changes the human made by hand since an agent's last request ended.\n")
	case event.ScopeBranch:
		b.WriteString("Review this branch against " + v.Against + ", committed and not, the human's work and an agent's.\n")
	case event.ScopeRequest:
		b.WriteString("Review the changes an agent made for one request.\n")
	}
	if strings.TrimSpace(v.Asked) != "" {
		fmt.Fprintf(&b, "\nThey were made for this request:\n%s\n", strings.TrimSpace(v.Asked))
	}
	b.WriteString("\nThe changed files, with lines added and removed:\n")
	for _, f := range v.Files {
		fmt.Fprintf(&b, "  %s (%s%s)\n", f.Path, f.Change, fileSize(f))
	}
	b.WriteString("\nRead each that matters with review_diff, and comment with review_comment.")
	return b.String()
}

func fileSize(f event.FileDiff) string {
	switch {
	case f.Binary:
		return ", binary"
	case f.Cut:
		return ", too long to show"
	}
	var add, del int
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			switch l.Op {
			case event.LineAdded:
				add++
			case event.LineRemoved:
				del++
			case event.LineContext:
			}
		}
	}
	return fmt.Sprintf(", +%d -%d", add, del)
}
