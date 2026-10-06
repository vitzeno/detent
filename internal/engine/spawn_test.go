package engine

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// A child has the parent's tools, MCP's included, but cannot spawn: one level deep.
func TestSpawn_AChildHasEveryToolButSpawn(t *testing.T) {
	cm := &childModel{}
	reg := tool.Standard(tool.SpawnAgent{})
	require.NoError(t, reg.Register(remoteTool{name: "srv__do"}))
	r := rigWithTools(t, event.New(), &fakeModel{replies: []model.Reply{{Requests: spawns(spawnCall("s1", "look", ""))}}},
		&fakeRunner{out: "ok\n"}, reg, WithChildModel(cm))
	r.run("explore")

	want := slices.DeleteFunc(reg.Names(), func(n string) bool { return n == event.ToolSpawnAgent })
	require.NotEmpty(t, cm.offered())
	for _, names := range cm.offered() {
		assert.Equal(t, want, names)
	}
}

func TestSpawn_ReportIsTheResultAndNothingElseReachesTheParent(t *testing.T) {
	cm := &childModel{script: map[string][]model.Reply{"trace the login": {
		{Text: "thinking privately", Requests: []event.ToolRequest{readCall("c1", "auth.go")}},
		{Text: "login is in auth.go:12", Stop: "stop"},
	}}}
	r := spawnRig(t, cm, spawns(spawnCall("s1", "trace the login", "trace")))
	r.run("how does login work")

	var sent []string
	for _, m := range r.model.lastSent() {
		sent = append(sent, m.Content)
	}
	all := strings.Join(sent, "\n")
	assert.Contains(t, all, "login is in auth.go:12", "the report is the spawn's result")
	assert.NotContains(t, all, "thinking privately", "a child's transcript stays its own")

	ended := r.of(event.AgentEndedKind)
	require.Len(t, ended, 1)
	assert.Equal(t, event.AgentDone, ended[0].(event.AgentEnded).Reason)
}

// The parent rereads what it cannot see was read, so a report ends with what
// the child read, from what ran: a failed read is not listed.
func TestSpawn_ReportListsWhatTheChildRead(t *testing.T) {
	cm := &childModel{script: map[string][]model.Reply{"trace": {
		{Requests: []event.ToolRequest{readCall("c1", "auth.go"),
			{ID: "c2", Name: "read_file", Args: map[string]any{"path": "db.go", "offset": 40, "max_lines": 20}}}},
		{Requests: []event.ToolRequest{readCall("c1", "auth.go")}},
		{Text: "login is in auth.go:12", Stop: "stop"},
	}}}
	r := rigWithTools(t, event.New(), &fakeModel{replies: []model.Reply{{Requests: spawns(spawnCall("s1", "trace", ""))}}},
		&fakeRunner{out: "output\n", fail: "db.go"}, tool.Standard(tool.SpawnAgent{}), WithChildModel(cm))
	r.run("go")

	report := toolAnswers(r.eng.messages())
	assert.Contains(t, report, "login is in auth.go:12")
	assert.Contains(t, report, "**Files this subagent read**")
	assert.Equal(t, 1, strings.Count(report, "auth.go lines 1-500"), "a file read twice is listed once")
	assert.NotContains(t, report, "db.go", "a read that failed backs nothing")
}

// Every fact a child produces names it, and its Steps carry the parent's
// Turn, which is what lets a resume end them.
func TestSpawn_ChildFactsNameTheChildAndTheParentsTurn(t *testing.T) {
	cm := &childModel{script: map[string][]model.Reply{"look": {
		{Requests: []event.ToolRequest{readCall("c1", "a.go")}},
	}}}
	r := spawnRig(t, cm, spawns(spawnCall("s1", "look", "")))
	end := r.run("go")

	started := r.of(event.AgentStartedKind)
	require.Len(t, started, 1)
	child := started[0].(event.AgentStarted).Agent
	assert.Equal(t, "agent-1", started[0].(event.AgentStarted).Name, "an unnamed child gets one")

	var childSteps int
	for _, ev := range r.of(event.StepStartedKind) {
		if s := ev.(event.StepStarted); s.Agent == child {
			childSteps++
			assert.Equal(t, end.Turn, s.Turn)
		}
	}
	assert.Equal(t, 2, childSteps)
	proposed := r.of(event.ToolCallProposedKind)
	assert.True(t, slices.ContainsFunc(proposed, func(e event.Event) bool {
		return e.(event.ToolCallProposed).Agent == child
	}), "the child's call names it")
}

