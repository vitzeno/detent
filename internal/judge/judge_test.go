package judge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/classify"
)

func TestJudge_ReadsWhatTheJudgeSaid(t *testing.T) {
	j := resultJudge{Asker: fakeAsker{answers: classify.Answers{
		"result_status": {Choice: string(event.StatusWarnings)},
		"render_kind":   {Choice: "file_listing"},
		"attention":     {Noul: 0.7},
		"goal_achieved": {Noul: 0.95},
	}}}
	got := j.Judge(context.Background(), "bash", event.Result{Stdout: "a.go\n"})

	assert.True(t, got.FromJudge)
	assert.Equal(t, event.StatusWarnings, got.Status)
	assert.Equal(t, event.RendersFiles, got.RenderKind)
	assert.InDelta(t, 0.7, got.Attention, 1e-9)
	assert.InDelta(t, 0.95, got.GoalAchieved, 1e-9)
}

// The heuristic must never claim to be a verdict: the UI tells the two
// apart by FromJudge alone.
func TestJudge_FallsBackWithoutClaimingToBeAVerdict(t *testing.T) {
	tests := []struct {
		name   string
		asker  classify.Asker
		res    event.Result
		status event.Status
	}{
		{"no judge at all", nil, event.Result{Stdout: "ok\n"}, event.StatusClean},
		{"judge unreachable", fakeAsker{err: errors.New("down")}, event.Result{Stdout: "ok\n"}, event.StatusClean},
		{"non-zero exit", nil, event.Result{ExitCode: 2, Stderr: "boom"}, event.StatusFailed},
		{"could not run", nil, event.Result{Err: "no such file"}, event.StatusFailed},
		{"silent", nil, event.Result{}, event.StatusEmpty},
		{"whitespace only", nil, event.Result{Stdout: "  \n\n"}, event.StatusEmpty},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resultJudge{Asker: tt.asker}.Judge(context.Background(), "bash", tt.res)
			assert.Equal(t, tt.status, got.Status)
			assert.False(t, got.FromJudge, "a guess must never present as a verdict")
			assert.NotEmpty(t, got.RenderKind, "something must always be drawable")
		})
	}
}

// A judged-met request is suggested to the model as answered, so it need not
// keep re-running what already answered it, and decides for itself.
func TestWatch_AHighScoreSuggestsFinishing(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	stop := Watch(t.Context(), bus, fakeAsker{answers: classify.Answers{
		"result_status": {Choice: string(event.StatusClean)},
		"goal_achieved": {Noul: 0.97},
	}})
	defer stop()

	intents, unsub := bus.Subscribe(event.Only(event.SuggestFinishKind))
	defer unsub()

	turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	bus.Publish(event.TurnStarted{Turn: turn, N: 1, Prompt: "count the files"})
	bus.Publish(event.ToolCallProposed{ToolCall: call, Tool: "bash"})
	ran(bus, call, event.Result{Stdout: "12\n"})

	select {
	case rec := <-intents:
		got, ok := rec.Event.(event.SuggestFinish)
		require.True(t, ok)
		assert.Equal(t, turn, got.Turn)
		assert.NotEmpty(t, got.Reason, "a suggestion must say why")
	case <-time.After(3 * time.Second):
		t.Fatal("a judged-met request was never suggested as answered")
	}
}

func TestWatch_ALowScoreLetsItCarryOn(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	stop := Watch(t.Context(), bus, fakeAsker{answers: classify.Answers{
		"result_status": {Choice: string(event.StatusClean)},
		"goal_achieved": {Noul: 0.2},
	}})
	defer stop()

	facts, unsub := bus.Subscribe(event.Only(event.ToolCallJudgedKind, event.SuggestFinishKind))
	defer unsub()

	call := uuid.Must(uuid.NewV7())
	bus.Publish(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "go"})
	bus.Publish(event.ToolCallProposed{ToolCall: call, Tool: "bash"})
	ran(bus, call, event.Result{Stdout: "partial\n"})

	judged, ok := next(t, facts).(event.ToolCallJudged)
	require.True(t, ok, "the judgement comes first")
	assert.Equal(t, call, judged.ToolCall)

	stop()
	assert.Empty(t, rest(bus, facts), "nothing else should follow")
}

// A heuristic must not suggest a request is answered: only a real verdict
// carries that weight.
func TestWatch_AGuessNeverSuggestsFinishing(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	stop := Watch(t.Context(), bus, nil) // no judge at all
	defer stop()

	facts, unsub := bus.Subscribe(event.Only(event.ToolCallJudgedKind, event.SuggestFinishKind))
	defer unsub()

	call := uuid.Must(uuid.NewV7())
	bus.Publish(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "go"})
	bus.Publish(event.ToolCallProposed{ToolCall: call, Tool: "bash"})
	ran(bus, call, event.Result{Stdout: "done\n"})

	assert.Equal(t, event.ToolCallJudgedKind, next(t, facts).Kind())
	stop()
	assert.Empty(t, rest(bus, facts), "a heuristic suggested a request was answered")
}

