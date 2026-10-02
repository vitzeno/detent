package judge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/classify"
)

func TestJudge_ReadsWhatTheJudgeSaid(t *testing.T) {
	j := ResultJudge{Asker: fakeAsker{answers: classify.Answers{
		"result_status": {Choice: StatusWarnings},
		"render_kind":   {Choice: "file_listing"},
		"attention":     {Noul: 0.7},
		"goal_achieved": {Noul: 0.95},
	}}}
	got := j.Judge(context.Background(), "bash", event.Result{Stdout: "a.go\n"})

	assert.True(t, got.FromJudge)
	assert.Equal(t, StatusWarnings, got.Status)
	assert.Equal(t, "file_listing", got.RenderKind)
	assert.Equal(t, 0.7, got.Attention)
	assert.Equal(t, 0.95, got.GoalAchieved)
}

// The heuristic must never claim to be a verdict: the UI tells the two
// apart by FromJudge alone.
func TestJudge_FallsBackWithoutClaimingToBeAVerdict(t *testing.T) {
	tests := []struct {
		name   string
		asker  classify.Asker
		res    event.Result
		status string
	}{
		{"no judge at all", nil, event.Result{Stdout: "ok\n"}, StatusClean},
		{"judge unreachable", fakeAsker{err: errors.New("down")}, event.Result{Stdout: "ok\n"}, StatusClean},
		{"non-zero exit", nil, event.Result{ExitCode: 2, Stderr: "boom"}, StatusFailed},
		{"could not run", nil, event.Result{Err: "no such file"}, StatusFailed},
		{"silent", nil, event.Result{}, StatusEmpty},
		{"whitespace only", nil, event.Result{Stdout: "  \n\n"}, StatusEmpty},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResultJudge{Asker: tt.asker}.Judge(context.Background(), "bash", tt.res)
			assert.Equal(t, tt.status, got.Status)
			assert.False(t, got.FromJudge, "a guess must never present as a verdict")
			assert.NotEmpty(t, got.RenderKind, "something must always be drawable")
		})
	}
}

// A judged-met request becomes an advisory stop, so a model does not
// keep re-running what already answered it.
func TestWatch_AHighScoreAsksTheTurnToStop(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	stop := Watch(bus, fakeAsker{answers: classify.Answers{
		"result_status": {Choice: StatusClean},
		"goal_achieved": {Noul: 0.97},
	}})
	defer stop()

	intents, unsub := bus.Subscribe(event.Only(event.RequestStopKind))
	defer unsub()

	turn, call := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	bus.Publish(event.TurnStarted{Turn: turn, N: 1, Prompt: "count the files"})
	bus.Publish(event.CallProposed{Call: call, Tool: "bash"})
	bus.Publish(event.CallEnded{Call: call, Result: event.Result{Stdout: "12\n"}})

	select {
	case rec := <-intents:
		got := rec.Event.(event.RequestStop)
		assert.Equal(t, turn, got.Turn)
		assert.NotEmpty(t, got.Reason, "a stop must say why")
	case <-time.After(3 * time.Second):
		t.Fatal("a judged-met request never asked to stop")
	}
}

func TestWatch_ALowScoreLetsItCarryOn(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	stop := Watch(bus, fakeAsker{answers: classify.Answers{
		"result_status": {Choice: StatusClean},
		"goal_achieved": {Noul: 0.2},
	}})
	defer stop()

	facts, unsub := bus.Subscribe(event.Only(event.CallJudgedKind, event.RequestStopKind))
	defer unsub()

	call := uuid.Must(uuid.NewV7())
	bus.Publish(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "go"})
	bus.Publish(event.CallProposed{Call: call, Tool: "bash"})
	bus.Publish(event.CallEnded{Call: call, Result: event.Result{Stdout: "partial\n"}})

	rec := <-facts
	judged, ok := rec.Event.(event.CallJudged)
	require.True(t, ok, "the judgement comes first")
	assert.Equal(t, call, judged.Call)

	select {
	case rec := <-facts:
		t.Fatalf("nothing else should follow, got %s", rec.Event.Kind())
	case <-time.After(300 * time.Millisecond):
	}
}

// A heuristic must not be able to end a request: only a real verdict
// carries that weight.
func TestWatch_AGuessNeverStopsATurn(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	stop := Watch(bus, nil) // no judge at all
	defer stop()

	intents, unsub := bus.Subscribe(event.Only(event.RequestStopKind))
	defer unsub()

	call := uuid.Must(uuid.NewV7())
	bus.Publish(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "go"})
	bus.Publish(event.CallProposed{Call: call, Tool: "bash"})
	bus.Publish(event.CallEnded{Call: call, Result: event.Result{Stdout: "done\n"}})

	select {
	case <-intents:
		t.Fatal("a heuristic asked a request to stop")
	case <-time.After(300 * time.Millisecond):
	}
}

// Shown only "bash", the judge classified a shape without knowing
// which command printed it.
func TestWatch_TheJudgeSeesTheCommand(t *testing.T) {
	bus := event.New()
	defer bus.Close()
	asker := recordingAsker{states: make(chan classify.State, 1)}
	defer Watch(bus, asker)()

	call := uuid.Must(uuid.NewV7())
	bus.Publish(event.TurnStarted{Turn: uuid.Must(uuid.NewV7()), N: 1, Prompt: "list it"})
	bus.Publish(event.CallProposed{Call: call, Tool: "bash", Args: map[string]any{"command": "ls -la"}})
	bus.Publish(event.CallEnded{Call: call, Result: event.Result{Stdout: "total 0\n"}})

	select {
	case s := <-asker.states:
		assert.Equal(t, "ls -la", s.(map[string]any)["command"])
	case <-time.After(3 * time.Second):
		t.Fatal("the judge was never asked")
	}
}

// recordingAsker keeps the state it was asked about.
type recordingAsker struct{ states chan classify.State }

func (r recordingAsker) Ask(_ context.Context, s classify.State, _ classify.Questions) (classify.Answers, classify.Usage, error) {
	r.states <- s
	return classify.Answers{}, classify.Usage{}, nil
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
