package event

import (
	"go/build"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This package is imported by ui and by the engine alike, which is
// what removes the translation layer between them. It can only stay
// that way while it depends on nothing either side could disagree
// about: the standard library, plus viewspec, which is itself stdlib
// only and already imported by both.
func TestPackage_DependsOnStdlibAndViewspecOnly(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	require.NoError(t, err)

	const allowed = "github.com/vitzeno/detent/viewspec"
	for _, imp := range pkg.Imports {
		if imp == allowed || !strings.Contains(imp, ".") {
			continue
		}
		t.Errorf("event imports %q; only the standard library and %s are allowed", imp, allowed)
	}
}

// Every Kind must name exactly one event type, and every event type
// must have a Kind. A duplicate makes a log query match two things and
// a reducer switch on the wrong one.
func TestKinds_AreUniqueAndComplete(t *testing.T) {
	all := []Event{
		SessionStarted{}, TurnStarted{}, TurnEnded{}, CheckpointTaken{},
		RolledBack{}, BoundReached{}, StepStarted{}, StepEnded{}, ModelText{},
		CallProposed{}, CallAssessed{}, ApprovalAsked{}, CallStarted{},
		OutputChunk{}, CallEnded{}, CallJudged{}, ViewReady{}, Notice{},
		SubmitPrompt{}, ResolveApproval{}, NoteContext{}, Abort{},
		RequestStop{}, Continue{}, RequestRollback{}, ResetSession{},
	}
	seen := map[Kind]bool{}
	for _, e := range all {
		k := e.Kind()
		assert.NotEmpty(t, k, "%T has no Kind", e)
		assert.False(t, seen[k], "%T reuses kind %q", e, k)
		seen[k] = true
	}
	assert.Len(t, all, len(seen))
}

// Tense is the convention that tells a reader which way an event
// travels, so it has to actually hold.
func TestKinds_IntentsAndFactsAreDistinct(t *testing.T) {
	facts := []Event{TurnStarted{}, CallEnded{}, Notice{}, ApprovalAsked{}}
	for _, e := range facts {
		assert.False(t, e.Kind().IsIntent(), "%T is a fact", e)
	}
	intents := []Event{SubmitPrompt{}, Abort{}, RequestStop{}, ResetSession{}}
	for _, e := range intents {
		assert.True(t, e.Kind().IsIntent(), "%T is an intent", e)
	}
}

// Exactly one event is lossy. If a second ever is, it was a decision.
func TestLossy_IsOutputChunkAlone(t *testing.T) {
	assert.True(t, OutputChunk{}.Lossy())
	for _, e := range []Event{CallEnded{}, StepEnded{}, TurnEnded{}, Notice{}, Abort{}} {
		assert.False(t, e.Lossy(), "%T must be delivered", e)
	}
}

func TestNewID_IsUUIDv7AndSortsByTime(t *testing.T) {
	a := NewID()
	require.Len(t, string(a), 36)
	assert.Equal(t, byte('7'), string(a)[14], "version nibble")
	assert.Contains(t, "89ab", string(string(a)[19]), "variant nibble")

	seen := map[ID]bool{}
	prev := ID("")
	for range 200 {
		id := NewID()
		assert.False(t, seen[id], "duplicate id %s", id)
		seen[id] = true
		if prev != "" {
			assert.GreaterOrEqual(t, string(id), string(prev), "ids must sort by creation")
		}
		prev = id
	}
}
