package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/config"
	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/tool"
)

// Every phase run() strings together, against a fake endpoint on the host,
// opens a session, answers one request and records it.
func TestSession_PhasesRunOneRequestEndToEnd(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // the store, the worktree and the views live under it
	asked := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = io.WriteString(w, `{"data":[]}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		asked <- string(raw)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`)
	}))
	defer srv.Close()

	cfg := config.Resolve(config.Config{}, config.Config{BaseURL: srv.URL, SandboxMode: config.SandboxHost}, -1)
	require.NoError(t, cfg.Validate())
	require.NoError(t, <-ping(cfg))

	s := &session{o: options{prompt: "say done"}, cfg: cfg, id: uuid.Must(uuid.NewV7())}
	s.sd.session = s.id
	defer func() { s.sd.close() }()
	require.NoError(t, s.openSandbox())
	assert.False(t, s.env.Sandboxed, "host mode runs here")
	require.NoError(t, s.buildEngine())
	require.NotNil(t, s.events, "the session is recorded under HOME")

	require.NoError(t, s.runHeadless(s.wire()))
	assert.Contains(t, <-asked, "say done", "the prompt reached the endpoint")
}

// The host shell picks the runner, the shell tool's name and what the prompt says, together.
func TestSession_HostShellPicksTheToolAndThePrompt(t *testing.T) {
	t.Setenv("DETENT_HOST_SHELL", "")
	for _, tt := range []struct {
		shell, want, tool string
	}{
		{host.Sh, model.ShellSh, "bash"},
		{host.GitBash, model.ShellGitBash, "bash"},
		{host.Pwsh, model.ShellPwsh, tool.PowerShellName},
	} {
		t.Run(tt.shell, func(t *testing.T) {
			if _, err := host.Find(tt.shell); err != nil {
				t.Skipf("no %s here", tt.shell)
			}
			cfg := config.Resolve(config.Config{}, config.Config{SandboxMode: config.SandboxHost, HostShell: tt.shell}, -1)
			s := &session{cfg: cfg}
			require.NoError(t, s.openSandbox())
			assert.Equal(t, tt.want, s.env.Shell)
			assert.Equal(t, tt.tool, shellTool(s.env).Name())
			runner, _ := s.runners.Select(event.UnknownRisk())
			assert.Equal(t, tt.want, runner.(*host.Shell).Dialect())
		})
	}
}
