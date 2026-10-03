// Package judge reads a finished Call: how it went, how to draw it,
// and whether the request now looks answered. A bus subscriber, so
// none of it gates execution.
package judge

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/viewgen"
)

// Result status, as the UI's own status package spells them.
const (
	StatusClean    = "clean_success"
	StatusWarnings = "success_with_warnings"
	StatusFailed   = "failed"
	StatusEmpty    = "empty"
)

// GoalMet is where the judge's goal-achieved score stops being an
// opinion and becomes a reason to stop asking for tools.
const GoalMet = 0.9

// maxJudging caps judgements in flight, since a Step's parallel Calls
// all finish together.
const maxJudging = 4

// judgeTimeout bounds one judgement, queueing included. A late verdict
// is worth less than the heuristic now.
const judgeTimeout = 20 * time.Second

// ResultJudge judges one finished Call.
type ResultJudge struct {
	Asker classify.Asker
	// Prompt is the request goal_achieved is judged against, kept per Turn by Watch.
	Prompt string
}

// Judge asks, and falls back to a heuristic when nothing answers. FromJudge
// tells the two apart, so the UI never presents a guess as a verdict.
func (j ResultJudge) Judge(ctx context.Context, command string, res event.Result) event.CallJudged {
	out := heuristic(res)
	state := map[string]any{"request": j.Prompt, "command": command, "output": output(res),
		"exit_code": res.ExitCode}
	if res.Err != "" {
		state["error"] = res.Err
	}
	answers, _, ok := classify.AskOrFallback(ctx, j.Asker, classify.State(state), resultQuestions())
	if !ok {
		return out
	}
	out.FromJudge = true
	if a, has := answers["result_status"]; has && a.Choice != "" {
		out.Status = a.Choice
	}
	if a, has := answers["render_kind"]; has && a.Choice != "" {
		out.RenderKind = a.Choice
	}
	if a, has := answers["attention"]; has {
		out.Attention = a.Noul
	}
	if a, has := answers["goal_achieved"]; has {
		out.GoalAchieved = a.Noul
	}
	return out
}

// Watch judges every finished Call and publishes what it decided. A high
// goal-achieved becomes a RequestStop, honoured at the next Step boundary.
func Watch(bus *event.Bus, asker classify.Asker) func() {
	facts, unsub := bus.Subscribe(event.Only(
		event.TurnStartedKind, event.TurnEndedKind, event.CallProposedKind, event.CallEndedKind))
	ctx, cancel := context.WithCancel(context.Background())
	w := &watcher{bus: bus, ctx: ctx, slots: make(chan struct{}, maxJudging)}
	looped := make(chan struct{})
	go func() {
		defer close(looped)
		var prompt string
		var turn uuid.UUID
		cmds := map[uuid.UUID]string{}
		for rec := range facts {
			switch v := rec.Event.(type) {
			case event.TurnStarted:
				prompt, turn = v.Prompt, v.Turn
				clear(cmds)
				w.setTurn(v.Turn)
			case event.TurnEnded:
				w.setTurn(uuid.Nil)
			case event.CallProposed:
				cmds[v.Call] = event.Command(v.Tool, v.Args)
			case event.CallEnded:
				j := ResultJudge{Asker: asker, Prompt: prompt}
				w.wg.Add(1)
				go w.publish(j, turn, v, cmds[v.Call])
				delete(cmds, v.Call)
			}
		}
	}()
	// Stopping cancels judgements in flight and waits for them.
	return func() {
		unsub()
		cancel()
		<-looped
		w.wg.Wait()
	}
}

// watcher is what the judging goroutines share with the loop.
type watcher struct {
	bus   *event.Bus
	ctx   context.Context
	slots chan struct{}
	wg    sync.WaitGroup

	mu sync.Mutex
	// turn is the Turn still running, Nil between Turns.
	turn uuid.UUID
	// stopped is the Turn already asked to stop, so it is asked once.
	stopped uuid.UUID
}

func (w *watcher) setTurn(id uuid.UUID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.turn = id
}

