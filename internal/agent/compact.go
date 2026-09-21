package agent

import (
	"context"
	"fmt"

	"github.com/vitzeno/detent/internal/propose"
)

// MaxTranscriptBytes caps the transcript as a whole. boundStr bounds
// one entry, but the whole thing is resent on every propose call, so
// without this a long session outgrows the context window. Roughly 30k
// tokens, leaving room for the system prompt and the reply.
const MaxTranscriptBytes = 120 * 1024

// Summarizer condenses the oldest turns. Optional, like Judge: without
// one, compaction drops them and says plainly that it did.
type Summarizer interface {
	Summarize(ctx context.Context, messages []propose.Message) (string, error)
}

// compact keeps the newest turns whole and replaces the rest with one
// note. Only safe at a goal boundary — see the call in BeginGoal.
func (s *Session) compact(ctx context.Context) {
	if s.transcriptBytes() <= MaxTranscriptBytes {
		return
	}
	cut := s.cutPoint()
	if cut <= 0 {
		return
	}
	old := s.Transcript[:cut]

	note := droppedNote(old)
	if s.Summarizer != nil {
		if sum, err := s.Summarizer.Summarize(ctx, old); err == nil && sum != "" {
			note = "Earlier in this session: " + sum
		}
	}

	kept := append([]propose.Message{{Role: propose.RoleUser, Content: note}},
		s.Transcript[cut:]...)
	s.Transcript = kept
	// One message replaced cut of them, so every existing mark now
	// resolves that much further forward. See index.
	s.dropped += cut - 1
}

// cutPoint is the first message to keep: half the budget's worth of
// tail, so compaction buys room rather than running again next goal.
func (s *Session) cutPoint() int {
	target := MaxTranscriptBytes / 2
	total := 0
	for i := len(s.Transcript) - 1; i >= 0; i-- {
		total += len(s.Transcript[i].Content)
		if total > target {
			return i + 1
		}
	}
	return 0
}

func (s *Session) transcriptBytes() int {
	n := 0
	for _, m := range s.Transcript {
		n += len(m.Content)
	}
	return n
}

// droppedNote stands in when nothing summarised the turns, so the
// model looks at current state rather than assuming it starts here.
func droppedNote(dropped []propose.Message) string {
	return fmt.Sprintf("[%d earlier turns in this session were dropped to stay inside the "+
		"context window. Their commands and output are gone — check the current state "+
		"rather than assuming it.]", len(dropped))
}

// index converts a mark into a slice index; ok is false once the mark
// has been compacted away.
func (s *Session) index(mark int) (int, bool) {
	i := mark - s.dropped
	if i < 0 || i > len(s.Transcript) {
		return 0, false
	}
	return i, true
}
