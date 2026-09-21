package propose

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serveProposal(t *testing.T, content string, status int, captured *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/chat/completions", r.URL.Path)
		if captured != nil {
			*captured, _ = io.ReadAll(r.Body)
		}
		w.WriteHeader(status)
		if status != http.StatusOK {
			_, _ = w.Write([]byte(`{"error": "boom"}`))
			return
		}
		resp := map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": content}}},
		}
		raw, _ := json.Marshal(resp)
		_, _ = w.Write(raw)
	}))
}

func testProposer(srv *httptest.Server) *OpenAIProposer {
	return New(WithBaseURL(srv.URL), WithModel("test-model"), WithAPIKey("test-key"))
}

func TestDefaultSystemPrompt_StatesEnvironmentOnce(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)

	prompt := defaultSystemPrompt(LocalEnvironment())
	assert.Contains(t, prompt, runtime.GOOS+"/"+runtime.GOARCH,
		"OS/arch must be stated so the proposer doesn't have to discover it via uname")
	assert.Contains(t, prompt, cwd,
		"cwd must be stated so the proposer doesn't have to discover it via pwd")
	assert.Contains(t, prompt, "does not carry to the next command",
		"must warn that a bare cd has no effect across sh -c invocations")
}

// The preamble is the only thing telling the proposer which OS's flags
// to write and what survives. Describing detent's own machine while
// commands run in a container is how you get BSD flags in an Ubuntu
// container, so the sandbox's facts have to win.
func TestSystemPrompt_DescribesWhereCommandsActuallyRun(t *testing.T) {
	p := New(WithEnvironment(Environment{
		OS: "linux", Arch: "arm64", Dir: "/workspace",
		Sandboxed: true, Network: true, Undoable: true,
	}))
	got := p.systemPrompt()

	assert.Contains(t, got, "linux/arm64")
	assert.Contains(t, got, "/workspace")
	assert.Contains(t, got, "in a container")
	assert.Contains(t, got, "network is reachable")
	assert.Contains(t, got, "undo a step")
	assert.NotContains(t, got, runtime.GOOS+"/"+runtime.GOARCH,
		"detent's own OS must not leak in when commands run elsewhere")

	// And the opposite posture reads the opposite way.
	offline := New(WithEnvironment(Environment{OS: "linux", Arch: "amd64", Dir: "/workspace", Sandboxed: true}))
	assert.Contains(t, offline.systemPrompt(), "no network")
	assert.NotContains(t, offline.systemPrompt(), "undo a step",
		"don't promise an undo that isn't wired")
}

func TestOpenAIProposer_SystemPromptOverrideSkipsEnvironmentPreamble(t *testing.T) {
	var captured []byte
	srv := serveProposal(t, `{"command":"ls","rationale":"r","done":false,"summary":""}`, http.StatusOK, &captured)
	defer srv.Close()

	p := New(WithBaseURL(srv.URL), WithModel("test-model"), WithSystemPrompt("custom prompt, nothing else"))
	_, _, err := p.Propose(context.Background(), []Message{{Role: RoleUser, Content: "goal"}})
	require.NoError(t, err)

	var req wireRequest
	require.NoError(t, json.Unmarshal(captured, &req))
	require.NotEmpty(t, req.Messages)
	assert.Equal(t, "custom prompt, nothing else", req.Messages[0].Content,
		"an explicit SystemPrompt takes full control, same as before — no auto-prepended environment block")
}

