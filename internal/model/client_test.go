package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

// Two calls in one response are two tool calls, with the prose beside them kept.
func TestComplete_DecodesAStep(t *testing.T) {
	const two = `{"choices":[{"message":{"content":"looking","tool_calls":[
		{"id":"c1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a.go\"}"}},
		{"id":"c2","type":"function","function":{"name":"list_dir","arguments":"{\"path\":\".\",\"all\":true}"}}
	]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":11,"completion_tokens":7},"model":"m"}`

	c, _ := serve(t, two)
	reply, used, err := c.Complete(context.Background(), []event.Message{{Role: event.RoleUser, Content: "go"}}, nil)
	require.NoError(t, err)

	require.Len(t, reply.Requests, 2)
	assert.Equal(t, "looking", reply.Text, "prose alongside calls is kept")
	assert.Equal(t, event.ToolRequest{ID: "c1", Name: "read_file", Args: map[string]any{"path": "a.go"}}, reply.Requests[0])
	assert.Equal(t, map[string]any{"path": ".", "all": true}, reply.Requests[1].Args)
	assert.Equal(t, []string{"c1", "c2"}, event.RequestIDs(reply.Requests))
	assert.Equal(t, 18, used.Tokens())
	assert.Positive(t, used.Latency)
}

func TestComplete_TextOnlyIsAStop(t *testing.T) {
	c, _ := serve(t, `{"choices":[{"message":{"content":"three files changed"},"finish_reason":"stop"}]}`)
	reply, _, err := c.Complete(context.Background(), []event.Message{{Role: event.RoleUser, Content: "go"}}, nil)
	require.NoError(t, err)
	assert.Empty(t, reply.Requests, "no calls means the Turn ends")
	assert.Equal(t, "three files changed", reply.Text)
}

// A malformed call must survive decoding. Dropping it leaves the
// assistant message naming an id nothing answers.
func TestComplete_KeepsCallsItCannotParse(t *testing.T) {
	c, _ := serve(t, `{"choices":[{"message":{"tool_calls":[
		{"id":"c1","type":"function","function":{"name":"bash","arguments":"{not json"}}
	]}}]}`)
	reply, _, err := c.Complete(context.Background(), []event.Message{{Role: event.RoleUser, Content: "go"}}, nil)
	require.NoError(t, err, "a bad argument string is the model's problem, not a transport error")
	require.Len(t, reply.Requests, 1)
	assert.Equal(t, "c1", reply.Requests[0].ID)
	assert.Contains(t, reply.Requests[0].Err, "not valid JSON")
	assert.NotNil(t, reply.Requests[0].Args, "args must be usable even when empty")
}

func TestComplete_TolerantOfEndpointQuirks(t *testing.T) {
	tests := []struct {
		name, body string
		check      func(*testing.T, Reply)
	}{
		{
			name: "reasoning_content when content is empty",
			body: `{"choices":[{"message":{"content":"","reasoning_content":"thought"}}]}`,
			check: func(t *testing.T, r Reply) {
				t.Helper()
				assert.Equal(t, "thought", r.Text)
				assert.True(t, r.Thinking, "reasoning is not an answer")
			},
		},
		{
			name:  "openrouter reasoning field",
			body:  `{"choices":[{"message":{"reasoning":"thought"}}]}`,
			check: func(t *testing.T, r Reply) { t.Helper(); assert.Equal(t, "thought", r.Text) },
		},
		{
			name:  "a call with no id gets one",
			body:  `{"choices":[{"message":{"tool_calls":[{"type":"function","function":{"name":"bash","arguments":"{}"}}]}}]}`,
			check: func(t *testing.T, r Reply) { t.Helper(); assert.Equal(t, "call_0", r.Requests[0].ID) },
		},
		{
			name:  "empty arguments are an empty map",
			body:  `{"choices":[{"message":{"tool_calls":[{"id":"c","type":"function","function":{"name":"list_dir","arguments":""}}]}}]}`,
			check: func(t *testing.T, r Reply) { t.Helper(); assert.Equal(t, map[string]any{}, r.Requests[0].Args) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := serve(t, tt.body)
			reply, _, err := c.Complete(context.Background(), []event.Message{{Role: event.RoleUser, Content: "go"}}, nil)
			require.NoError(t, err)
			tt.check(t, reply)
		})
	}
}