// A verdict that lands after its Turn ended must not reach the next one:
// the request it read as answered is not the one now running.
func TestWatch_ALateVerdictReachesOnlyItsOwnTurn(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	asker := &gatedAsker{gate: make(chan struct{}), asked: make(chan struct{}, 2)}
	stop := Watch(t.Context(), bus, asker)
	defer stop()

	facts, unsub := bus.Subscribe(event.Only(event.ToolCallJudgedKind, event.SuggestFinishKind))
	defer unsub()

	first, second := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	bus.Publish(event.TurnStarted{Turn: first, N: 1, Prompt: "count the files"})
	ran(bus, uuid.Must(uuid.NewV7()), event.Result{Stdout: "12\n"})
	waitFor(t, asker.asked)
	bus.Publish(event.TurnEnded{Turn: first})
	bus.Publish(event.TurnStarted{Turn: second, N: 2, Prompt: "now delete them"})
	// Asked only once the loop has moved past the end of the first Turn.
	ran(bus, uuid.Must(uuid.NewV7()), event.Result{Stdout: "ok\n"})
	waitFor(t, asker.asked)
	close(asker.gate)

	var told []uuid.UUID
	for judged := 0; judged < 2; {
		switch v := next(t, facts).(type) {
		case event.ToolCallJudged:
			judged++
		case event.SuggestFinish:
			told = append(told, v.Turn)
		}
	}
	stop()
	for _, e := range rest(bus, facts) {
		if v, ok := e.(event.SuggestFinish); ok {
			told = append(told, v.Turn)
		}
	}
	assert.Equal(t, []uuid.UUID{second}, told, "only the running Turn may be told")
}

// Stopping cancels a judgement in flight rather than waiting out the
// client's timeout, and publishes nothing for it.
func TestWatch_StopCancelsWhatIsInFlight(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	asker := &gatedAsker{gate: make(chan struct{}), asked: make(chan struct{}, 1)}
	stop := Watch(t.Context(), bus, asker)

	facts, unsub := bus.Subscribe(event.Only(event.ToolCallJudgedKind))
	defer unsub()

	bus.Publish(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "go"})
	ran(bus, uuid.Must(uuid.NewV7()), event.Result{Stdout: "x\n"})
	waitFor(t, asker.asked)

	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("stop waited on a judgement nobody wants any more")
	}
	assert.Empty(t, rest(bus, facts))
}

// The session's ctx ending abandons a judgement without anyone calling stop.
func TestWatch_ItsContextEndingCancelsWhatIsInFlight(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	ctx, cancel := context.WithCancel(t.Context())
	asker := &quittingAsker{asked: make(chan struct{}, 1), quit: make(chan struct{})}
	defer Watch(ctx, bus, asker)()

	bus.Publish(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "go"})
	ran(bus, uuid.Must(uuid.NewV7()), event.Result{Stdout: "x\n"})
	waitFor(t, asker.asked)
	cancel()
	waitFor(t, asker.quit)
}

// Shown only "bash", the judge classified a shape without knowing
// which command printed it.
func TestWatch_TheJudgeSeesTheCommand(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	asker := recordingAsker{states: make(chan classify.State, 1)}
	defer Watch(t.Context(), bus, asker)()

	call := uuid.Must(uuid.NewV7())
	bus.Publish(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "list it"})
	bus.Publish(event.ToolCallProposed{ToolCall: call, Tool: "bash", Args: map[string]any{"command": "ls -la"}})
	ran(bus, call, event.Result{Stdout: "total 0\n"})

	select {
	case s := <-asker.states:
		m, ok := s.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "ls -la", m["command"])
	case <-time.After(3 * time.Second):
		t.Fatal("the judge was never asked")
	}
}

// A child's calls answer its task, not the request, so only its spawn is
// judged, and that judges the report against the request.
func TestJudge_SkipsAChildsCallsButJudgesTheSpawn(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	asker := recordingAsker{states: make(chan classify.State, 2)}
	stop := Watch(t.Context(), bus, asker)

	spawn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	bus.Publish(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "how does login work"})
	bus.Publish(event.ToolCallProposed{ToolCall: spawn, Tool: "spawn_agent", Args: map[string]any{"task": "trace login"}})
	bus.Publish(event.ToolCallProposed{ToolCall: call, Tool: "grep", Args: map[string]any{"pattern": "login"},
		Agent: uuid.Must(uuid.NewV7())})
	ran(bus, call, event.Result{Stdout: "auth.go:12: func login\n"})
	ran(bus, spawn, event.Result{Stdout: "login is in auth.go:12"})
	bus.Settle(3 * time.Second)
	stop()

	require.Len(t, asker.states, 1, "the child's call was judged")
	m, ok := (<-asker.states).(map[string]any)
	require.True(t, ok)
	assert.Contains(t, m["command"], "spawn_agent")
}

