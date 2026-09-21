package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/propose"
)

type stubSummarizer struct {
	summary string
	err     error
	saw     []propose.Message
}

func (s *stubSummarizer) Summarize(_ context.Context, m []propose.Message) (string, error) {
	s.saw = append(s.saw, m...)
	return s.summary, s.err
}

// fill appends n messages of size bytes each, through append so seq
// tracks them the way real turns would.
func fill(s *Session, n, size int) {
	for i := 0; i < n; i++ {
		s.append(propose.Message{Role: propose.RoleTool, Content: strings.Repeat("x", size)})
	}
}

func contents(msgs []propose.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}

func TestSession_Compact_LeavesSmallTranscriptsAlone(t *testing.T) {
	s := &Session{}
	fill(s, 10, 1024)
	before := append([]propose.Message(nil), s.Transcript...)

	s.compact(context.Background())

	assert.Equal(t, before, s.Transcript)
	assert.Zero(t, s.dropped)
}

func TestSession_Compact_DropsOldestAndKeepsNewest(t *testing.T) {
	s := &Session{}
	fill(s, 100, 4*1024) // 400KB, comfortably over budget
	s.append(propose.Message{Role: propose.RoleUser, Content: "the newest turn"})

	s.compact(context.Background())

	assert.LessOrEqual(t, s.transcriptBytes(), s.budgetBytes())
	assert.Equal(t, "the newest turn", s.Transcript[len(s.Transcript)-1].Content)
	assert.Contains(t, s.Transcript[0].Content, "earlier turns in this session were dropped")
	// The invariant every mark depends on.
	assert.Equal(t, len(s.Transcript), s.seq-s.dropped)
}

func TestSession_Compact_HonoursConfiguredBudget(t *testing.T) {
	small := &Session{ContextTokens: 1_000} // 4KB
	big := &Session{ContextTokens: 200_000} // 800KB
	fill(small, 50, 4*1024)
	fill(big, 50, 4*1024) // 200KB: over the small budget, under the big one

	small.compact(context.Background())
	big.compact(context.Background())

	assert.Less(t, small.transcriptBytes(), 50*4*1024, "a small budget compacts")
	assert.Zero(t, big.dropped, "a budget above the transcript leaves it alone")
}

func TestSession_BudgetBytes_DefaultsWhenUnset(t *testing.T) {
	assert.Equal(t, DefaultContextTokens*BytesPerToken, (&Session{}).budgetBytes())
	assert.Equal(t, DefaultContextTokens*BytesPerToken, (&Session{ContextTokens: -1}).budgetBytes())
	assert.Equal(t, 40_000, (&Session{ContextTokens: 10_000}).budgetBytes())
}

func TestSession_Compact_UsesSummarizerWhenWired(t *testing.T) {
	sum := &stubSummarizer{summary: "cloned the repo and ran the tests"}
	s := &Session{Summarizer: sum}
	fill(s, 100, 4*1024)

	s.compact(context.Background())

	assert.Contains(t, s.Transcript[0].Content, "cloned the repo and ran the tests")
	assert.NotEmpty(t, sum.saw, "summarizer should see the turns being dropped")
}

func TestSession_Compact_FallsBackWhenSummarizerFails(t *testing.T) {
	s := &Session{Summarizer: &stubSummarizer{err: errors.New("no")}}
	fill(s, 100, 4*1024)

	s.compact(context.Background())

	assert.Contains(t, s.Transcript[0].Content, "were dropped")
	assert.LessOrEqual(t, s.transcriptBytes(), s.budgetBytes())
}

// A mark is only useful if it still names the same message after the
// front of the transcript has been rewritten underneath it.
func TestSession_Compact_MarksStillResolve(t *testing.T) {
	s := &Session{}
	fill(s, 100, 4*1024)
	s.append(propose.Message{Role: propose.RoleTool, Content: "landmark"})
	mark := s.mark()
	fill(s, 5, 100)

	s.compact(context.Background())

	i, ok := s.index(mark)
	require.True(t, ok)
	assert.Equal(t, "landmark", s.Transcript[i-1].Content)
}

func TestSession_Compact_MarkBeforeCutIsGone(t *testing.T) {
	s := &Session{}
	fill(s, 100, 4*1024)
	early := 1 // the very first message

	s.compact(context.Background())

	_, ok := s.index(early)
	assert.False(t, ok)
}

func TestSession_Rollback_RefusesCompactedStep(t *testing.T) {
	fake := &fakeSnapshotRunner{snapID: "snap-1"}
	s := &Session{Runners: sandboxSelector{sandbox: fake}}
	res := &GoalResult{
		Goal:         "g",
		Baseline:     "base",
		BaselineMark: 1,
		Commands:     []*ExecutedCommand{{Command: "echo hi", SnapshotID: "snap-1"}},
	}
	fill(s, 100, 4*1024)
	s.compact(context.Background())

	ok, err := s.Rollback(context.Background(), res, 1, false)
	assert.True(t, ok)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "older than the transcript")
	assert.Empty(t, fake.rolledBackTo, "nothing should be restored when the transcript can't follow")
}

// Compaction has to happen at the goal boundary and before the new
// goal is appended, or the fresh ask is what gets dropped.
func TestSession_BeginGoal_CompactsFirst(t *testing.T) {
	s := &Session{
		Proposer: &stubProposer{},
		Runners:  SingleRunner{Runner: okRun(host.Result{})},
	}
	fill(s, 100, 4*1024)

	_, err := s.BeginGoal(context.Background(), "count the files")
	require.NoError(t, err)

	assert.Contains(t, s.Transcript[0].Content, "were dropped")
	assert.Contains(t, contents(s.Transcript), "count the files",
		"the goal is appended after compaction, so it survives it")
	assert.Equal(t, len(s.Transcript), s.seq-s.dropped)
}

func TestSession_Reset_ClearsMarks(t *testing.T) {
	s := &Session{}
	fill(s, 100, 4*1024)
	s.compact(context.Background())
	s.Reset()

	assert.Zero(t, s.seq)
	assert.Zero(t, s.dropped)
	assert.Empty(t, s.Transcript)
}

func TestRunSafely_TurnsPanicIntoError(t *testing.T) {
	boom := runFunc(func(context.Context, string, chan<- host.StreamEvent) (host.Result, error) {
		panic("daemon went away")
	})

	_, err := runSafely(context.Background(), boom, "echo hi", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "daemon went away")
}

// A panicking Runner must close the goal the same way a failing one
// does, not take the session down with it.
func TestSession_Execute_SurvivesPanickingRunner(t *testing.T) {
	boom := runFunc(func(context.Context, string, chan<- host.StreamEvent) (host.Result, error) {
		panic("kaboom")
	})
	s := &Session{Proposer: &stubProposer{}, Runners: SingleRunner{Runner: boom}}
	res := &GoalResult{Goal: "g"}

	_, err := s.Execute(context.Background(), res, nil, propose.Proposal{Command: "echo hi"}, PreJudgment{}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "kaboom")
	assert.Equal(t, EndProposerError, res.End)
}
