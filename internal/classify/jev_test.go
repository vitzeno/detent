package classify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJevJudge_Ask(t *testing.T) {
	tests := []struct {
		name         string
		questions    Questions
		startServer  bool
		serverStatus int
		serverBody   string
		wantErr      bool
		wantAnswers  Answers
		wantUsage    Usage
	}{
		{
			name: "choice question round-trips",
			questions: Questions{
				"next_action": {
					Instructions: "what next?",
					Choice:       &ChoiceQuestion{Criteria: map[string]any{"a": "do a", "b": "do b"}},
				},
			},
			startServer:  true,
			serverStatus: http.StatusOK,
			serverBody: `{
				"model": "jev-1.13.0",
				"answers": {"next_action": {"type":"choice","choice":"a","confidence":0.9,"probabilities":{"a":0.9,"b":0.1}}},
				"usage": {"input_tokens": 120, "output_tokens": 0}
			}`,
			wantAnswers: Answers{
				"next_action": {Choice: "a", Confidence: 0.9, Probabilities: map[string]float64{"a": 0.9, "b": 0.1}},
			},
			wantUsage: Usage{InputTokens: 120, Model: "jev-1.13.0"},
		},
		{
			name: "noul question round-trips, confidence stays zero-value",
			questions: Questions{
				"goal_achieved": {Instructions: "done?", Noul: &NoulQuestion{}},
			},
			startServer:  true,
			serverStatus: http.StatusOK,
			serverBody: `{
				"model": "jev-1.13.0",
				"answers": {"goal_achieved": {"type":"noul","noul":0.73}},
				"usage": {"input_tokens": 80, "output_tokens": 0}
			}`,
			wantAnswers: Answers{"goal_achieved": {Noul: 0.73}},
			wantUsage:   Usage{InputTokens: 80, Model: "jev-1.13.0"},
		},
		{
			name: "score question round-trips with legend",
			questions: Questions{
				"severity": {Instructions: "how bad?", Score: &ScoreQuestion{Levels: []string{"low", "mid", "high"}}},
			},
			startServer:  true,
			serverStatus: http.StatusOK,
			serverBody: `{
				"model": "jev-1.13.0",
				"answers": {"severity": {"type":"score","score":1.3,"confidence":0.54,
					"probabilities":{"0":0.0,"1":0.7,"2":0.3},"legend":{"0":"low","1":"mid","2":"high"}}},
				"usage": {"input_tokens": 60, "output_tokens": 0}
			}`,
			wantAnswers: Answers{"severity": {
				Score: 1.3, Confidence: 0.54,
				Probabilities: map[string]float64{"0": 0, "1": 0.7, "2": 0.3},
				Legend:        map[string]string{"0": "low", "1": "mid", "2": "high"},
			}},
			wantUsage: Usage{InputTokens: 60, Model: "jev-1.13.0"},
		},
		{
			name:         "non-200 status is an error",
			questions:    Questions{"x": {Instructions: "x", Noul: &NoulQuestion{}}},
			startServer:  true,
			serverStatus: http.StatusUnauthorized,
			serverBody:   `{"error": "invalid api key"}`,
			wantErr:      true,
		},
		{
			name:      "question with none of Choice/Noul/Score set is rejected before any request",
			questions: Questions{"broken": {Instructions: "??"}},
			wantErr:   true, // no server started: this must fail without making a network call
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j := &JevJudge{Model: DefaultModel, APIKey: "test-key"}

			if tt.startServer {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, http.MethodPost, r.Method)
					assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
					w.WriteHeader(tt.serverStatus)
					_, _ = w.Write([]byte(tt.serverBody))
				}))
				defer srv.Close()
				j.Endpoint = srv.URL
			}

			answers, usage, err := j.Ask(context.Background(), State(map[string]any{"goal": "test"}), tt.questions)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantAnswers, answers)
			assert.Equal(t, tt.wantUsage.InputTokens, usage.InputTokens)
			assert.Equal(t, tt.wantUsage.Model, usage.Model)
			assert.GreaterOrEqual(t, usage.LatencyMS, 0.0)
		})
	}
}

// TestJevJudge_Ask_RequestShape checks the outgoing JSON directly — this is
// what catches a Score criteria regression back to a map (PLAN.md §6's
// original sketch) instead of the ordered array TypeSafe's API actually
// requires, since the round-trip tests above only exercise responses.
func TestJevJudge_Ask_RequestShape(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{},"usage":{"input_tokens":0,"output_tokens":0}}`))
	}))
	defer srv.Close()

	j := &JevJudge{Model: DefaultModel, APIKey: "test-key", Endpoint: srv.URL}
	questions := Questions{
		"next_action":   {Instructions: "pick one", Choice: &ChoiceQuestion{Criteria: map[string]any{"a": "A"}}},
		"goal_achieved": {Instructions: "done?", Noul: &NoulQuestion{}},
		"severity":      {Instructions: "how bad?", Score: &ScoreQuestion{Levels: []string{"low", "high"}}},
	}
	_, _, err := j.Ask(context.Background(), State(map[string]any{"goal": "test"}), questions)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(capturedBody, &got))

	assert.Equal(t, "jev-1.13.0", got["model"])
	assert.Equal(t, map[string]any{"goal": "test"}, got["state"])

	qs := got["questions"].(map[string]any)

	choice := qs["next_action"].(map[string]any)
	assert.Equal(t, "choice", choice["type"])
	assert.Equal(t, map[string]any{"a": "A"}, choice["criteria"])

	noul := qs["goal_achieved"].(map[string]any)
	assert.Equal(t, "noul", noul["type"])
	assert.NotContains(t, noul, "criteria")

	score := qs["severity"].(map[string]any)
	assert.Equal(t, "score", score["type"])
	assert.Equal(t, []any{"low", "high"}, score["criteria"]) // ordered array, not a map
}
