package event

import (
	"go/build"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ui and the engine both import event, so nothing it pulls in may drag
// either anywhere: every non-stdlib import must itself be stdlib only.
func TestPackage_DependsOnlyOnStdlibOnlyPackages(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	require.NoError(t, err)

	for _, imp := range pkg.Imports {
		if !external(imp) {
			continue
		}
		dep, err := build.Import(imp, ".", build.FindOnly|build.ImportComment)
		require.NoError(t, err, "cannot resolve %q", imp)
		found, err := build.ImportDir(dep.Dir, 0)
		require.NoError(t, err, "cannot read %q", imp)

		for _, sub := range found.Imports {
			assert.False(t, external(sub),
				"event imports %s, which imports %s; event may only depend on packages that are themselves stdlib only", imp, sub)
		}
	}
}

// Every intent is a "do." kind and nothing else is, so a new one left out
// of intents would go to the front-ends instead of its owner.
func TestKinds_IntentsAreExactlyTheDoKinds(t *testing.T) {
	for _, k := range Kinds() {
		assert.Equal(t, strings.HasPrefix(string(k), "do."), k.IsIntent(), "%s", k)
	}
}

// Exactly one event is lossy. If a second ever is, it was a decision.
func TestLossy_IsOutputChunkAlone(t *testing.T) {
	assert.True(t, OutputChunk{}.Lossy())
	for _, e := range []Event{ToolCallEnded{}, StepEnded{}, TurnEnded{}, Notice{}, Abort{}} {
		assert.False(t, e.Lossy(), "%T must be delivered", e)
	}
}

// A Step mints all its Call ids inside one millisecond, so ordering rests
// on google/uuid's v7 being monotonic within one. Pinned against a bump.
func TestNewV7_OrdersWithinOneMillisecond(t *testing.T) {
	const n = 500
	ids := make([]string, n)
	start := time.Now()
	for i := range ids {
		ids[i] = uuid.Must(uuid.NewV7()).String()
	}
	require.Less(t, time.Since(start), 100*time.Millisecond,
		"%d ids took too long to share many milliseconds", n)

	assert.True(t, slices.IsSorted(ids), "ids must sort by creation")
	assert.Len(t, slices.Compact(slices.Clone(ids)), n, "and be unique")
}

// external reports whether an import path leaves the standard
// library. Stdlib paths have no dot in their first segment.
func external(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return strings.Contains(first, ".")
}
