package stress_test

import (
	"math/rand/v2"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/store"
	"github.com/vitzeno/detent/ui"
)

// Generated events are worth nothing unless they are the shape real
// ones are, so this runs every time rather than behind the flag.
func TestGenerated_ReplaysAsARealSessionWould(t *testing.T) {
	records, session := generate(t, 40)

	require.NotEmpty(t, records)
	started, ok := records[0].Event.(event.SessionStarted)
	assert.True(t, ok, "a session that does not open with SessionStarted has no header")

	// Gapless from 1, or a filtered subscriber reads as a lossy one.
	for i, r := range records {
		require.Equal(t, uint64(i+1), r.Ordinal, "ordinal %d is out of step", i)
	}

	turns, ended := map[uuid.UUID]bool{}, 0
	for _, r := range records {
		switch v := r.Event.(type) {
		case event.TurnStarted:
			turns[v.Turn] = true
		case event.TurnEnded:
			assert.True(t, turns[v.Turn], "a turn ended that never started")
			ended++
		}
	}
	assert.Len(t, turns, 40, "not every turn was written")
	assert.Equal(t, 40, ended, "a turn was left open")
	assert.Equal(t, session, started.Session)
}

// A Step is indivisible: an endpoint rejects either half on its own.
func TestGenerated_EveryToolCallIsAnswered(t *testing.T) {
	records, _ := generate(t, 40)

	asked, answered := map[string]event.ToolName{}, map[string]bool{}
	for _, r := range records {
		app, ok := r.Event.(event.Appended)
		if !ok {
			continue
		}
		for _, m := range app.Messages {
			for _, c := range m.Requests {
				asked[c.ID] = c.Name
			}
			if m.Role == event.RoleTool {
				answered[m.RequestID] = true
			}
		}
	}
	require.NotEmpty(t, asked, "no tool calls were generated at all")
	for id, name := range asked {
		assert.Truef(t, answered[id], "%s call %q was never answered", name, id)
	}
	for id := range answered {
		assert.Containsf(t, asked, id, "an answer came back for call %q that was never asked", id)
	}
}

// A front-end folds it with no harness, and every tool call becomes a row
// except in a Turn that was rolled back, which takes its rows with it.
func TestGenerated_TheUiFoldsEveryToolCallIntoARow(t *testing.T) {
	records, _ := generate(t, 40)

	// CallProposed names its Step, not its Turn, so Steps tie the two.
	turnOf, undone := map[uuid.UUID]uuid.UUID{}, map[uuid.UUID]bool{}
	for _, r := range records {
		switch v := r.Event.(type) {
		case event.StepStarted:
			turnOf[v.Step] = v.Turn
		case event.RolledBack:
			undone[v.Turn] = true
		}
	}
	require.NotEmpty(t, undone, "no rollback was generated, so this proves less than it looks")

	want := 0
	for _, r := range records {
		switch v := r.Event.(type) {
		case event.ToolCallProposed:
			if !undone[turnOf[v.Step]] {
				want++
			}
		case event.ModelText:
			if !undone[v.Turn] {
				want++
			}
		}
	}
	require.NotZero(t, want)

	m := ui.New(t.Context(), event.New(), ui.SessionInfo{}).Restore(records)
	assert.Equal(t, want, m.RowCount(), "the history lost rows on the way in")
	assert.True(t, m.Idle(), "a restored session is waiting on a request that already ended")
	assert.NotEmpty(t, m.View().Content, "a restored session draws nothing")
}

// A tool the registry ships but the generator never calls is a shape
// no stress run has drawn.
func TestTools_EveryRegisteredToolHasAGenerator(t *testing.T) {
	for _, name := range registry().Names() {
		assert.Containsf(t, gens, name, "%s has no generator", name)
	}
}

// A render kind event does not know folds as unknown and draws no view.
func TestTools_RenderKindsAreRealOnes(t *testing.T) {
	for _, k := range tools {
		assert.Containsf(t, event.RenderKinds(), k.render, "%s renders as %q", k.name, k.render)
	}
}

// generate writes to a scratch database and reads it back, as resume does.
func generate(t *testing.T, turns int) ([]event.Record, uuid.UUID) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "events.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	w := &writer{
		t: t, store: db, session: uuid.Must(uuid.NewV7()),
		rng: rand.New(rand.NewPCG(1, 1)), at: time.Now().Add(-time.Hour),
	}
	w.run(turns)

	records, err := db.Replay(w.session)
	require.NoError(t, err)
	return records, w.session
}
