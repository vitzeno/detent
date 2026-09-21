package logging_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/logging"
)

func records(t *testing.T, dir, session string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, session+".jsonl"))
	require.NoError(t, err)
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m))
		out = append(out, m)
	}
	return out
}

// One stream, and a component field rather than a file per component:
// filtering stays easy and the join across components survives.
func TestSetup_WritesOneQueryableStreamPerSession(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup(logging.Options{Dir: dir, Session: "s1"})
	require.NoError(t, err)

	ctx := logging.WithStep(logging.WithGoal(context.Background(), 2), 7)
	logging.For(logging.Viewgen).InfoContext(ctx, "declined",
		logging.KeyEvent, logging.ViewDeclined, "candidates", 2)
	logging.For(logging.LLM).InfoContext(ctx, "replied",
		logging.KeyEvent, logging.LLMReply, logging.KeyMS, 190)
	require.NoError(t, closer())

	got := records(t, dir, "s1")
	require.Len(t, got, 2)
	for _, r := range got {
		assert.Equal(t, "s1", r[logging.KeySession])
		assert.EqualValues(t, 2, r[logging.KeyGoal], "the goal came from the context")
		assert.EqualValues(t, 7, r[logging.KeyStep], "and so did the step")
	}
	assert.Equal(t, logging.Viewgen, got[0][logging.KeyComponent])
	assert.Equal(t, logging.ViewDeclined, got[0][logging.KeyEvent])
	assert.Equal(t, logging.LLM, got[1][logging.KeyComponent])
}

// An unmarked context writes no empty correlation fields, so a query
// for a step never matches a record that has none.
func TestSetup_OmitsMarksTheContextDoesNotCarry(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup(logging.Options{Dir: dir, Session: "s2"})
	require.NoError(t, err)
	logging.For(logging.UI).Info("started", logging.KeyEvent, logging.GoalBegin)
	require.NoError(t, closer())

	got := records(t, dir, "s2")
	require.Len(t, got, 1)
	assert.NotContains(t, got[0], logging.KeyStep)
	assert.NotContains(t, got[0], logging.KeyGoal)
}

// Bodies are off by default because they carry secrets and bulk, and
// the shape of a reply answers most questions.
func TestBody_WithheldUnlessAskedFor(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup(logging.Options{Dir: dir, Session: "s3"})
	require.NoError(t, err)
	withheld := logging.Body("sk-secret-key-material")
	require.NoError(t, closer())
	assert.NotContains(t, withheld, "secret")
	assert.Contains(t, withheld, "22 bytes")

	closer, err = logging.Setup(logging.Options{Dir: dir, Session: "s4", Bodies: true})
	require.NoError(t, err)
	assert.Equal(t, "hello", logging.Body("hello"))
	require.NoError(t, closer())
}

// Logging is never worth failing a session over.
func TestSetup_UnwritableDirDisablesRatherThanFails(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))

	closer, err := logging.Setup(logging.Options{Dir: file, Session: "s5"})
	assert.Error(t, err, "the caller is told")
	require.NotNil(t, closer)
	assert.NotPanics(t, func() {
		logging.For(logging.Agent).Info("still fine")
		_ = closer()
	}, "but the session runs on")
}

func TestSetup_LevelFiltersAsAsked(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup(logging.Options{Dir: dir, Session: "s6", Level: "warn"})
	require.NoError(t, err)
	logging.For(logging.Host).Info("quiet")
	logging.For(logging.Host).Warn("loud", logging.KeyEvent, logging.CmdRun)
	require.NoError(t, closer())

	got := records(t, dir, "s6")
	require.Len(t, got, 1)
	assert.Equal(t, slog.LevelWarn.String(), got[0]["level"])
}
