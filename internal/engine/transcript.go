package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/vitzeno/detent/internal/model"
)

// DefaultContextTokens caps the transcript, resent whole every Step.
// A ceiling to stay under, not what is billed.
const (
	DefaultContextTokens = 24_000
	BytesPerToken        = 4
	// MaxResultBytes bounds one result: a 200KB ps is twice the budget.
	MaxResultBytes = 4 * 1024
)

// transcript is the model's input. Its atom is a Step, which nothing
// may split, so every mutation lives here.
type transcript struct {
	msgs []model.Message
	// protect is where the open Turn began. Compaction never drops
	// from here on: the model cannot work a request it cannot see.
	protect int
	// dropped is how far compaction has shifted the front, so a mark
	// can count appends rather than positions that move under it.
	dropped int
}

// step appends one Step atomically: the assistant message, then one
// answer per call in order. Missing answers are filled rather than
// skipped, because a call nothing answers breaks the next Step.
func (t *transcript) step(reply model.Reply, answers map[string]string) {
	t.msgs = append(t.msgs, model.Message{
		Role: model.RoleAssistant, Content: reply.Text, Calls: reply.Calls,
	})
	for _, c := range reply.Calls {
		body, ok := answers[c.ID]
		if !ok {
			body = "This call did not run."
		}
		t.msgs = append(t.msgs, model.Answer(c, bound(body)))
	}
}

// say appends the model's prose for a Step that called nothing.
func (t *transcript) say(text string) {
	if strings.TrimSpace(text) != "" {
		t.msgs = append(t.msgs, model.Message{Role: model.RoleAssistant, Content: text})
	}
}

// user opens a Turn and protects everything from here on.
func (t *transcript) user(prompt string) {
	t.protect = len(t.msgs)
	t.msgs = append(t.msgs, model.Message{Role: model.RoleUser, Content: prompt})
}

// note is a NoteContext: a message with no tool run.
func (t *transcript) note(text string) {
	t.msgs = append(t.msgs, model.Message{Role: model.RoleUser, Content: text})
}

func (t *transcript) messages() []model.Message { return t.msgs }

// mark names a position that survives compaction. Counted in appends,
// so it stays valid however much the front is rewritten.
func (t *transcript) mark() int { return len(t.msgs) + t.dropped }

// truncate rewinds to a mark, taken at a Turn boundary so it cannot
// land inside a Step. A mark compaction has eaten is a no-op.
func (t *transcript) truncate(to int) {
	at := to - t.dropped
	if at < 0 || at > len(t.msgs) {
		return
	}
	t.msgs = t.msgs[:at]
	t.protect = min(t.protect, at)
}

func (t *transcript) reset() { t.msgs, t.protect, t.dropped = nil, 0, 0 }

func (t *transcript) bytes() int { return msgBytes(t.msgs) }

func msgBytes(msgs []model.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
		for _, c := range m.Calls {
			n += len(c.Name) + len(fmt.Sprint(c.Args))
		}
	}
	return n
}

// Summarizer condenses dropped Steps. Nil means they become a note
// saying they are gone.
type Summarizer interface {
	Summarize(ctx context.Context, msgs []model.Message) (string, error)
}

// compact drops whole Steps off the front until the transcript fits.
// Whole, because half a Step is a transcript no endpoint accepts.
func (t *transcript) compact(ctx context.Context, budgetTokens int, s Summarizer) bool {
	budget := budgetTokens * BytesPerToken
	if budgetTokens <= 0 {
		budget = DefaultContextTokens * BytesPerToken
	}
	if t.bytes() <= budget {
		return false
	}
	cut := t.cutPoint(budget)
	if cut == 0 {
		return false
	}
	dropped := t.msgs[:cut]
	note := fmt.Sprintf("[%d earlier messages were dropped to stay in budget]", len(dropped))
	if s != nil {
		if sum, err := s.Summarize(ctx, dropped); err == nil && sum != "" {
			note = "[earlier steps, summarised]\n" + sum
		}
	}
	rest := append([]model.Message{{Role: model.RoleUser, Content: note}}, t.msgs[cut:]...)
	t.msgs = rest
	// cut messages became one note, so everything after shifts by
	// cut-1 and every outstanding mark must shift with it.
	t.dropped += cut - 1
	t.protect = t.protect - cut + 1
	if t.protect < 1 {
		t.protect = 1
	}
	return true
}

// cutPoint is the first unit boundary bringing the tail under budget,
// never past what the open Turn protects. An impossible budget drops
// everything droppable rather than giving up and staying full.
func (t *transcript) cutPoint(budget int) int {
	tail := t.bytes()
	last := 0
	for i := 0; i < t.protect; {
		end := unitEnd(t.msgs, i)
		if end > t.protect {
			break
		}
		tail -= msgBytes(t.msgs[i:end])
		i, last = end, end
		if tail <= budget {
			return i
		}
	}
	return last
}

// unitEnd returns the index just past the unit starting at i. An
// assistant message owns the tool messages that answer it.
func unitEnd(msgs []model.Message, i int) int {
	if i >= len(msgs) {
		return len(msgs)
	}
	end := i + 1
	if msgs[i].Role != model.RoleAssistant {
		return end
	}
	for end < len(msgs) && msgs[end].Role == model.RoleTool {
		end++
	}
	return end
}

func bound(s string) string {
	if len(s) <= MaxResultBytes {
		return s
	}
	return s[:MaxResultBytes] + "\n…[truncated]"
}