func TestOpenAIProposer_CommandRoundTrip(t *testing.T) {
	var captured []byte
	srv := serveProposal(t,
		`{"command": "ls -la", "rationale": "list files first", "done": false, "summary": ""}`,
		http.StatusOK, &captured)
	defer srv.Close()

	got, _, err := testProposer(srv).Propose(context.Background(), []Message{
		{Role: RoleUser, Content: "what files are here?"},
	})
	require.NoError(t, err)
	assert.Equal(t, Proposal{Command: "ls -la", Rationale: "list files first"}, got)

	var reqBody map[string]any
	require.NoError(t, json.Unmarshal(captured, &reqBody))
	assert.Equal(t, "test-model", reqBody["model"])
	rf := reqBody["response_format"].(map[string]any)
	assert.Equal(t, "json_schema", rf["type"])
	schema := rf["json_schema"].(map[string]any)["schema"].(map[string]any)
	assert.ElementsMatch(t, []any{"command", "rationale", "done", "summary", "file"}, schema["required"])
	msgs := reqBody["messages"].([]any)
	require.Len(t, msgs, 2)
	assert.Equal(t, "system", msgs[0].(map[string]any)["role"])
	assert.Equal(t, "user", msgs[1].(map[string]any)["role"])
	assert.Equal(t, "what files are here?", msgs[1].(map[string]any)["content"])
}

func TestOpenAIProposer_DoneRoundTrip(t *testing.T) {
	srv := serveProposal(t,
		`{"command": "", "rationale": "", "done": true, "summary": "found 3 files"}`,
		http.StatusOK, nil)
	defer srv.Close()

	got, _, err := testProposer(srv).Propose(context.Background(), []Message{
		{Role: RoleUser, Content: "what files are here?"},
		{Role: RoleAssistant, Content: `{"command":"ls","rationale":"list","done":false,"summary":""}`},
		{Role: RoleTool, Content: "Command `ls` exited with code 0.\nstdout:\na b c"},
	})
	require.NoError(t, err)
	assert.True(t, got.Done)
	assert.Empty(t, got.Command)
	assert.Equal(t, "found 3 files", got.Summary)
}

func TestOpenAIProposer_ToolRoleMappedToUser(t *testing.T) {
	var captured []byte
	srv := serveProposal(t, `{"command": "pwd", "rationale": "r", "done": false, "summary": ""}`,
		http.StatusOK, &captured)
	defer srv.Close()

	_, _, err := testProposer(srv).Propose(context.Background(), []Message{
		{Role: RoleUser, Content: "goal"},
		{Role: RoleTool, Content: "Command `ls` exited with code 0."},
	})
	require.NoError(t, err)

	var reqBody map[string]any
	require.NoError(t, json.Unmarshal(captured, &reqBody))
	msgs := reqBody["messages"].([]any)
	require.Len(t, msgs, 3)
	// Tool results go out as user: hosted endpoints reject a bare tool role.
	for _, m := range msgs {
		assert.NotEqual(t, "tool", m.(map[string]any)["role"])
	}
}

func TestOpenAIProposer_ToleratesFences(t *testing.T) {
	srv := serveProposal(t,
		"Here you go:\n```json\n{\"command\": \"pwd\", \"rationale\": \"show dir\", \"done\": false, \"summary\": \"\"}\n```",
		http.StatusOK, nil)
	defer srv.Close()

	got, _, err := testProposer(srv).Propose(context.Background(), []Message{{Role: RoleUser, Content: "g"}})
	require.NoError(t, err)
	assert.Equal(t, "pwd", got.Command)
}

