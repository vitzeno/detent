package reduce

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessLines(t *testing.T) {
	tests := []struct {
		name   string
		output string
		golden string
	}{
		{
			name: "several real-shaped ps rows",
			output: " 4821 mohamed node\n" +
				" 5140 mohamed chrome\n" +
				"    1 root    launchd\n",
			golden: "testdata/process_lines/basic.golden.json",
		},
		{
			name:   "single process",
			output: "  501 alice   sleep\n",
			golden: "testdata/process_lines/single.golden.json",
		},
		{
			name:   "empty output",
			output: "",
			golden: "testdata/process_lines/empty.golden.json",
		},
		{
			name:   "command with spaces, e.g. a full path with args, stays one field",
			output: " 4821 mohamed /usr/local/bin/node server.js --port 3000\n",
			golden: "testdata/process_lines/cmd_with_spaces.golden.json",
		},
		{
			name:   "malformed row (fewer than 3 columns) is skipped, not guessed at",
			output: " 4821 mohamed node\nnotanumber alice sleep\n incomplete\n",
			golden: "testdata/process_lines/malformed_row_skipped.golden.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGolden(t, tt.golden, ProcessLines(tt.output))
		})
	}
}

// TestProcessLines_CapsRowsOnABusyRealMachine covers the bug a live run
// against a real desktop caught: `ps` scoped to one user still returned
// 700+ rows, and without a cap a single finding blew the entire §4.5
// state-token budget on step one. Not a golden-file case — the point is
// the count and the truncation flags, not a fixed 700-line fixture.
func TestProcessLines_CapsRowsOnABusyRealMachine(t *testing.T) {
	var lines []string
	for i := range 700 {
		lines = append(lines, fmt.Sprintf(" %d mohamed proc%d", 1000+i, i))
	}
	result := ProcessLines(strings.Join(lines, "\n") + "\n")

	assert.Len(t, result.Values, maxProcessRows)
	require.Contains(t, result.Facts, "processes")
	assert.Len(t, result.Facts["processes"], maxProcessRows)
	assert.Equal(t, maxProcessRows, result.Facts["count"])
	assert.Equal(t, true, result.Facts["truncated"])
	assert.Equal(t, 700, result.Facts["total_found"])
}

func TestProcessLines_UnderCap_NoTruncationFlag(t *testing.T) {
	result := ProcessLines(" 4821 mohamed node\n")
	assert.NotContains(t, result.Facts, "truncated")
	assert.NotContains(t, result.Facts, "total_found")
}