// A tool call that was declined or abandoned never ran, so nobody asks about it.
func TestWatch_AToolCallThatNeverRanIsNotJudged(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	asker := recordingAsker{states: make(chan classify.State, 1)}
	stop := Watch(t.Context(), bus, asker)
	bus.Publish(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "p"})
	bus.Publish(event.ToolCallEnded{ToolCall: uuid.Must(uuid.NewV7()), Result: event.Result{Err: "declined"}})
	bus.Settle(3 * time.Second)
	stop() // waits for any judgement already started
	assert.Empty(t, asker.states, "judged a tool call that never ran")
}

func TestOutput_KeepsTheTailOfStderr(t *testing.T) {
	res := event.Result{Stdout: strings.Repeat("ok\n", 4000), Stderr: strings.Repeat("é", 2000) + "the error"}
	got := output(res)
	assert.Contains(t, got, "the error", "a long stdout must not hide why it failed")
	assert.True(t, utf8.ValidString(got))
	assert.Less(t, len(got), 4200)
}

// The judge is offered exactly the statuses event declares, so ui labels
// whatever it can answer.
func TestResultQuestions_OfferEveryStatus(t *testing.T) {
	var offered []event.Status
	for s := range resultQuestions()["result_status"].Choice.Criteria {
		offered = append(offered, event.Status(s))
	}
	assert.ElementsMatch(t, event.Statuses(), offered)
}

// ran publishes a tool call starting and ending, as the engine does for one that ran.
func ran(bus *event.Bus, call uuid.UUID, res event.Result) {
	bus.Publish(event.ToolCallStarted{ToolCall: call, Runner: "host"})
	bus.Publish(event.ToolCallEnded{ToolCall: call, Result: res})
}

func next(t *testing.T, ch <-chan event.Record) event.Event {
	t.Helper()
	select {
	case rec := <-ch:
		return rec.Event
	case <-time.After(3 * time.Second):
		t.Fatal("nothing arrived")
	}
	return nil
}

// rest is whatever else reaches ch once the bus settles. Call it after the
// stop, so nothing can still be on its way.
func rest(bus *event.Bus, ch <-chan event.Record) []event.Event {
	settled := make(chan struct{})
	go func() { bus.Settle(3 * time.Second); close(settled) }()
	var out []event.Event
	for {
		select {
		case rec := <-ch:
			out = append(out, rec.Event)
		case <-settled:
			return out
		}
	}
}

func waitFor(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("the judge was never asked")
	}
}

type fakeAsker struct {
	answers classify.Answers
	err     error
}

func (f fakeAsker) Ask(context.Context, classify.State, classify.Questions) (classify.Answers, classify.Usage, error) {
	if f.err != nil {
		return nil, classify.Usage{}, f.err
	}
	return f.answers, classify.Usage{}, nil
}

// recordingAsker keeps the state it was asked about.
type recordingAsker struct{ states chan classify.State }

func (r recordingAsker) Ask(_ context.Context, s classify.State, _ classify.Questions) (classify.Answers, classify.Usage, error) {
	r.states <- s
	return classify.Answers{}, classify.Usage{}, nil
}

// gatedAsker reads every request as answered once gate opens, or gives up with ctx.
type gatedAsker struct {
	gate  chan struct{}
	asked chan struct{}
}

func (g *gatedAsker) Ask(ctx context.Context, _ classify.State, _ classify.Questions) (classify.Answers, classify.Usage, error) {
	g.asked <- struct{}{}
	select {
	case <-g.gate:
		return classify.Answers{"goal_achieved": {Noul: 0.97}}, classify.Usage{}, nil
	case <-ctx.Done():
		return nil, classify.Usage{}, ctx.Err()
	}
}

// quittingAsker blocks until its ctx ends, and says when it has.
type quittingAsker struct{ asked, quit chan struct{} }

func (q *quittingAsker) Ask(ctx context.Context, _ classify.State, _ classify.Questions) (classify.Answers, classify.Usage, error) {
	q.asked <- struct{}{}
	<-ctx.Done()
	close(q.quit)
	return nil, classify.Usage{}, ctx.Err()
}
