package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/config"
	mcppkg "github.com/vitzeno/detent/internal/mcp"
	"github.com/vitzeno/detent/internal/skills"
	"github.com/vitzeno/detent/internal/store"
	"github.com/vitzeno/detent/internal/trust"
)

func TestDotenvLine_ReadsWhatDotenvFilesHold(t *testing.T) {
	for line, want := range map[string][2]string{
		"KEY=v":                   {"KEY", "v"},
		"  KEY = v  ":             {"KEY", "v"},
		"export KEY=v":            {"KEY", "v"},
		`KEY="a b"`:               {"KEY", "a b"},
		`KEY='a # not a comment'`: {"KEY", "a # not a comment"},
		`KEY='abc"`:               {"KEY", `'abc"`},
		`KEY=ends in quote"`:      {"KEY", `ends in quote"`},
		"KEY=v # a comment":       {"KEY", "v"},
		"KEY=a#b":                 {"KEY", "a#b"},
		"KEY=":                    {"KEY", ""},
		"KEY=x=y":                 {"KEY", "x=y"},
	} {
		key, value, ok := dotenvLine(line)
		require.True(t, ok, line)
		assert.Equal(t, want, [2]string{key, value}, line)
	}
	for _, line := range []string{"", "  ", "# KEY=v", "no equals", "=v"} {
		_, _, ok := dotenvLine(line)
		assert.False(t, ok, line)
	}
}

// A real variable wins, even one set to "".
func TestLoadDotenv_FillsOnlyGaps(t *testing.T) {
	raw := []byte("DETENT_T_SET=file\nexport DETENT_T_EMPTY=file\nDETENT_T_NEW='file'\n")
	t.Setenv("DETENT_T_SET", "real")
	t.Setenv("DETENT_T_EMPTY", "")
	t.Setenv("DETENT_T_NEW", "")
	require.NoError(t, os.Unsetenv("DETENT_T_NEW"))

	require.NoError(t, loadDotenv(raw))
	assert.Equal(t, "real", os.Getenv("DETENT_T_SET"))
	assert.Empty(t, os.Getenv("DETENT_T_EMPTY"))
	assert.Equal(t, "file", os.Getenv("DETENT_T_NEW"))

	assert.NoError(t, loadDotenv(nil), "no .env is not an error")
}

// What was approved is what loads, even if the files change after the question.
func TestLayer_LoadsWhatTrustApproved(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DETENT_MODEL", "")
	require.NoError(t, os.Unsetenv("DETENT_MODEL"))
	t.Setenv("DETENT_T_APPROVED", "")
	require.NoError(t, os.Unsetenv("DETENT_T_APPROVED"))
	files := map[string]string{
		".detent.yaml": "model: approved\n",
		".env":         "DETENT_T_APPROVED=yes\n",
		".mcp.json":    `{"mcpServers": {"approved": {"command": "true"}}}`,
	}
	for name, body := range files {
		require.NoError(t, os.WriteFile(name, []byte(body), 0o600))
	}
	var asked bool
	trusted, err := trust.Decide(trust.Options{Dir: dir, State: t.TempDir(),
		Ask: func() bool { asked = true; return true }, Out: io.Discard})
	require.NoError(t, err)
	require.True(t, asked)

	require.NoError(t, os.WriteFile(".detent.yaml", []byte("model: swapped\n"), 0o600))
	require.NoError(t, os.WriteFile(".env", []byte("DETENT_T_APPROVED=swapped\n"), 0o600))
	require.NoError(t, os.WriteFile(".mcp.json", []byte(`{"mcpServers": {"swapped": {"command": "sh"}}}`), 0o600))

	cfg, err := layer(options{steps: -1}, trusted)
	require.NoError(t, err)
	assert.Equal(t, "approved", cfg.Model)
	assert.Equal(t, "yes", os.Getenv("DETENT_T_APPROVED"))
	servers, err := loadMCPConfig(true, trusted.Files[mcppkg.Project], nil)
	require.NoError(t, err)
	assert.Contains(t, servers, "approved")
	assert.NotContains(t, servers, "swapped")
}

// A script driving -prompt reads the exit status, not stdout.
func TestEndedError_GivesEachReasonItsOwnStatus(t *testing.T) {
	for reason, want := range map[event.EndReason]int{
		event.EndError: 1, event.EndAborted: 130, event.EndBound: 3, event.EndStopped: 4,
	} {
		assert.Equal(t, want, endedError(reason).code(), reason)
	}
	assert.NotEmpty(t, endedError(event.EndError).Error())
	assert.Empty(t, endedError(event.EndAborted).Error(), "the reason is already printed")
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

func TestResolveSession_ReadsAnIdThenLastThenAName(t *testing.T) {
	events, err := store.Open(filepath.Join(t.TempDir(), "events.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = events.Close() })

	_, err = resolveSession(events, "last")
	require.ErrorContains(t, err, "no sessions")

	older, newer := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for i, id := range []uuid.UUID{older, newer} {
		require.NoError(t, events.Append(id, event.Record{
			Ordinal: 1, At: time.Now().Add(time.Duration(i) * time.Second), Event: event.SessionStarted{Session: id}}))
	}
	require.NoError(t, events.Rename(older, "Mine"))

	for want, got := range map[uuid.UUID]string{older: "mine", newer: "LAST"} {
		id, err := resolveSession(events, got)
		require.NoError(t, err, got)
		assert.Equal(t, want, id, got)
	}
	id, err := resolveSession(events, newer.String())
	require.NoError(t, err)
	assert.Equal(t, newer, id)

	_, err = resolveSession(events, "nobody")
	assert.ErrorContains(t, err, `no session named "nobody"`)
}

// A directory called ..foo is under cwd, not above it.
func TestSandboxDir_ReadsDotDotAsAParentOnly(t *testing.T) {
	cwd := t.TempDir()
	mounts := map[string]string{}
	got := sandboxDir(skills.Skill{Dir: filepath.Join(cwd, "..foo", "s")}, cwd, "/workspace", nil, mounts)
	assert.Equal(t, "/workspace/..foo/s", got)
	assert.Empty(t, mounts)
}
