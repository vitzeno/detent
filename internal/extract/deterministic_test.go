package extract

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/capabilities"
	"github.com/vitzeno/detent/internal/reduce"
)

func TestDeterministicConstructor_Candidates_UnsupportedArgType(t *testing.T) {
	c := &DeterministicConstructor{}
	_, err := c.Candidates(context.Background(), capabilities.ArgLiteral, "search for TODO", State{})
	assert.Error(t, err)
}

func TestDeterministicConstructor_Candidates_Path_FromGoalText(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"go.mod", "go.sum", "readme.txt", "notes.md"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
	}

	tests := []struct {
		name      string
		goalText  string
		wantPaths []string // in ranked order; nil means "no candidates"
	}{
		{
			name:      "goal names a file directly, no ambiguity",
			goalText:  "show me the go.mod file",
			wantPaths: []string{filepath.Join(dir, "go.mod")},
		},
		{
			name:     "goal's wording incidentally substring-matches an unrelated file",
			goalText: "read the go.mod file",
			// "read" is a substring of "readme.txt" — the fuzzy matcher is
			// deliberately imprecise (§5: "regex/heuristic ... often no
			// model involvement"); over-proposing here is correct, since
			// disambiguation is the Judge's job (target_resolvable, §7),
			// not the Constructor's.
			wantPaths: []string{filepath.Join(dir, "go.mod"), filepath.Join(dir, "readme.txt")},
		},
		{
			name:      "goal references nothing in the directory",
			goalText:  "what files are in this directory?",
			wantPaths: nil,
		},
		{
			name:     "goal matches multiple files, ranked by token overlap",
			goalText: "look at go.mod and go.sum",
			// both mention "go", but each also uniquely matches its own
			// extension word (mod/sum) — order isn't asserted beyond
			// "both present", since token-overlap ties aren't meaningful.
			wantPaths: []string{filepath.Join(dir, "go.mod"), filepath.Join(dir, "go.sum")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &DeterministicConstructor{Root: dir}
			candidates, err := c.Candidates(context.Background(), capabilities.ArgPath, tt.goalText, State{})
			require.NoError(t, err)

			var gotPaths []string
			for _, cand := range candidates {
				gotPaths = append(gotPaths, cand.Fields["path"].(string))
				assert.Equal(t, cand.Fields["path"], cand.Desc)
				assert.NotEmpty(t, cand.ID)
			}

			if len(tt.wantPaths) > 1 {
				// tied scores, so relative order isn't meaningful
				assert.ElementsMatch(t, tt.wantPaths, gotPaths)
				return
			}
			assert.Equal(t, tt.wantPaths, gotPaths)
		})
	}
}

func TestDeterministicConstructor_Candidates_Path_StateTakesPriorityOverGoalText(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("x"), 0o644))

	c := &DeterministicConstructor{Root: dir}
	state := State{Values: []reduce.Value{
		{Type: "path", Value: "/discovered/earlier/step.log"},
	}}

	// Goal text alone would match go.mod (in dir) via fuzzy matching, but
	// §5's ordering rule says state-derived facts win outright — the
	// goal-text fallback must not even run.
	candidates, err := c.Candidates(context.Background(), capabilities.ArgPath, "read the go.mod file", state)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, "/discovered/earlier/step.log", candidates[0].Fields["path"])
}

func TestDeterministicConstructor_Candidates_Path_NonexistentRoot(t *testing.T) {
	c := &DeterministicConstructor{Root: "/does/not/exist/anywhere"}
	_, err := c.Candidates(context.Background(), capabilities.ArgPath, "read something", State{})
	assert.Error(t, err)
}

func TestCapAt(t *testing.T) {
	tests := []struct {
		name  string
		in    []Candidate
		max   int
		wantN int
	}{
		{name: "under cap", in: make([]Candidate, 3), max: 5, wantN: 3},
		{name: "exactly at cap", in: make([]Candidate, 5), max: 5, wantN: 5},
		{name: "over cap, truncated", in: make([]Candidate, 8), max: 5, wantN: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Len(t, capAt(tt.in, tt.max), tt.wantN)
		})
	}
}
