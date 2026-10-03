package logging_test

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

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
	require.Error(t, err, "the caller is told")
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

// names is every record name events.go declares, one list so the two
// tests below cannot disagree about what exists.
var names = []string{
	logging.SessionOpen,
	logging.LLMRequest, logging.LLMReply, logging.LLMError,
	logging.ViewLookup, logging.ViewSkipped, logging.ViewInvalid,
	logging.ViewFit, logging.ViewAccepted, logging.ViewDeclined, logging.ViewDrawn,
}

// An event name is a record's primary key, so two sharing one makes a
// query return both. Facts use event.Kind, which event's tests cover.
func TestEvents_NamesAreUnique(t *testing.T) {
	seen := map[string]int{}
	for _, e := range names {
		seen[e]++
		assert.Equal(t, 1, seen[e], "%s is used by more than one event", e)
	}
}

// No name may say "goal": the unit is a Turn, and a query for the
// wrong word finds nothing.
func TestEvents_NoNameSaysGoal(t *testing.T) {
	for _, e := range append(slices.Clone(names), logging.KeyTurn, logging.KeyStep, logging.KeyToolCall) {
		assert.NotContains(t, e, "goal", "%q still names a goal", e)
	}
}

// With bodies on, the log holds every prompt and output, so nobody else
// on the machine may read it, even if the file was made wider before.
func TestSetup_LogIsOwnerOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	closer, err := logging.Setup("m1", logging.WithDir(dir))
	require.NoError(t, err)
	require.NoError(t, closer())

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	info, err = os.Stat(filepath.Join(dir, "m1.jsonl"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	wide := filepath.Join(dir, "m2.jsonl")
	require.NoError(t, os.WriteFile(wide, nil, 0o644))
	require.NoError(t, os.Chmod(wide, 0o644))
	closer, err = logging.Setup("m2", logging.WithDir(dir))
	require.NoError(t, err)
	require.NoError(t, closer())
	info, err = os.Stat(wide)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// A typo in the level is said, not silently read as info.
func TestSetup_UnknownLevelIsReportedAndLogsAtInfo(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup("l1", logging.WithDir(dir), logging.WithLevel("debgu"))
	require.ErrorContains(t, err, "debgu")
	logging.For(logging.Host).Debug("quiet")
	logging.For(logging.Host).Info("kept")
	require.NoError(t, closer())
	assert.Len(t, records(t, dir, "l1"), 1)
}

// A late write after the closer goes nowhere, not to a closed file.
func TestSetup_CloserLeavesADiscardingDefault(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup("d1", logging.WithDir(dir))
	require.NoError(t, err)
	require.NoError(t, closer())
	logging.For(logging.Host).Info("late")
	assert.Empty(t, records(t, dir, "d1"))
}

func TestSnippet_KeepsTheStartOfLongTextWhenBodiesAreOff(t *testing.T) {
	dir := t.TempDir()
	closer, err := logging.Setup("p1", logging.WithDir(dir))
	require.NoError(t, err)
	defer closer() //nolint:errcheck

	assert.Equal(t, "short error", logging.Snippet("short error"))
	long := "endpoint said: " + strings.Repeat("é", 300)
	got := logging.Snippet(long)
	assert.True(t, strings.HasPrefix(got, "endpoint said: "))
	assert.Less(t, len(got), len(long))
	assert.True(t, utf8.ValidString(got), "a cut must not split a rune")
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
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}