func TestSpawn_RunsAtMostFourAtOnce(t *testing.T) {
	cm := &childModel{gate: make(chan struct{})}
	var calls []event.ToolRequest
	for i := range 6 {
		calls = append(calls, spawnCall(string(rune('a'+i)), "task "+string(rune('a'+i)), ""))
	}
	r := spawnRig(t, cm, calls)
	r.bus.Publish(event.SubmitPrompt{Text: "fan out"})

	require.Eventually(t, func() bool { return cm.running() == childSlots }, 3*time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, childSlots, cm.running(), "a fifth child ran beside four")
	close(cm.gate)
	r.await(event.TurnEndedKind)
	assert.Equal(t, childSlots, cm.most())
	assert.Len(t, r.of(event.AgentEndedKind), 6)
}

// A queued child already exists, so the human can see it and stop it.
func TestSpawn_QueuedChildIsStartedAndStoppable(t *testing.T) {
	cm := &childModel{gate: make(chan struct{})}
	var calls []event.ToolRequest
	for i := range childSlots + 1 {
		calls = append(calls, spawnCall(string(rune('a'+i)), "task "+string(rune('a'+i)), ""))
	}
	r := spawnRig(t, cm, calls)
	r.bus.Publish(event.SubmitPrompt{Text: "fan out"})
	r.awaitNth(event.AgentStartedKind, childSlots+1)
	require.Eventually(t, func() bool { return cm.running() == childSlots }, 3*time.Second, time.Millisecond)

	// The root's Step and each running child's, recorded before reading who has none.
	r.awaitNth(event.StepStartedKind, childSlots+1)
	queued := queuedAgent(t, r)
	r.bus.Publish(event.StopAgent{Agent: queued})
	stopped := r.await(event.AgentEndedKind).(event.AgentEnded)
	assert.Equal(t, queued, stopped.Agent, "the queued child ended first, while the others ran")
	assert.Equal(t, event.AgentStopped, stopped.Reason)

	close(cm.gate)
	r.await(event.TurnEndedKind)
}

func TestSpawn_RefusesPastMaxAgents(t *testing.T) {
	r := spawnRig(t, &childModel{}, spawns(spawnCall("a", "one", ""), spawnCall("b", "two", "")),
		WithAgentLimits(1, 0, 0))
	r.run("go")
	assert.Len(t, r.of(event.AgentStartedKind), 1)
	assert.Contains(t, toolAnswers(r.eng.messages()), "at most 1 subagents")
}

func TestSpawn_EndsWithAReportAtTheStepCap(t *testing.T) {
	cm := &childModel{final: "found half of it", forever: true}
	r := spawnRig(t, cm, spawns(spawnCall("s1", "dig", "")))
	r.run("go")

	report := toolAnswers(r.eng.messages())
	assert.Contains(t, report, "[partial: stopped at 30 steps]")
	assert.Contains(t, report, "found half of it", "the last call, with no tools, is the report")
	ended := r.await(event.AgentEndedKind).(event.AgentEnded)
	assert.Equal(t, event.AgentPartial, ended.Reason)
	assert.Equal(t, "stopped at 30 steps", ended.Why, "the log can say which limit it hit")
}

func TestSpawn_EndsWithAReportAtItsContextBudget(t *testing.T) {
	cm := &childModel{final: "what fit", forever: true}
	r := spawnRig(t, cm, spawns(spawnCall("s1", strings.Repeat("a long task ", 100), "")),
		WithAgentLimits(0, 100, 0))
	r.run("go")
	report := toolAnswers(r.eng.messages())
	assert.Contains(t, report, "[partial: ran out of context]")
	assert.Contains(t, report, "what fit")
}

