package markdown

import (
	"testing"

	"charm.land/glamour/v2/styles"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/ui/theme"
)

func TestWants(t *testing.T) {
	const doc = "# Title\n\nSome prose with a [link](https://x).\n"
	tests := []struct {
		name, command, output string
		want                  bool
	}{
		{"a markdown file", "cat README.md", "whatever", true},
		{"any case, any extension", "cat docs/NOTES.MARKDOWN", "whatever", true},
		{"a doc piped from nowhere", "ls", doc, true},
		{"a second heading", "ls", "# Title\nbody\n## More\n", true},
		{"go source", "cat main.go", "package main\n", false},
		{"nothing", "ls", "", false},
		{"yaml with a comment header", "cat config.yaml", "# settings\nkey: value\n", false},
		{"a named file wins over a body", "cat notes.txt", doc, false},
		{"a shell script", "cat run", "#!/bin/sh\n# does things\necho hi\n", false},
		{"a Dockerfile", "cat Dockerfile", "# base\nFROM alpine\nRUN apk add git\n", false},
		{"one heading alone", "ls", "# Title\nbody\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Wants(tt.command, tt.output))
		})
	}
}

func TestRender(t *testing.T) {
	out, err := Render("# Hi\n\nbody\n", "dark", 60)
	require.NoError(t, err)
	assert.Contains(t, out, "Hi")
}

// A typo in a theme's style name would drop every document to plain
// text with no error anyone sees.
func TestRender_EveryThemeNamesAStyleGlamourKnows(t *testing.T) {
	for _, name := range theme.Names() {
		style := theme.Themes[name].Markdown
		assert.Contains(t, styles.DefaultStyles, style, "theme %s", name)
		_, err := Render("# x", style, 40)
		assert.NoError(t, err, "theme %s", name)
	}
}