func TestOpenAIProposer_Errors(t *testing.T) {
	t.Run("non-200 is an error", func(t *testing.T) {
		srv := serveProposal(t, "", http.StatusUnauthorized, nil)
		defer srv.Close()
		_, _, err := testProposer(srv).Propose(context.Background(), []Message{{Role: RoleUser, Content: "g"}})
		assert.Error(t, err)
	})

	t.Run("no choices is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"choices": []}`))
		}))
		defer srv.Close()
		_, _, err := testProposer(srv).Propose(context.Background(), []Message{{Role: RoleUser, Content: "g"}})
		assert.Error(t, err)
	})

	t.Run("not-done with empty command is an error", func(t *testing.T) {
		srv := serveProposal(t, `{"command": "", "rationale": "x", "done": false, "summary": ""}`,
			http.StatusOK, nil)
		defer srv.Close()
		_, _, err := testProposer(srv).Propose(context.Background(), []Message{{Role: RoleUser, Content: "g"}})
		assert.ErrorContains(t, err, "empty command")
	})

	t.Run("non-JSON is an error", func(t *testing.T) {
		srv := serveProposal(t, `just some prose, no object`, http.StatusOK, nil)
		defer srv.Close()
		_, _, err := testProposer(srv).Propose(context.Background(), []Message{{Role: RoleUser, Content: "g"}})
		assert.Error(t, err)
	})

	t.Run("empty transcript is an error without a request", func(t *testing.T) {
		called := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		}))
		defer srv.Close()
		_, _, err := testProposer(srv).Propose(context.Background(), nil)
		assert.Error(t, err)
		assert.False(t, called)
	})

	t.Run("unknown role is an error", func(t *testing.T) {
		srv := serveProposal(t, `{"command": "x", "done": false}`, http.StatusOK, nil)
		defer srv.Close()
		_, _, err := testProposer(srv).Propose(context.Background(),
			[]Message{{Role: "bogus", Content: "g"}})
		assert.ErrorContains(t, err, "unknown message role")
	})
}

func TestOpenAIProposer_AuthHeaderOptional(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"choices": [{"message": {"content": "{\"command\": \"pwd\", \"done\": false}"}}]}`))
	}))
	defer srv.Close()

	p := New(WithBaseURL(srv.URL), WithModel("m"))
	_, _, err := p.Propose(context.Background(), []Message{{Role: RoleUser, Content: "g"}})
	require.NoError(t, err)
	assert.Empty(t, gotAuth)

	p.APIKey = "sk-x"
	_, _, err = p.Propose(context.Background(), []Message{{Role: RoleUser, Content: "g"}})
	require.NoError(t, err)
	assert.Equal(t, "Bearer sk-x", gotAuth)
}

func TestEncodeAssistantTurn_RoundTrips(t *testing.T) {
	enc := EncodeAssistantTurn(Proposal{Command: "ls", Rationale: "list", Done: false})
	assert.True(t, strings.Contains(enc, `"command"`))
	p, err := parseProposal(enc)
	require.NoError(t, err)
	assert.Equal(t, "ls", p.Command)
}

func TestOpenAIProposer_FileRoundTrips(t *testing.T) {
	t.Run("populated when the model writes one", func(t *testing.T) {
		srv := serveProposal(t,
			`{"command": "cat > out.py <<'EOF'\nprint(1)\nEOF", "rationale": "write it", "done": false, "summary": "", "file": "out.py"}`,
			http.StatusOK, nil)
		defer srv.Close()

		got, _, err := testProposer(srv).Propose(context.Background(), []Message{{Role: RoleUser, Content: "write a script"}})
		require.NoError(t, err)
		assert.Equal(t, "out.py", got.File)
	})

	t.Run("populated when the model just cats one", func(t *testing.T) {
		srv := serveProposal(t,
			`{"command": "cat out.py", "rationale": "show it", "done": false, "summary": "", "file": "out.py"}`,
			http.StatusOK, nil)
		defer srv.Close()

		got, _, err := testProposer(srv).Propose(context.Background(), []Message{{Role: RoleUser, Content: "show me out.py"}})
		require.NoError(t, err)
		assert.Equal(t, "out.py", got.File)
	})

	t.Run("empty for a command that doesn't center on one file", func(t *testing.T) {
		srv := serveProposal(t,
			`{"command": "ls", "rationale": "list", "done": false, "summary": "", "file": ""}`,
			http.StatusOK, nil)
		defer srv.Close()

		got, _, err := testProposer(srv).Propose(context.Background(), []Message{{Role: RoleUser, Content: "goal"}})
		require.NoError(t, err)
		assert.Empty(t, got.File)
	})

	t.Run("round-trips through EncodeAssistantTurn", func(t *testing.T) {
		enc := EncodeAssistantTurn(Proposal{Command: "cat > f.txt <<'EOF'\nx\nEOF", File: "f.txt"})
		assert.Contains(t, enc, `"file":"f.txt"`)
		p, err := parseProposal(enc)
		require.NoError(t, err)
		assert.Equal(t, "f.txt", p.File)
	})
}

func TestPing(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/models", r.URL.Path)
		_, _ = w.Write([]byte(`{"data": []}`))
	}))
	defer ok.Close()
	require.NoError(t, Ping(context.Background(), ok.URL, ""))

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer bad.Close()
	assert.ErrorContains(t, Ping(context.Background(), bad.URL, ""), "HTTP 401")

	assert.Error(t, Ping(context.Background(), "http://127.0.0.1:1", ""))
}

func TestOpenAIProposer_ReasoningContentFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices": [{"message": {
			"content": "",
			"reasoning_content": "{\"command\": \"ls\", \"rationale\": \"list\", \"done\": false, \"summary\": \"\"}"
		}}]}`))
	}))
	defer srv.Close()

	got, _, err := New(WithBaseURL(srv.URL), WithModel("m")).Propose(
		context.Background(), []Message{{Role: RoleUser, Content: "g"}})
	require.NoError(t, err)
	assert.Equal(t, "ls", got.Command)
}