func TestSpawn_EndsWithAReportAtItsTimeout(t *testing.T) {
	cm := &childModel{final: "what it had", hang: true}
	r := spawnRig(t, cm, spawns(spawnCall("s1", "slow", "")), WithAgentLimits(0, 0, 50*time.Millisecond))
	r.run("go")
	report := toolAnswers(r.eng.messages())
	assert.Contains(t, report, "[partial: ran for 50ms]")
	assert.Contains(t, report, "what it had")
}

// Only the root compacts and is measured. A child's notices say whose they are.
func TestSpawn_ChildNeverMeasuresAndItsNoticesCarryItsName(t *testing.T) {
	cm := &childModel{script: map[string][]model.Reply{"look": {{Stop: "length"}, {Text: "done", Stop: "stop"}}}}
	r := spawnRig(t, cm, spawns(spawnCall("s1", "look", "explore")))
	r.run("go")

	var rootSteps int
	for _, ev := range r.of(event.StepEndedKind) {
		if ev.(event.StepEnded).Agent == uuid.Nil {
			rootSteps++
		}
	}
	assert.Len(t, r.of(event.MeasuredKind), rootSteps+1, "once at startup, then once per root Step")
	assert.True(t, slices.ContainsFunc(r.of(event.NoticeKind), func(e event.Event) bool {
		return strings.HasPrefix(e.(event.Notice).Text, "explore: the model stopped")
	}))
}

// A task that mentions rm -rf describes work, and is not sent to TypeSafe.
func TestSpawn_IsNotAssessedByJev(t *testing.T) {
	j := &flagAll{}
	r := spawnRig(t, &childModel{}, spawns(spawnCall("s1", "find where rm -rf is called", "")),
		WithJudge(j, 0.5))
	r.run("go")

	assessed := r.of(event.ToolCallAssessedKind)
	require.NotEmpty(t, assessed)
	assert.False(t, assessed[0].(event.ToolCallAssessed).Risk.Dangerous)
	assert.Empty(t, j.seen(), "the task went to the judge")
}

func TestSpawn_UsageRollsUpToTheTurn(t *testing.T) {
	cm := &childModel{}
	r := spawnRig(t, cm, spawns(spawnCall("a", "one", ""), spawnCall("b", "two", ""), spawnCall("c", "three", "")))
	end := r.run("go")
	var steps event.Usage
	for _, ev := range r.of(event.StepEndedKind) {
		steps = steps.Add(ev.(event.StepEnded).Usage)
	}
	require.Positive(t, cm.calls())
	assert.Equal(t, steps.Tokens(), end.Usage.Tokens(), "every agent's spend is the Turn's, counted once")
}

func TestSpawn_ReadOnlyChildLeavesTheTurnUnchanged(t *testing.T) {
	r := spawnRig(t, &childModel{}, spawns(spawnCall("s1", "look", "")), WithFinishCheck(true))
	r.run("go")
	for _, m := range r.eng.messages() {
		assert.NotEqual(t, finishNote, m.Content, "a spawn that only read was asked to check its work")
	}
}

// Asked about a read, a child waits alone: its sibling finishes meanwhile.
func TestSpawn_AFlaggedChildCallBlocksOnlyThatChild(t *testing.T) {
	cm := &childModel{script: map[string][]model.Reply{"risky": {
		{Requests: []event.ToolRequest{readCall("c1", "secrets.env")}},
	}}}
	// A caller's hooks end every agent's chain, a child's too.
	r := spawnRig(t, cm, spawns(spawnCall("a", "risky", ""), spawnCall("b", "calm", "")),
		WithAssessor(flagReads{}))
	r.bus.Publish(event.SubmitPrompt{Text: "go"})

	asked := r.await(event.ApprovalAskedKind).(event.ApprovalAsked)
	assert.NotEqual(t, uuid.Nil, asked.Agent, "the question names the child")
	calm := r.await(event.AgentEndedKind).(event.AgentEnded)
	assert.NotEqual(t, asked.Agent, calm.Agent, "the sibling finished while the question waited")

	r.bus.Publish(event.ResolveApproval{ToolCall: asked.ToolCall, Approved: true})
	r.await(event.TurnEndedKind)
	assert.Len(t, r.of(event.AgentEndedKind), 2)
}

