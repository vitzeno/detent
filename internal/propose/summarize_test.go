package propose

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarize_ReturnsProseNotAProposal(t *testing.T) {
	var body []byte
	srv := serveProposal(t, "Cloned the repo into /workspace and installed go 1.24.", http.StatusOK, &body)
	defer srv.Close()

	out, err := testProposer(srv).Summarize(context.Background(), []Message{
		{Role: RoleUser, Content: "clone the repo"},
		{Role: RoleAssistant, Content: `{"command":"git clone x"}`},
		{Role: RoleTool, Content: "Cloned."},
	})

	require.NoError(t, err)
	assert.Equal(t, "Cloned the repo into /workspace and installed go 1.24.", out)

	// The proposal schema would force the reply into a command shape,
	// which is the opposite of what a summary is for.
	var sent wireRequest
	require.NoError(t, json.Unmarshal(body, &sent))
	assert.Nil(t, sent.ResponseFormat)
}

func TestSummarize_SendsEveryTurnAsMaterial(t *testing.T) {
	var body []byte
	srv := serveProposal(t, "ok", http.StatusOK, &body)
	defer srv.Close()

	_, err := testProposer(srv).Summarize(context.Background(), []Message{
		{Role: RoleUser, Content: "first goal"},
		{Role: RoleTool, Content: "exit code 1"},
	})
	require.NoError(t, err)

	var sent wireRequest
	require.NoError(t, json.Unmarshal(body, &sent))
	require.Len(t, sent.Messages, 2, "one system prompt, one block of material")
	assert.Contains(t, sent.Messages[1].Content, "first goal")
	assert.Contains(t, sent.Messages[1].Content, "exit code 1")
}

func TestSummarize_EmptyTranscriptMakesNoCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("Summarize called the endpoint with nothing to summarize")
	}))
	defer srv.Close()

	out, err := testProposer(srv).Summarize(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestSummarize_CapsWhatItReturns(t *testing.T) {
	srv := serveProposal(t, strings.Repeat("x", MaxSummaryBytes*2), http.StatusOK, nil)
	defer srv.Close()

	out, err := testProposer(srv).Summarize(context.Background(), []Message{{Role: RoleUser, Content: "g"}})

	require.NoError(t, err)
	assert.LessOrEqual(t, len(out), MaxSummaryBytes+10)
}

func TestSummarize_ErrorsAreNamed(t *testing.T) {
	srv := serveProposal(t, "", http.StatusInternalServerError, nil)
	defer srv.Close()

	_, err := testProposer(srv).Summarize(context.Background(), []Message{{Role: RoleUser, Content: "g"}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "summarize")
}
