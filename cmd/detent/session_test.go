package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/config"
	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/model"
)

// Every phase run() strings together, against a fake endpoint on the host,
// opens a session, answers one request and records it.
func TestSession_PhasesRunOneRequestEndToEnd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())              // the store, the worktree and the views live under it
	t.Setenv("USERPROFILE", os.Getenv("HOME")) // where Windows looks for home
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
	s.sd.session = &latestSession{id: s.id}
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
		{host.Pwsh, model.ShellPwsh, event.ToolPowerShell},
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
			require.IsType(t, &host.Shell{}, runner)
			assert.Equal(t, tt.want, runner.(*host.Shell).Dialect())
		})
	}
}

func TestLoadMCPConfig_SkipsFilesForHeadlessRuns(t *testing.T) {
	broken := filepath.Join(t.TempDir(), ".mcp.json")
	require.NoError(t, os.WriteFile(broken, []byte("not json"), 0o600))

	got, err := loadMCPConfig(false, []byte("not json"), []string{broken})
	require.NoError(t, err, "headless run must not read MCP config")
	require.Empty(t, got)
}

func TestLoadMCPConfig_LoadsFilesForTUI(t *testing.T) {
	broken := filepath.Join(t.TempDir(), ".mcp.json")
	require.NoError(t, os.WriteFile(broken, []byte("not json"), 0o600))

	_, err := loadMCPConfig(true, nil, []string{broken})
	require.Error(t, err, "TUI should retain normal MCP config validation")
}

func TestSandboxSocketFor_OnlyWhenThereIsASandbox(t *testing.T) {
	assert.Equal(t, "/s.sock", sandboxSocketFor(config.Config{SandboxMode: config.SandboxAuto, SandboxSocket: "/s.sock"}))
	assert.Empty(t, sandboxSocketFor(config.Config{SandboxMode: config.SandboxHost, SandboxSocket: "/s.sock"}))
}

// Both exist to keep a typed nil from reaching forget as a non-nil interface.
func TestNilGuards_StayNil(t *testing.T) {
	assert.Nil(t, sessionStore(nil))
	assert.Nil(t, containerRemover(""))
	assert.NotNil(t, containerRemover("/s.sock"))
}