func TestStopAgent_EndsOneChildAndLeavesItsSiblings(t *testing.T) {
	cm := &childModel{final: "half of it", hangOn: "stuck"}
	r := spawnRig(t, cm, spawns(spawnCall("a", "stuck", ""), spawnCall("b", "fine", "")))
	r.bus.Publish(event.SubmitPrompt{Text: "go"})
	fine := r.await(event.AgentEndedKind).(event.AgentEnded)
	require.Equal(t, event.AgentDone, fine.Reason)

	// Both started, though under load the stuck one can start after the other ends.
	r.awaitNth(event.AgentStartedKind, 2)
	var stuck uuid.UUID
	for _, ev := range r.of(event.AgentStartedKind) {
		if a := ev.(event.AgentStarted); a.Task == "stuck" {
			stuck = a.Agent
		}
	}
	require.NotEqual(t, uuid.Nil, stuck)
	r.bus.Publish(event.StopAgent{Agent: stuck})
	end := r.await(event.TurnEndedKind).(event.TurnEnded)

	assert.Equal(t, event.EndDone, end.Reason, "the parent carried on")
	report := toolAnswers(r.eng.messages())
	assert.Contains(t, report, "[the human stopped this subagent]")
	assert.Contains(t, report, "half of it", "a stopped child still reports what it found")
}

// A stop sent the moment a child is announced must still reach it.
func TestStopAgent_SentOnSeeingTheChildIsNotLost(t *testing.T) {
	cm := &childModel{hang: true}
	r := spawnRig(t, cm, spawns(spawnCall("a", "forever", "")))
	// Handled before the spawn goes on, as a loaded machine can order it.
	r.eng.afterAnnounce = func(agent uuid.UUID) {
		r.bus.Publish(event.StopAgent{Agent: agent})
		r.dispatched()
	}
	end := r.run("go")
	assert.Equal(t, event.EndDone, end.Reason)
	assert.Equal(t, event.AgentStopped, r.await(event.AgentEndedKind).(event.AgentEnded).Reason)
}

func TestAbort_EndsEveryAgentsOpenToolCalls(t *testing.T) {
	cm := &childModel{hang: true}
	r := spawnRig(t, cm, spawns(spawnCall("a", "one", ""), spawnCall("b", "two", "")))
	r.bus.Publish(event.SubmitPrompt{Text: "go"})
	require.Eventually(t, func() bool { return cm.running() == 2 }, 3*time.Second, time.Millisecond)
	r.bus.Publish(event.Abort{})
	r.await(event.TurnEndedKind)

	assert.Len(t, r.of(event.ToolCallEndedKind), len(r.of(event.ToolCallProposedKind)))
	for _, ev := range r.of(event.AgentEndedKind) {
		assert.Equal(t, event.AgentAborted, ev.(event.AgentEnded).Reason)
	}
	answered(t, r.eng)
}

// A child runs whichever shell the session does.
func TestSpawn_AChildRunsTheSessionsShell(t *testing.T) {
	cm := &childModel{}
	r := rigWithTools(t, event.New(), &fakeModel{replies: []model.Reply{{Requests: spawns(spawnCall("a", "run the tests", ""))}}},
		&fakeRunner{out: "ok\n"}, tool.StandardFor(tool.PowerShell{}, tool.SpawnAgent{}), WithChildModel(cm))
	r.run("go")
	for _, names := range cm.offered() {
		assert.Contains(t, names, "powershell")
		assert.NotContains(t, names, "bash")
	}
}

// A child's call to a server is confirmed like the root's, then runs there.
func TestSpawn_AChildsMCPCallIsConfirmedThenInvoked(t *testing.T) {
	cm := &childModel{script: map[string][]model.Reply{"file it": {{Requests: []event.ToolRequest{remoteCall("m1")}}}}}
	in := &fakeInvoker{out: capture.Result{Stdout: "issue 42\n"}}
	reg := tool.Standard(tool.SpawnAgent{})
	require.NoError(t, reg.Register(remoteTool{name: "srv__do"}))
	r := rigWithTools(t, event.New(), &fakeModel{replies: []model.Reply{{Requests: spawns(spawnCall("a", "file it", ""))}}},
		&fakeRunner{}, reg, WithChildModel(cm), WithInvoker(in))
	r.bus.Publish(event.SubmitPrompt{Text: "go"})

	asked := r.await(event.ApprovalAskedKind).(event.ApprovalAsked)
	assert.NotEqual(t, uuid.Nil, asked.Agent)
	assert.Empty(t, in.calls(), "it ran before anyone answered")
	r.bus.Publish(event.ResolveApproval{ToolCall: asked.ToolCall, Approved: true})
	r.await(event.TurnEndedKind)
	require.Len(t, in.calls(), 1)
	assert.Equal(t, "srv", in.calls()[0].Executor)
}

