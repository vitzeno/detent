package slash

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchSlash(t *testing.T) {
	assert.Len(t, Match("/"), 3)
	assert.Equal(t, []Cmd{{"/quit", "quit detent"}}, Match("/q"))
	assert.Equal(t, []Cmd{{"/abort", "abort the running command"}}, Match("/a"))
	assert.Empty(t, Match("/x"))
	assert.Empty(t, Match("quit"), "no leading slash matches nothing")
	assert.True(t, Exact("/quit"))
	assert.False(t, Exact("/q"))
}

func TestView(t *testing.T) {
	v := View(Match("/"), 0)
	require.Contains(t, v, "/quit")
	require.Contains(t, v, "/abort")
	require.Contains(t, v, "/help")
	assert.Empty(t, View(nil, 0))
}