func TestComplete_SurfacesFailures(t *testing.T) {
	t.Run("an error object beats an empty choices list", func(t *testing.T) {
		c, _ := serve(t, `{"error":{"message":"model not found"}}`)
		_, _, err := c.Complete(context.Background(), []event.Message{{Role: event.RoleUser, Content: "go"}}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "model not found")
	})

	t.Run("an HTML error page is bounded", func(t *testing.T) {
		fastRetries(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			for range 5000 {
				_, _ = io.WriteString(w, "<html>")
			}
		}))
		defer srv.Close()
		c := &Client{BaseURL: srv.URL}
		_, _, err := c.Complete(context.Background(), []event.Message{{Role: event.RoleUser, Content: "go"}}, nil)
		require.Error(t, err)
		assert.Less(t, len(err.Error()), 500, "a big error page must not become the message")
	})

	t.Run("an empty transcript is refused before the wire", func(t *testing.T) {
		c := &Client{BaseURL: "http://127.0.0.1:1"}
		_, _, err := c.Complete(context.Background(), nil, nil)
		assert.ErrorContains(t, err, "empty transcript")
	})
}

// serve replies with body and captures the request that asked for it.
func serve(t *testing.T, body string) (*Client, *map[string]any) {
	t.Helper()
	got := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		assert.NoError(t, json.Unmarshal(raw, &got)) // require cannot stop a test from a handler
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, Model: "m"}, &got
}

// The request carries the key, the configured headers and the model,
// and Ping sends the same, or a gateway keyed by header fails at startup.
func TestClient_AuthenticatesEveryRequest(t *testing.T) {
	headers := make(chan http.Header, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL + "/", APIKey: "k", Headers: map[string]string{"X-API-Key": "g"}}

	require.NoError(t, c.Ping(context.Background()))
	_, _, err := c.Complete(context.Background(), []event.Message{{Role: event.RoleUser, Content: "go"}}, nil)
	require.NoError(t, err)

	for _, name := range []string{"ping", "complete"} {
		h := <-headers
		assert.Equal(t, "Bearer k", h.Get("Authorization"), name)
		assert.Equal(t, "g", h.Get("X-API-Key"), name)
	}
}

func TestPing_ANon200Fails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	err := (&Client{BaseURL: srv.URL}).Ping(context.Background())
	assert.ErrorContains(t, err, "HTTP 401")
}

// A transient failure is asked again and a bad request is not, and either
// way the caller can read the status without parsing a message.
func TestComplete_RetriesOnlyTransientFailures(t *testing.T) {
	fastRetries(t)
	tests := []struct {
		name     string
		codes    []int
		wantErr  int
		wantAsks int
	}{
		{"a 502 then an answer", []int{502, 200}, 0, 2},
		{"a 429 then an answer", []int{429, 200}, 0, 2},
		{"a 400 is final", []int{400, 200}, 400, 1},
		{"retries are bounded", []int{503, 503, 503, 200}, 503, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var asks atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.codes[asks.Add(1)-1])
				_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
			}))
			defer srv.Close()
			c := &Client{BaseURL: srv.URL}
			_, _, err := c.Complete(context.Background(), []event.Message{{Role: event.RoleUser, Content: "go"}}, nil)
			assert.Equal(t, int32(tt.wantAsks), asks.Load())
			if tt.wantErr == 0 {
				require.NoError(t, err)
				return
			}
			var se *StatusError
			require.ErrorAs(t, err, &se)
			assert.Equal(t, tt.wantErr, se.Code)
		})
	}
}

// fastRetries keeps a 5xx from making a test wait out the real backoff.
func fastRetries(t *testing.T) {
	t.Helper()
	old := retryWaits
	retryWaits = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { retryWaits = old })
}

func TestRetryAfter_ReadsSeconds(t *testing.T) {
	assert.Equal(t, 3*time.Second, retryAfter("3"))
	assert.Zero(t, retryAfter(""))
	assert.Zero(t, retryAfter("Wed, 21 Oct 2015 07:28:00 GMT"))
}

func TestComplete_NullArgumentsAreAnEmptyMap(t *testing.T) {
	c, _ := serve(t, `{"choices":[{"message":{"tool_calls":[{"id":"c","type":"function","function":{"name":"list_dir","arguments":"null"}}]}}]}`)
	reply, _, err := c.Complete(context.Background(), []event.Message{{Role: event.RoleUser, Content: "go"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{}, reply.Requests[0].Args, "null must not go back to the endpoint as null")
}

func TestReply_Unfinished(t *testing.T) {
	call := []event.ToolRequest{{ID: "c", Name: "bash"}}
	tests := []struct {
		name  string
		reply Reply
		want  bool
	}{
		{"an answer", Reply{Text: "done", Stop: "stop"}, false},
		{"no stop reason is taken at its word", Reply{Text: "done"}, false},
		{"end_turn", Reply{Text: "done", Stop: "end_turn"}, false},
		{"cut off", Reply{Text: "half", Stop: "length"}, true},
		{"provider error", Reply{Text: "half", Stop: "error"}, true},
		{"only whitespace", Reply{Text: " \n", Stop: "stop"}, true},
		{"only reasoning", Reply{Text: "thinking", Thinking: true, Stop: "stop"}, true},
		{"calls are never unfinished", Reply{Requests: call, Stop: "length"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.reply.Unfinished())
		})
	}
}
