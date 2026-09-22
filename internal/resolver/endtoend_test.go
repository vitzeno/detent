package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/agent"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/usage"
	"github.com/vitzeno/detent/internal/viewgen"
	"github.com/vitzeno/detent/ui"
	"github.com/vitzeno/detent/viewspec"
)

// One goal from the top, through the parts ui cannot reach: a real
// OpenAI-compatible endpoint, a real Session, a real Resolver, and a
// real generator authoring the view the pane would draw. ui's own
// tests cover the half above Driver against its fake.

// Long enough that the no-Jev heuristic calls it a log rather than
// inline_short, which is the set viewgen deliberately skips: a few
// lines have no view worth a model call.
var psOutput = func() string {
	out := "  PID TTY           TIME CMD\n"
	for i := range 14 {
		out += fmt.Sprintf("  %3d ttys%03d    0:0%d.412 proc%02d\n", 501+i, i, i%9, i)
	}
	return out
}()

// fakeEndpoint answers proposals and view specs on the one
// /chat/completions route a real backend uses, dispatching on the
// schema name the request asks for rather than on call order: the
// proposer and the generator interleave.
// fakeEndpoint answers proposals only. A view is composed from the
// judge's answers now, so the proposer is never asked for one.
func fakeEndpoint(t *testing.T, proposals []string) *httptest.Server {
	t.Helper()
	n := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&struct{}{}))
		reply := proposals[min(n, len(proposals)-1)]
		n++
		out, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": reply}}},
			"usage":   map[string]any{"prompt_tokens": 12, "completion_tokens": 4},
			"model":   "fake",
		})
		require.NoError(t, err)
		_, _ = w.Write(out)
	}))
}

func TestEndToEnd_GoalRunsAndItsOutputGetsAView(t *testing.T) {
	// pgrep, not ps: anything detent ships a seed for never reaches
	// the generator, and this test is about the generator.
	const proposal = `{"command":"pgrep -al someone","rationale":"look at processes",` +
		`"done":false,"summary":"","file":""}`
	const done = `{"command":"","rationale":"","done":true,` +
		`"summary":"Listed the processes.","file":""}`
	srv := fakeEndpoint(t, []string{proposal, done})
	defer srv.Close()

	proposer := propose.New(propose.WithBaseURL(srv.URL), propose.WithModel("fake"))
	sess := agent.New(proposer, nil,
		agent.WithRunners(agent.SingleRunner{Runner: runFunc(
			func(context.Context, string, chan<- host.StreamEvent) (host.Result, error) {
				return host.Result{Stdout: psOutput}, nil
			})}),
		agent.WithStats(usage.New()))

	drv := New(sess)
	drv.Views = &viewgen.Generator{Judge: composingJudge{},
		Store: &viewgen.Store{Dir: t.TempDir()}}

	ctx := context.Background()
	res, err := drv.BeginGoal(ctx, "what is running")
	require.NoError(t, err)

	// Step one: propose, record, execute.
	p, pre, used, err := drv.ProposeNext(ctx, "what is running")
	require.NoError(t, err)
	require.False(t, p.Done)
	assert.Equal(t, "pgrep -al someone", p.Command)

	step := drv.RecordStep(res, p, pre, used, 0)
	ec, err := drv.Execute(ctx, res, step, p, pre, nil)
	require.NoError(t, err)
	require.Equal(t, psOutput, ec.Result.Stdout)

	post := drv.JudgeResult(ctx, "what is running", p.Command, ec.Result, step)
	require.Equal(t, ui.KindText, post.RenderKind,
		"no Jev wired, so the heuristic classifies by length; a log is worth a view")

	// The pane's half: a generated spec that actually draws this output.
	got, ok := drv.GenerateView(ctx, p.Command, ec.Result.Stdout, ec.Result.ExitCode, post.RenderKind)
	require.True(t, ok, "the judge composed a view")
	require.NotNil(t, got.Spec)
	assert.Equal(t, ui.ViewGenerated, got.Source,
		"and the pane will say a model had a hand in this framing")

	compiled, err := viewspec.Compile(*got.Spec)
	require.NoError(t, err)
	bound, err := compiled.Bind(ec.Result.Stdout)
	require.NoError(t, err)
	drawn, err := bound.Draw(viewspec.Frame{Width: 50, Paint: viewspec.Plain()})
	require.NoError(t, err)
	require.Len(t, drawn.Lines, 15, "a header and fourteen processes")
	assert.Contains(t, drawn.Lines[0], "PID")
	assert.Contains(t, drawn.Lines[1], "501")

	// A composed view carries no on_enter: a command template is free
	// text, and composition only ever picks from a list. Saved and
	// shipped specs still have one, so enter still acts on those.
	_, ok = bound.Action(viewspec.Frame{Cursor: 0})
	assert.False(t, ok)

	// Step two closes the goal.
	p, _, _, err = drv.ProposeNext(ctx, "what is running")
	require.NoError(t, err)
	require.True(t, p.Done)
	drv.RecordDone(res, p)
	assert.Equal(t, ui.EndDone, res.End)
}

