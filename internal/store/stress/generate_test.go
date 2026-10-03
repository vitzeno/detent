// Package stress writes a long session into the real store, so resume
// and the history pane have something session-sized to run against.
//
//	DETENT_STRESS=1 go test ./internal/store/stress/ -run TestGenerate -v
//
// Skipped otherwise. DETENT_STRESS_TURNS, _NAME, _SEED and _DB override
// the defaults, and the same seed writes the same session.
package stress_test

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/store"
)

const (
	defaultTurns = 1000
	defaultName  = "stress"
	defaultSeed  = 1
)

func TestGenerate(t *testing.T) {
	if os.Getenv("DETENT_STRESS") == "" {
		t.Skip("set DETENT_STRESS=1 to write a stress session to the real store")
	}
	turns := envInt(t, "DETENT_STRESS_TURNS", defaultTurns)
	name := envStr("DETENT_STRESS_NAME", defaultName)
	rawSeed := envInt(t, "DETENT_STRESS_SEED", defaultSeed)
	require.GreaterOrEqual(t, rawSeed, 0, "DETENT_STRESS_SEED must not be negative")
	seed := uint64(rawSeed)

	db, err := store.Open(envStr("DETENT_STRESS_DB", store.DefaultPath()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	// Checked before writing, or a taken name fails only after a thousand Turns.
	existing, err := db.Sessions()
	require.NoError(t, err)
	for _, s := range existing {
		require.NotEqualf(t, name, s.Name, "a session is already called %q, so set DETENT_STRESS_NAME", name)
	}

	w := &writer{
		t: t, store: db, session: uuid.Must(uuid.NewV7()),
		rng: rand.New(rand.NewPCG(seed, seed)),
		// Backdated, or a thousand turns land in one second.
		at: time.Now().Add(-time.Duration(turns) * 3 * time.Minute),
	}

	start := time.Now()
	w.run(turns)
	require.NoError(t, db.Rename(w.session, name))

	t.Logf("wrote %d turns as %d records in %s", turns, w.ordinal, time.Since(start).Round(time.Millisecond))
	t.Logf("session %s named %q", w.session, name)
	t.Logf("open it with: detent -resume %s", name)
}

// writer lays one session down record by record, with gapless
// ordinals: a subscriber that filtered reads differently from one that lost.
type writer struct {
	t       *testing.T
	store   *store.Store
	session uuid.UUID
	rng     *rand.Rand
	ordinal uint64
	at      time.Time
}

func (w *writer) run(turns int) {
	w.emit(event.SessionStarted{
		Session: w.session, Model: "openai/gpt-6-luna", Judge: "jev-1",
		Sandbox: true, Network: true, MaxSteps: 50,
		Recorded: true, ContextTokens: 200000,
	})
	for n := 1; n <= turns; n++ {
		w.turn(n)
	}
}

// turn is the unit of undo, so it opens with a checkpoint.
func (w *writer) turn(n int) {
	turn := uuid.Must(uuid.NewV7())
	prompt := pick(w.rng, prompts)
	w.emit(event.TurnStarted{Turn: turn, N: n, Prompt: prompt})
	w.emit(event.CheckpointTaken{Turn: turn, Snapshot: fmt.Sprintf("snap-%d", n), Tree: fmt.Sprintf("tree-%d", n)})
	w.emit(event.Appended{Turn: turn, Messages: []event.Message{{Role: event.RoleUser, Content: prompt}}})

	// Mostly short, a few long enough to be worth scrolling.
	steps := 1 + w.rng.IntN(3)
	if w.rng.IntN(20) == 0 {
		steps = 4 + w.rng.IntN(8)
	}
	var total event.Usage
	for i := 1; i <= steps; i++ {
		total = total.Add(w.step(turn, i, i == steps))
	}
	if w.rng.IntN(25) == 0 {
		w.emit(event.Compacted{Turn: turn, Dropped: 10 + w.rng.IntN(40), Note: "earlier steps summarised"})
	}
	if steps > 8 {
		w.emit(event.BoundReached{Turn: turn, Steps: steps, ToolCalls: steps * 2})
	}
	w.emit(event.TurnEnded{Turn: turn, Reason: w.reason(), Summary: pick(w.rng, summaries), Usage: total})
	// The third Turn always rolls back, so a short session still holds one.
	if n == 3 || w.rng.IntN(60) == 0 {
		w.emit(event.RolledBack{Turn: turn, RevertFiles: w.rng.IntN(2) == 0})
	}
	if w.rng.IntN(30) == 0 {
		w.emit(event.Notice{Level: pick(w.rng, levels), Text: pick(w.rng, notices)})
	}
}

// step is one round trip: an assistant message and an answer for
// every Call it asked for, never half of one.
func (w *writer) step(turn uuid.UUID, n int, last bool) event.Usage {
	step := uuid.Must(uuid.NewV7())
	w.emit(event.StepStarted{Turn: turn, Step: step, N: n})

	calls := 1 + w.rng.IntN(3)
	if last {
		calls = 0
	}
	if w.rng.IntN(8) == 0 && !last {
		w.emit(event.ModelText{Turn: turn, Step: step, Text: pick(w.rng, prose)})
	}

	asked := make([]event.ToolRequest, 0, calls)
	answers := make([]event.Message, 0, calls)
	for range calls {
		c, answer := w.call(step)
		asked = append(asked, c)
		answers = append(answers, answer)
	}

	text := ""
	if last {
		text = pick(w.rng, prose)
		w.emit(event.ModelText{Turn: turn, Step: step, Text: text})
	}
	w.emit(event.Appended{Turn: turn, Step: step, Messages: append(
		[]event.Message{{Role: event.RoleAssistant, Content: text, Requests: asked}}, answers...)})

	used := event.Usage{
		PromptTokens:     2000 + w.rng.IntN(60000),
		CompletionTokens: 50 + w.rng.IntN(900),
		Latency:          time.Duration(300+w.rng.IntN(4000)) * time.Millisecond,
		Model:            "openai/gpt-6-luna",
	}
	w.emit(event.StepEnded{Turn: turn, Step: step, Usage: used, ToolCalls: calls})
	return used
}

// call is proposed, assessed, maybe approved, run and judged.
func (w *writer) call(step uuid.UUID) (event.ToolRequest, event.Message) {
	call := uuid.Must(uuid.NewV7())
	// The Call's own id, so no two Turns share one and mask a missing answer.
	id := call.String()
	t := pick(w.rng, tools)
	args := t.args(w.rng)
	risk := t.risk(w.rng)

	w.emit(event.ToolCallProposed{
		ToolCall: call, Step: step, Tool: t.name, Args: args,
		Rationale: risk.Note, Renders: t.renders, Executor: t.executor,
	})
	w.emit(event.ToolCallAssessed{ToolCall: call, Risk: risk})
	// Only a Dangerous verdict asks: the gate is conditional.
	if risk.Dangerous {
		w.emit(event.ApprovalAsked{ToolCall: call, Tool: t.name, Args: args, Rationale: risk.Note, Risk: risk})
	}
	w.emit(event.ToolCallStarted{ToolCall: call, Runner: t.runner})

	res := t.result(w.rng)
	w.emit(event.ToolCallEnded{
		ToolCall: call, Result: res,
		Took: time.Duration(50+w.rng.IntN(9000)) * time.Millisecond,
	})
	w.emit(event.ToolCallJudged{
		ToolCall: call, Status: status(res), RenderKind: t.render,
		Attention: w.rng.Float64(), GoalAchieved: w.rng.Float64(), FromJudge: true,
	})

	content := res.Stdout
	if res.Err != "" {
		content = res.Err
	}
	return event.ToolRequest{ID: id, Name: t.name, Args: args},
		event.Message{Role: event.RoleTool, RequestID: id, Content: content}
}

func (w *writer) reason() event.EndReason {
	switch w.rng.IntN(20) {
	case 0:
		return event.EndError
	case 1:
		return event.EndAborted
	case 2:
		return event.EndStopped
	}
	return event.EndDone
}

// emit stamps the next ordinal and nudges the clock.
func (w *writer) emit(e event.Event) {
	w.t.Helper()
	w.ordinal++
	w.at = w.at.Add(time.Duration(200+w.rng.IntN(6000)) * time.Millisecond)
	require.NoError(w.t, w.store.Append(w.session, event.Record{
		Ordinal: w.ordinal, At: w.at, Event: e,
	}))
}

func status(r event.Result) string {
	switch {
	case r.Err != "":
		return "error"
	case r.ExitCode != 0:
		return "failed"
	}
	return "ok"
}

func pick[T any](r *rand.Rand, from []T) T { return from[r.IntN(len(from))] }

func envInt(t *testing.T, key string, def int) int {
	t.Helper()
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	require.NoErrorf(t, err, "%s must be a number", key)
	return n
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
