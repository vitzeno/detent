package event

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A session saved before the rename still resumes: every old kind decodes,
// and the ids it carried land in the renamed fields rather than vanishing.
func TestDecode_ReadsSessionsSavedUnderTheOldNames(t *testing.T) {
	id := uuid.MustParse("01a101e1-fc16-7dcb-a97a-a4d28284b10d")
	for _, c := range []struct {
		kind    Kind
		payload string
		want    Event
	}{
		{"call.proposed", `{"Call":"` + id.String() + `","Tool":"bash"}`, ToolCallProposed{ToolCall: id, Tool: "bash"}},
		{"call.assessed", `{"Call":"` + id.String() + `"}`, ToolCallAssessed{ToolCall: id}},
		{"call.approval", `{"Call":"` + id.String() + `"}`, ApprovalAsked{ToolCall: id}},
		{"call.started", `{"Call":"` + id.String() + `","Runner":"host"}`, ToolCallStarted{ToolCall: id, Runner: "host"}},
		{"call.ended", `{"Call":"` + id.String() + `","Result":{"ExitCode":2}}`, ToolCallEnded{ToolCall: id, Result: Result{ExitCode: 2}}},
		{"call.judged", `{"Call":"` + id.String() + `","Status":"ok"}`, ToolCallJudged{ToolCall: id, Status: "ok"}},
		{"call.view", `{"Call":"` + id.String() + `","Source":"saved"}`, ViewReady{ToolCall: id, Source: "saved"}},
		{"shell.started", `{"Shell":"` + id.String() + `","Command":"ls"}`, UserCommandStarted{UserCommand: id, Command: "ls"}},
		{"shell.ended", `{"Shell":"` + id.String() + `"}`, UserCommandEnded{UserCommand: id}},
		{StepEndedKind, `{"Calls":3,"Stop":"stop"}`, StepEnded{ToolCalls: 3, Stop: "stop"}},
		{BoundReachedKind, `{"Steps":100,"Calls":140}`, BoundReached{Steps: 100, ToolCalls: 140}},
		{AppendedKind,
			`{"Messages":[{"Role":"assistant","Calls":[{"ID":"c1","Name":"bash","Args":{"Call":"kept"}}]},{"Role":"tool","CallID":"c1","Content":"\"Calls\": left alone"}]}`,
			Appended{Messages: []Message{
				{Role: RoleAssistant, Requests: []ToolRequest{{ID: "c1", Name: "bash", Args: map[string]any{"Call": "kept"}}}},
				{Role: RoleTool, RequestID: "c1", Content: `"Calls": left alone`},
			}}},
	} {
		t.Run(string(c.kind), func(t *testing.T) {
			got, err := Decode(c.kind, []byte(c.payload))
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

// A record written now is not touched on the way back in.
func TestDecode_PassesCurrentRecordsThrough(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	for _, e := range []Event{
		ToolCallEnded{ToolCall: id},
		StepEnded{ToolCalls: 2},
		Appended{Messages: []Message{{Role: RoleTool, RequestID: "x", Content: "Calls"}}},
	} {
		raw, err := Encode(e)
		require.NoError(t, err)
		got, err := Decode(e.Kind(), raw)
		require.NoError(t, err)
		assert.Equal(t, e, got)
	}
}