// The main agent decides what runs together, so children run at once.
func TestSpawn_ChildrenThatRunCommandsRunTogether(t *testing.T) {
	cm := &childModel{gate: make(chan struct{})}
	r := spawnRig(t, cm, spawns(spawnCall("a", "test the api", ""), spawnCall("b", "test the ui", "")))
	r.bus.Publish(event.SubmitPrompt{Text: "go"})
	require.Eventually(t, func() bool { return cm.running() == 2 }, 3*time.Second, time.Millisecond)
	close(cm.gate)
	r.await(event.TurnEndedKind)
}

// A child's command goes through the same chain as the root's, and its question
// names the child.
func TestSpawn_AChildsCommandIsFlaggedLikeTheRoots(t *testing.T) {
	cm := &childModel{script: map[string][]model.Reply{"clean up": {
		{Requests: []event.ToolRequest{bashCall("c1", "rm -rf build")}},
	}}}
	r := spawnRig(t, cm, spawns(spawnCall("a", "clean up", "")))
	r.bus.Publish(event.SubmitPrompt{Text: "go"})
	asked := r.await(event.ApprovalAskedKind).(event.ApprovalAsked)
	assert.NotEqual(t, uuid.Nil, asked.Agent)
	assert.Contains(t, event.Command(asked.Tool, asked.Args), "rm -rf build")
	r.bus.Publish(event.ResolveApproval{ToolCall: asked.ToolCall, Approved: false})
	r.await(event.TurnEndedKind)
	assert.Empty(t, r.runner.commands(), "declined, so nothing ran")
}

// A child's command can change the workspace, so the root checks its work. One
// that only read leaves the request as it was.
func TestSpawn_AChildsCommandTriggersTheRootsFinishCheck(t *testing.T) {
	for _, tt := range []struct {
		name  string
		call  event.ToolRequest
		check bool
	}{{"ran a command", bashCall("c1", "go test ./..."), true}, {"only read", readCall("c1", "go.mod"), false}} {
		t.Run(tt.name, func(t *testing.T) {
			cm := &childModel{script: map[string][]model.Reply{"check": {{Requests: []event.ToolRequest{tt.call}}}}}
			r := spawnRig(t, cm, spawns(spawnCall("a", "check", "")), WithFinishCheck(true))
			r.run("go")
			checked := slices.ContainsFunc(r.eng.messages(), func(m event.Message) bool { return m.Content == finishNote })
			assert.Equal(t, tt.check, checked)
		})
	}
}

func TestSpawn_AChildsCommandIsBoundedByTheTimeout(t *testing.T) {
	cm := &childModel{script: map[string][]model.Reply{"wait": {{Requests: []event.ToolRequest{bashCall("c1", "sleep 600")}}}}}
	runner := &fakeRunner{hold: make(chan struct{})}
	r := rigWithTools(t, event.New(), &fakeModel{replies: []model.Reply{{Requests: spawns(spawnCall("a", "wait", ""))}}},
		runner, tool.Standard(tool.SpawnAgent{}), WithChildModel(cm), WithCommandTimeout(50*time.Millisecond))
	r.run("go")
	var stopped bool
	for _, ev := range r.of(event.ToolCallEndedKind) {
		stopped = stopped || strings.Contains(ev.(event.ToolCallEnded).Result.Err, "stopped after 50ms")
	}
	assert.True(t, stopped, "the child's command was not stopped by the command timeout")
}

// spawnRig is a rig whose parent spawns once with calls, then finishes, and
// whose children run on cm.
func spawnRig(t *testing.T, cm *childModel, calls []event.ToolRequest, opts ...Option) *rig {
	t.Helper()
	return rigWithTools(t, event.New(), &fakeModel{replies: []model.Reply{{Requests: calls}}},
		&fakeRunner{out: "output\n"}, tool.Standard(tool.SpawnAgent{}), append(opts, WithChildModel(cm))...)
}

