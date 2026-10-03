package classify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
			name:      "question with none of Choice/Noul/Score set is rejected before any request",
			questions: Questions{"broken": {Instructions: "??"}},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.startServer {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, http.MethodPost, r.Method)
					assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
					w.WriteHeader(tt.serverStatus)
					_, _ = w.Write([]byte(tt.serverBody))
				}))
				defer srv.Close()
				opts = append(opts, WithEndpoint(srv.URL))
			}
			j := NewJevJudge("test-key", opts...)

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

func TestJevJudge_Ask_RequestShape(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{},"usage":{"input_tokens":0,"output_tokens":0}}`))
	}))
	defer srv.Close()

	j := NewJevJudge("test-key", WithEndpoint(srv.URL))
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

	qs := object(t, got["questions"])

	choice := object(t, qs["next_action"])
	assert.Equal(t, "choice", choice["type"])
	assert.Equal(t, map[string]any{"a": "A"}, choice["criteria"])

	noul := object(t, qs["goal_achieved"])
	assert.Equal(t, "noul", noul["type"])
	assert.NotContains(t, noul, "criteria")

	score := object(t, qs["severity"])
	assert.Equal(t, "score", score["type"])
	assert.Equal(t, []any{"low", "high"}, score["criteria"])
}

// Every way a request can fail is an error a caller can show, never an
// empty answer that reads as a verdict.
func TestJevJudge_Ask_Failures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		model   string
		check   func(t *testing.T, err error)
	}{
		{
			name: "a dropped connection",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
			},
		},
		{
			name:    "a body that is not JSON",
			handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{")) },
		},
		{
			name: "a body too big to be an answer",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(strings.Repeat(" ", maxResponseBytes+10)))
			},
			check: func(t *testing.T, err error) { t.Helper(); assert.ErrorContains(t, err, "over") },
		},
		{
			name: "a status says which one it was",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
			},
			check: func(t *testing.T, err error) {
				t.Helper()
				var he *HTTPError
				require.ErrorAs(t, err, &he)
				assert.Equal(t, http.StatusUnauthorized, he.Status)
			},
		},
		{
			name:  "no model is refused before any request",
			model: "-",
			check: func(t *testing.T, err error) { t.Helper(); assert.ErrorIs(t, err, ErrNoModel) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := tc.handler
			if handler == nil {
				handler = func(http.ResponseWriter, *http.Request) { t.Error("a request was sent") }
			}
			srv := httptest.NewServer(handler)
			defer srv.Close()
			j := NewJevJudge("k", WithEndpoint(srv.URL))
			if tc.model == "-" {
				j.model = ""
			}
			_, _, err := j.Ask(context.Background(), State(nil), Questions{"x": {Noul: &NoulQuestion{}}})
			require.Error(t, err)
			if tc.check != nil {
				tc.check(t, err)
			}
		})
	}
}

// An answer with no value, or of another kind than asked, is absent:
// read as zero it would show as "scope 0%", a confident verdict nobody gave.
func TestJevJudge_Ask_LeavesOutAnAnswerWithNoValue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model":"jev","answers":{
			"scope_risk":{"type":"noul"},
			"mutability":{"type":"noul","noul":0.4},
			"kept":{"type":"noul","noul":0.7}}}`))
	}))
	defer srv.Close()
	got, _, err := NewJevJudge("k", WithEndpoint(srv.URL)).Ask(context.Background(), State(nil), Questions{
		"scope_risk": {Noul: &NoulQuestion{}},
		"mutability": {Choice: &ChoiceQuestion{Criteria: map[string]any{"a": "A"}}},
		"kept":       {Noul: &NoulQuestion{}},
	})
	require.NoError(t, err)
	assert.Equal(t, Answers{"kept": {Noul: 0.7}}, got)
}

// A nil judge held in an interface is not a nil interface, and must
// still fail as an error rather than a panic.
func TestJevJudge_Ask_ANilJudgeIsAnError(t *testing.T) {
	var j *JevJudge
	var asker Asker = j
	_, _, err := asker.Ask(context.Background(), State(nil), Questions{})
	require.Error(t, err)

	_, _, ok := AskOrFallback(context.Background(), nil, State(nil), Questions{})
	assert.False(t, ok)
}

func object(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	require.True(t, ok, "%v is not an object", v)
	return m
}
