package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/model"
)

// DefaultContextTokens caps the transcript, resent whole every Step.
// Sized for a large window, so a small local model wants context_tokens.
const (
	DefaultContextTokens = 200_000
	// BytesPerToken is the rough ratio a token budget is measured in.
	BytesPerToken = 4
	// MaxResultBytes bounds one result, the same bound a human's Shell gets.
	MaxResultBytes = capture.MaxResultBytes
	// minCompactShare is the fraction of the budget a cut must free to
	// be worth a summariser round trip when it cannot reach budget.
	minCompactShare = 10
)

// Summarizer condenses dropped Steps. Nil means they become a note
// saying they are gone.
type Summarizer interface {
	Summarize(ctx context.Context, msgs []event.Message) (string, error)
}

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
	// starts mark where each request began, oldest first.
	starts []start
}

// The notes compaction leaves at the front, which measure tells apart from a request.
const (
	summaryMarker = "[earlier steps, summarised]"
	droppedMarker = " were dropped to stay in budget]"
)

// start is one request's first message, as a mark, with its number and prompt.
type start struct {
	at, n  int
	prompt string
}

// step appends one Step atomically, filling any missing answer, since
// a call nothing answers breaks the next Step. Answers go by position,
// so two calls a model gave one id still get one answer each.
func (t *transcript) step(reply model.Reply, answers []string) []event.Message {
	added := []event.Message{{
		Role: event.RoleAssistant, Content: reply.Text, Requests: reply.Requests,
	}}
	for i, c := range reply.Requests {
		body := "This call did not run."
		if i < len(answers) && answers[i] != "" {
			body = answers[i]
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

// user opens request n and protects everything from here on.
func (t *transcript) user(n int, prompt string) []event.Message {
	t.protect = len(t.msgs)
	t.begin(n, prompt)
	return t.add(event.Message{Role: event.RoleUser, Content: prompt})
}

// begin marks request n as starting at the next message.
func (t *transcript) begin(n int, prompt string) {
	t.starts = append(t.starts, start{at: t.mark(), n: n, prompt: prompt})
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
// land inside a Step. A mark compaction has eaten is a no-op, reported.
func (t *transcript) truncate(to int) bool {
	at := to - t.dropped
	// At 0 after a compaction the mark is inside what the note stands for.
	if at < 0 || at > len(t.msgs) || (at == 0 && t.dropped > 0) {
		return false
	}
	t.msgs = t.msgs[:at]
	t.protect = min(t.protect, at)
	for i, s := range t.starts {
		if s.at >= to {
			t.starts = t.starts[:i]
			break
		}
	}
	return true
}

func (t *transcript) reset() { t.msgs, t.protect, t.dropped, t.starts = nil, 0, 0, nil }

func (t *transcript) bytes() int { return msgBytes(t.msgs) }

func msgBytes(msgs []event.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
		for _, c := range m.Requests {
			n += len(c.Name) + len(fmt.Sprint(c.Args))
		}
	}
	return n
}

// compact drops whole Steps off the front until the transcript fits.
// Whole, because half a Step is a transcript no endpoint accepts.
func (t *transcript) compact(ctx context.Context, budgetTokens int, s Summarizer) (dropped int, note string) {
	cut := t.cutFor(budgetTokens)
	if cut == 0 {
		return 0, ""
	}
	note = summarise(ctx, s, t.msgs[:cut])
	t.fold(cut, note)
	return cut, note
}

// cutFor is how many messages compaction would fold, zero for none.
func (t *transcript) cutFor(budgetTokens int) int {
	budget := budgetTokens * BytesPerToken
	if t.bytes() <= budget {
		return 0
	}
	cut := t.cutPoint(budget)
	if cut == 0 {
		return 0
	}
	// A sliver that still misses the budget is asked for again next
	// Step, so it only ever re-summarises its own note.
	freed := msgBytes(t.msgs[:cut])
	if freed < budget/minCompactShare && t.bytes()-freed > budget {
		return 0
	}
	return cut
}

// summarise is the note that stands for what a compaction drops.
func summarise(ctx context.Context, s Summarizer, gone []event.Message) string {
	note := fmt.Sprintf("[%d earlier messages"+droppedMarker, len(gone))
	if s != nil {
		if sum, err := s.Summarize(ctx, gone); err == nil && sum != "" {
			note = summaryMarker + "\n" + sum
		}
	}
	return note
}

// fold replaces the first cut messages with note.
func (t *transcript) fold(cut int, note string) {
	t.msgs = append([]event.Message{{Role: event.RoleUser, Content: note}}, t.msgs[cut:]...)
	// cut messages became one note, so everything after shifts by
	// cut-1 and every outstanding mark must shift with it.
	t.dropped += cut - 1
	t.protect = max(t.protect-cut+1, 1)
}

// cutPoint is the first unit boundary bringing the tail under budget,
// never past the open Turn. An impossible budget drops all it can.
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
