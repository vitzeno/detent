package markdown

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWants(t *testing.T) {
	assert.True(t, Wants("cat README.md", "whatever"))
	assert.True(t, Wants("ls", "# Title\nbody"))
	assert.False(t, Wants("cat main.go", "package main\n"))
	assert.False(t, Wants("ls", ""))
}

func TestRender(t *testing.T) {
	out, err := Render("# Hi\n\nbody\n", 60)
	require.NoError(t, err)
	assert.Contains(t, out, "Hi")
}
