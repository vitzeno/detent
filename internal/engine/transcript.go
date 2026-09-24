package engine

import (
	"context"
	"fmt"
	"github.com/vitzeno/detent/event"
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
	// minCompactShare is the fraction of the budget a cut must free to
	// be worth a summariser round trip when it cannot reach budget.
	minCompactShare = 10
)

// transcript is the model's input. Its atom is a Step, which nothing
// may split, so every mutation lives here.
type transcript struct {
	msgs []event.Message
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
func (t *transcript) step(reply model.Reply, answers map[string]string) []event.Message {
	added := []event.Message{{
		Role: event.RoleAssistant, Content: reply.Text, Calls: reply.Calls,
	}}
	for _, c := range reply.Calls {
		body, ok := answers[c.ID]
		if !ok {
			body = "This call did not run."
		}
		added = append(added, event.Answer(c, bound(body)))
	}
	t.msgs = append(t.msgs, added...)
	return added
}

// say appends the model's prose for a Step that called nothing.
func (t *transcript) say(text string) []event.Message {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return t.add(event.Message{Role: event.RoleAssistant, Content: text})
}

// user opens a Turn and protects everything from here on.
func (t *transcript) user(prompt string) []event.Message {
	t.protect = len(t.msgs)
	return t.add(event.Message{Role: event.RoleUser, Content: prompt})
}

// note is a NoteContext: a message with no tool run.
func (t *transcript) note(text string) []event.Message {
	return t.add(event.Message{Role: event.RoleUser, Content: text})
}

// add appends and reports what it appended, which is what a caller
// publishes so a replay can put the same thing back.
func (t *transcript) add(m event.Message) []event.Message {
	t.msgs = append(t.msgs, m)
	return []event.Message{m}
}

func (t *transcript) messages() []event.Message { return t.msgs }

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

func msgBytes(msgs []event.Message) int {
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
	Summarize(ctx context.Context, msgs []event.Message) (string, error)
}

// compact drops whole Steps off the front until the transcript fits.
// Whole, because half a Step is a transcript no endpoint accepts.
func (t *transcript) compact(ctx context.Context, budgetTokens int, s Summarizer) (dropped int, note string) {
	budget := budgetTokens * BytesPerToken
	if budgetTokens <= 0 {
		budget = DefaultContextTokens * BytesPerToken
	}
	if t.bytes() <= budget {
		return 0, ""
	}
	cut := t.cutPoint(budget)
	if cut == 0 {
		return 0, ""
	}
	// A sliver that still misses the budget is asked for again next
	// Step, so it only ever re-summarises its own note.
	freed := msgBytes(t.msgs[:cut])
	if freed < budget/minCompactShare && t.bytes()-freed > budget {
		return 0, ""
	}
	gone := t.msgs[:cut]
	note = fmt.Sprintf("[%d earlier messages were dropped to stay in budget]", len(gone))
	if s != nil {
		if sum, err := s.Summarize(ctx, gone); err == nil && sum != "" {
			note = "[earlier steps, summarised]\n" + sum
		}
	}
	rest := append([]event.Message{{Role: event.RoleUser, Content: note}}, t.msgs[cut:]...)
	t.msgs = rest
	// cut messages became one note, so everything after shifts by
	// cut-1 and every outstanding mark must shift with it.
	t.dropped += cut - 1
	t.protect = t.protect - cut + 1
	if t.protect < 1 {
		t.protect = 1
	}
	return cut, note
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
func unitEnd(msgs []event.Message, i int) int {
	if i >= len(msgs) {
		return len(msgs)
	}
	end := i + 1
	if msgs[i].Role != event.RoleAssistant {
		return end
	}
	for end < len(msgs) && msgs[end].Role == event.RoleTool {
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
