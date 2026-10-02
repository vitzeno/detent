package logging_test

import (
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

// One stream, and a component field rather than a file per component:
// filtering stays easy and the join across components survives.
func TestSetup_WritesOneQueryableStreamPerSession(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup("s1", logging.WithDir(dir))
	require.NoError(t, err)

	logging.For(logging.Viewgen).Info("declined",
		logging.KeyEvent, logging.ViewDeclined, "candidates", 2)
	logging.For(logging.LLM).Info("replied",
		logging.KeyEvent, logging.LLMReply, logging.KeyMS, 190)
	require.NoError(t, closer())

	got := records(t, dir, "s1")
	require.Len(t, got, 2)
	for _, r := range got {
		assert.Equal(t, "s1", r[logging.KeySession])
	}
	assert.Equal(t, logging.Viewgen, got[0][logging.KeyComponent])
	assert.Equal(t, logging.ViewDeclined, got[0][logging.KeyEvent])
	assert.Equal(t, logging.LLM, got[1][logging.KeyComponent])
}

// Bodies are off by default because they carry secrets and bulk, and
// the shape of a reply answers most questions.
func TestBody_WithheldUnlessAskedFor(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup("s3", logging.WithDir(dir))
	require.NoError(t, err)
	withheld := logging.Body("sk-secret-key-material")
	require.NoError(t, closer())
	assert.NotContains(t, withheld, "secret")
	assert.Contains(t, withheld, "22 bytes")

	closer, err = logging.Setup("s4", logging.WithDir(dir), logging.WithBodies(true))
	require.NoError(t, err)
	assert.Equal(t, "hello", logging.Body("hello"))
	require.NoError(t, closer())
}

// Logging is never worth failing a session over.
func TestSetup_UnwritableDirDisablesRatherThanFails(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))

	closer, err := logging.Setup("s5", logging.WithDir(file))
	assert.Error(t, err, "the caller is told")
	require.NotNil(t, closer)
	assert.NotPanics(t, func() {
		logging.For(logging.Engine).Info("still fine")
		_ = closer()
	}, "but the session runs on")
}

func TestSetup_LevelFiltersAsAsked(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup("s6", logging.WithDir(dir), logging.WithLevel("warn"))
	require.NoError(t, err)
	logging.For(logging.Host).Info("quiet")
	logging.For(logging.Host).Warn("loud", logging.KeyEvent, logging.LLMRequest)
	require.NoError(t, closer())

	got := records(t, dir, "s6")
	require.Len(t, got, 1)
	assert.Equal(t, slog.LevelWarn.String(), got[0]["level"])
}

// An event name is a record's primary key, so two sharing one makes a
// query return both. Facts use event.Kind, which event's tests cover.
func TestEvents_NamesAreUnique(t *testing.T) {
	names := map[string]int{}
	for _, e := range []string{
		logging.SessionOpen,
		logging.LLMRequest, logging.LLMReply, logging.LLMError,
		logging.ViewLookup, logging.ViewSkipped, logging.ViewInvalid,
		logging.ViewFit, logging.ViewAccepted, logging.ViewDeclined, logging.ViewDrawn,
	} {
		names[e]++
		assert.Equal(t, 1, names[e], "%s is used by more than one event", e)
	}
}

// No name may say "goal": the unit is a Turn, and a query for the
// wrong word finds nothing.
func TestEvents_NoNameSaysGoal(t *testing.T) {
	for _, e := range []string{
		logging.SessionOpen, logging.LLMRequest, logging.LLMReply, logging.LLMError,
		logging.ViewLookup, logging.ViewSkipped, logging.ViewInvalid,
		logging.ViewFit, logging.ViewAccepted, logging.ViewDeclined, logging.ViewDrawn,
		logging.KeyTurn, logging.KeyStep, logging.KeyCall,
	} {
		assert.NotContains(t, e, "goal", "%q still names a goal", e)
	}
}

// records reads what has been written so far, skipping a half-written
// trailing line since a subscriber may be mid-write while a test polls.
func records(t *testing.T, dir, session string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, session+".jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}
