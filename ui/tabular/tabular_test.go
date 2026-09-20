package tabular

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/table"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const psSample = `USER       PID %CPU COMMAND
root         1  0.0 init
mo        4821 12.4 node server.js
`

func TestParseTable(t *testing.T) {
	cols, rows, ok := Parse(psSample, 80)
	require.True(t, ok)
	require.Len(t, cols, 4)
	assert.Equal(t, "USER", cols[0].Title)
	require.Len(t, rows, 2)
	assert.Equal(t, "4821", rows[1][1])

	for _, bad := range []string{
		"",
		"single line",
		"one\ntwo\nthree",
		"hello world",
		"a\nb c\n",
	} {
		_, _, ok := Parse(bad, 80)
		assert.False(t, ok, "%q must not parse as a table", bad)
	}
}

func TestParseTable_RaggedLastColumnJoins(t *testing.T) {
	_, rows, ok := Parse("a b\nc d e\n", 80)
	require.True(t, ok)
	assert.Equal(t, table.Row{"c", "d e"}, rows[0])
}

// TestBuild_RendersEveryRow pins the whole point of the component:
// bubbles scrolls rows through a viewport of its own, so a table built
// without a width or tall enough for its rows renders a header and
// nothing else — no error, just missing data.
func TestBuild_RendersEveryRow(t *testing.T) {
	cols, rows, ok := Parse(psSample, 80)
	require.True(t, ok)

	v := Build(cols, rows, 0, 40, 80, true).View()
	assert.Contains(t, v, "USER")
	assert.Contains(t, v, "4821")
	assert.Contains(t, v, "init")

	// Asked for less room than the rows need, it shows what fits.
	short := Build(cols, rows, 0, headerRows+1, 80, true).View()
	assert.Contains(t, short, "USER")
	assert.Equal(t, headerRows+1, strings.Count(short, "\n")+1, "must honour the height cap")
}

func TestParseTable_FitsWidth(t *testing.T) {
	cols, _, ok := Parse(psSample, 30)
	require.True(t, ok)
	total := 0
	for _, c := range cols {
		total += c.Width
	}
	assert.LessOrEqual(t, total, 30)
}