// shouldStop claims the one RequestStop a running Turn may get. A verdict
// for a Turn that has ended must not stop the next one.
func (w *watcher) shouldStop(turn uuid.UUID) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if turn == uuid.Nil || turn != w.turn || turn == w.stopped {
		return false
	}
	w.stopped = turn
	return true
}

// publish judges one Call on its own goroutine, so one slow judgement
// cannot hold up the others or the events behind them.
func (w *watcher) publish(j ResultJudge, turn uuid.UUID, done event.CallEnded, command string) {
	defer w.wg.Done()
	ctx, cancel := context.WithTimeout(w.ctx, judgeTimeout)
	defer cancel()
	var got event.CallJudged
	select {
	case w.slots <- struct{}{}:
		got = j.Judge(ctx, command, done.Result)
		<-w.slots
	case <-ctx.Done():
		got = heuristic(done.Result)
	}
	if w.ctx.Err() != nil {
		return // stopped: nothing is listening for this any more
	}
	got.Call = done.Call
	w.bus.Publish(got)
	if got.FromJudge && got.GoalAchieved >= GoalMet && w.shouldStop(turn) {
		w.bus.Publish(event.RequestStop{Turn: turn,
			Reason: "the judge reads the request as answered"})
	}
}

// heuristic is what a row reads as with no judge wired. It never claims
// a shape, only that something failed or was silent.
func heuristic(res event.Result) event.CallJudged {
	out := event.CallJudged{RenderKind: viewgen.KindText, GoalAchieved: -1}
	switch {
	case res.Err != "" || res.ExitCode != 0:
		out.Status, out.RenderKind, out.Attention = StatusFailed, viewgen.KindError, 0.9
	case strings.TrimSpace(res.Stdout+res.Stderr) == "":
		out.Status, out.Attention = StatusEmpty, 0.1
	default:
		out.Status, out.Attention = StatusClean, 0.1
	}
	return out
}

func resultQuestions() classify.Questions {
	return classify.Questions{
		"result_status": {
			Instructions: "Given the tool call and its captured output, how did it go? " +
				"Warnings means it succeeded but the output contains errors, retries or deprecations worth noticing.",
			Choice: &classify.ChoiceQuestion{Criteria: map[string]any{
				StatusClean:    "succeeded with clean, expected output",
				StatusWarnings: "succeeded but output contains warnings, errors or retries worth noticing",
				StatusFailed:   "failed: non-zero exit, or output showing it did not do its job",
				StatusEmpty:    "ran fine but produced no useful output",
			}},
		},
		"render_kind": viewgen.RenderKindQuestion(),
		"attention": {
			Instructions: "Does this outcome need the human's attention before continuing — " +
				"a failure, a surprise, a destructive result — rather than collapsing to one line?",
			Noul: &classify.NoulQuestion{},
		},
		"goal_achieved": {
			Instructions: "Has the human's request been answered given everything observed so far? " +
				"A second opinion: another model is choosing the tools, so judge only whether " +
				"the request itself now looks satisfied.",
			Noul: &classify.NoulQuestion{},
		},
	}
}

// stderrShare is the part of the sample kept for stderr's tail, so a long
// stdout cannot push the error that decides the status out of view.
const stderrShare = 1024

// output caps what the judge is shown. A judgement is about shape and
// outcome, neither of which needs a megabyte.
func output(r event.Result) string {
	outLimit := viewgen.MaxJudgeBytes - min(len(r.Stderr), stderrShare)
	stdout := headOf(r.Stdout, outLimit)
	stderr := r.Stderr
	if errLimit := viewgen.MaxJudgeBytes - min(len(r.Stdout), outLimit); len(stderr) > errLimit {
		stderr = "…[truncated]\n" + tailOf(stderr, errLimit)
	}
	if stdout != "" && stderr != "" {
		return stdout + "\n" + stderr
	}
	return stdout + stderr
}

// headOf and tailOf cut s to at most n bytes on a rune boundary.
func headOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "\n…[truncated]"
}

func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := len(s) - n
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return s[i:]
}