func spawns(calls ...event.ToolRequest) []event.ToolRequest { return calls }

func spawnCall(id, task, name string) event.ToolRequest {
	return event.ToolRequest{ID: id, Name: event.ToolSpawnAgent, Args: map[string]any{"task": task, "name": name}}
}

// toolAnswers is every tool result the root's transcript holds, joined.
func toolAnswers(msgs []event.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		if m.Role == event.RoleTool {
			b.WriteString(m.Content + "\n")
		}
	}
	return b.String()
}

// queuedAgent is the one child that has started but taken no Step.
func queuedAgent(t *testing.T, r *rig) uuid.UUID {
	t.Helper()
	stepped := map[uuid.UUID]bool{}
	for _, ev := range r.of(event.StepStartedKind) {
		stepped[ev.(event.StepStarted).Agent] = true
	}
	var out []uuid.UUID
	for _, ev := range r.of(event.AgentStartedKind) {
		if a := ev.(event.AgentStarted).Agent; !stepped[a] {
			out = append(out, a)
		}
	}
	require.Len(t, out, 1)
	return out[0]
}

// childModel answers each child by its task, since children run at once and
// one shared script would hand a child another's reply.
type childModel struct {
	mu     sync.Mutex
	script map[string][]model.Reply
	// final answers a call with no tools, which is the report call.
	final string
	// gate, when set, holds every call with tools until it closes.
	gate chan struct{}
	// forever keeps asking for a read, hang waits for the ctx to end, and
	// hangOn does that only for the task it names.
	forever      bool
	hang         bool
	hangOn       string
	n, inFlight  int
	peak         int
	toolsOffered [][]string
}

func (m *childModel) Complete(ctx context.Context, msgs []event.Message, tools []map[string]any) (model.Reply, event.Usage, error) {
	task := msgs[0].Content
	m.mu.Lock()
	m.n++
	m.inFlight++
	m.peak = max(m.peak, m.inFlight)
	if tools != nil {
		m.toolsOffered = append(m.toolsOffered, schemaNames(tools))
	}
	gate := m.gate
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.inFlight--
		m.mu.Unlock()
	}()
	used := event.Usage{PromptTokens: 1, CompletionTokens: 1}

	if tools == nil {
		return model.Reply{Text: m.final, Stop: "stop"}, used, nil
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return model.Reply{}, event.Usage{}, ctx.Err()
		}
	}
	if m.hang || (m.hangOn != "" && m.hangOn == task) {
		<-ctx.Done()
		return model.Reply{}, event.Usage{}, ctx.Err()
	}
	if m.forever {
		return model.Reply{Requests: []event.ToolRequest{readCall("r", "big.go")}}, used, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if next := m.script[task]; len(next) > 0 {
		m.script[task] = next[1:]
		return next[0], used, nil
	}
	return model.Reply{Text: "report on " + task, Stop: "stop"}, used, nil
}

func (m *childModel) running() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inFlight
}

func (m *childModel) most() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.peak
}

func (m *childModel) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.n
}

func (m *childModel) offered() [][]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.toolsOffered)
}

func schemaNames(tools []map[string]any) []string {
	var out []string
	for _, s := range tools {
		if fn, ok := s["function"].(map[string]any); ok {
			out = append(out, fn["name"].(string))
		}
	}
	return out
}

// flagAll is a judge that flags everything it is shown.
type flagAll struct {
	mu   sync.Mutex
	cmds []string
}

func (j *flagAll) Assess(_ context.Context, cmd string, _ float64) (event.Risk, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.cmds = append(j.cmds, cmd)
	return event.Risk{Dangerous: true, ScopeRisk: 1}, nil
}

func (j *flagAll) seen() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.cmds)
}

// flagReads flags every read_file, so a child's read can be made to ask.
type flagReads struct{}

func (flagReads) Name() string { return "flag-reads" }

func (flagReads) Assess(_ context.Context, c tool.Call, _ event.Risk) (event.Risk, error) {
	r := event.UnknownRisk()
	r.Dangerous = c.Tool == "read_file"
	return r, nil
}