// A Driver with no generator wired leaves ui on its built-in
// rendering. There is no "off" mode, but a nil generator is still a
// state the boundary has to survive.
func TestEndToEnd_NoGeneratorLeavesTheBuiltinRendering(t *testing.T) {
	srv := fakeEndpoint(t, []string{`{"command":"ps","rationale":"r","done":false,"summary":"","file":""}`})
	defer srv.Close()

	proposer := propose.New(propose.WithBaseURL(srv.URL), propose.WithModel("fake"))
	sess := agent.New(proposer, nil,
		agent.WithRunners(agent.SingleRunner{Runner: runFunc(
			func(context.Context, string, chan<- host.StreamEvent) (host.Result, error) {
				return host.Result{Stdout: psOutput}, nil
			})}),
		agent.WithStats(usage.New()))

	drv := New(sess) // no Views generator at all
	_, ok := drv.GenerateView(context.Background(), "ps", psOutput, 0, ui.KindText)
	assert.False(t, ok, "ui draws its built-in spec for the judged kind")
}

// Asked and refused reaches ui as a distinct outcome; never asked does
// not, because there is nothing to tell the human about.
func TestEndToEnd_ARefusedGenerationIsReportedAsDeclined(t *testing.T) {
	srv := fakeEndpoint(t, []string{`{"command":"df -h","rationale":"r",` +
		`"done":false,"summary":"","file":""}`})
	defer srv.Close()

	proposer := propose.New(propose.WithBaseURL(srv.URL), propose.WithModel("fake"))
	drv := New(agent.New(proposer, nil))
	// A judge that answers nothing composes nothing.
	drv.Views = &viewgen.Generator{Judge: silentJudge{},
		Store: &viewgen.Store{Dir: t.TempDir()}}

	got, ok := drv.GenerateView(context.Background(), "df -h", psOutput, 0, ui.KindTable)
	assert.False(t, ok)
	assert.Equal(t, ui.ViewDeclined, got.Source, "the human is told it was tried")

	// Too short to be worth asking: nothing was attempted, nothing said.
	got, ok = drv.GenerateView(context.Background(), "df -h", "one\ntwo\n", 0, ui.KindTable)
	assert.False(t, ok)
	assert.Empty(t, got.Source)
}

// composingJudge answers the composition's questions the way a real
// one would for column output: a header on the first line, columns,
// and a table over them.
type composingJudge struct{}

func (composingJudge) Ask(_ context.Context, _ classify.State,
	qs classify.Questions) (classify.Answers, classify.Usage, error) {
	say := map[string]string{
		"header_line": "0", "parse_kind": "columns", "body": "table", "summary": "none",
	}
	out := classify.Answers{}
	for name, q := range qs {
		choice, ok := say[name]
		if !ok || q.Choice == nil {
			continue
		}
		if _, valid := q.Choice.Criteria[choice]; !valid {
			return nil, classify.Usage{}, fmt.Errorf("%q not offered for %q", choice, name)
		}
		out[name] = classify.Answer{Choice: choice}
	}
	return out, classify.Usage{}, nil
}

type silentJudge struct{}

func (silentJudge) Ask(context.Context, classify.State, classify.Questions) (classify.Answers, classify.Usage, error) {
	return nil, classify.Usage{}, errors.New("no judge today")
}
