package reduce

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// update regenerates golden files: go test ./internal/reduce/... -update
var update = flag.Bool("update", false, "update golden files instead of checking them")

// assertGolden is the golden-file check §4.4/§9 step 4 call for: raw
// output in (from the test table), a Result out, compared byte-for-byte
// (as JSON) against a checked-in expectation — no Jev involved anywhere
// in this path.
func assertGolden(t *testing.T, goldenPath string, got Result) {
	t.Helper()

	gotJSON, err := json.MarshalIndent(got, "", "  ")
	require.NoError(t, err)
	gotJSON = append(gotJSON, '\n')

	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
		require.NoError(t, os.WriteFile(goldenPath, gotJSON, 0o644))
		return
	}

	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "golden file missing — run `go test ./internal/reduce/... -update` to create it")
	assert.JSONEq(t, string(want), string(gotJSON))
}