func TestOpenAIProposer_ReasoningFieldFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices": [{"message": {
			"content": "",
			"reasoning": "{\"command\": \"pwd\", \"rationale\": \"dir\", \"done\": false, \"summary\": \"\"}"
		}}]}`))
	}))
	defer srv.Close()

	got, _, err := New(WithBaseURL(srv.URL), WithModel("m")).Propose(
		context.Background(), []Message{{Role: RoleUser, Content: "g"}})
	require.NoError(t, err)
	assert.Equal(t, "pwd", got.Command)
}

func TestOpenAIProposer_ExtraHeadersSent(t *testing.T) {
	var gotReferer, gotTitle string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReferer = r.Header.Get("HTTP-Referer")
		gotTitle = r.Header.Get("X-Title")
		_, _ = w.Write([]byte(`{"choices": [{"message": {"content": "{\"command\": \"pwd\", \"done\": false}"}}]}`))
	}))
	defer srv.Close()

	p := New(WithBaseURL(srv.URL), WithModel("m"),
		WithHeaders(map[string]string{"HTTP-Referer": "https://example.com", "X-Title": "detent"}))
	_, _, err := p.Propose(context.Background(), []Message{{Role: RoleUser, Content: "g"}})
	require.NoError(t, err)
	assert.Equal(t, "https://example.com", gotReferer)
	assert.Equal(t, "detent", gotTitle)
}

func TestStructured_ReturnsRawJSONAndItsCost(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"kind\":\"table\"}"}}],` +
			`"usage":{"prompt_tokens":11,"completion_tokens":3},"model":"m"}`))
	}))
	defer srv.Close()

	p := &OpenAIProposer{BaseURL: srv.URL, Model: "m"}
	raw, used, err := p.Structured(context.Background(), "sys", "usr",
		map[string]any{"type": "object"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"kind":"table"}`, string(raw))
	assert.Equal(t, 11, used.PromptTokens)
	assert.Equal(t, 3, used.CompletionTokens)

	msgs := got["messages"].([]any)
	require.Len(t, msgs, 2, "the caller's two messages, no system prompt of ours")
	assert.Equal(t, "sys", msgs[0].(map[string]any)["content"])
	assert.Equal(t, "structured",
		got["response_format"].(map[string]any)["json_schema"].(map[string]any)["name"])
}

func TestStructured_EmptyReplyIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"  "}}]}`))
	}))
	defer srv.Close()

	p := &OpenAIProposer{BaseURL: srv.URL}
	_, _, err := p.Structured(context.Background(), "s", "u", map[string]any{})
	assert.Error(t, err)
}
