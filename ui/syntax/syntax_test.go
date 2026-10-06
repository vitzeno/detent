package syntax

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/ui/theme"
)

// Code is coloured, its text unchanged, one line out for each in.
func TestHunk_ColoursCodeAndKeepsItsText(t *testing.T) {
	lines := []string{"func main() {", `	fmt.Println("hi")`, "}"}
	got := Hunk("cmd/main.go", lines, "github-dark")
	require.Len(t, got, len(lines))
	for i, l := range got {
		assert.Equal(t, lines[i], ansi.Strip(l))
	}
	assert.Contains(t, got[0], "\x1b[38;2;", "a keyword has a colour")
}

// A comment opened on one line colours the next one too, which a line at a
// time would draw as code.
func TestHunk_CarriesAColourAcrossLines(t *testing.T) {
	got := Hunk("a.go", []string{"/* one", "two */", "x := 1"}, "github-dark")
	require.Len(t, got, 3)
	assert.Equal(t, colourOf(got[0]), colourOf(got[1]), "both lines of the comment")
	assert.NotEqual(t, colourOf(got[1]), colourOf(got[2]))
}

// A full reset would clear the tint an added or removed line is drawn on.
func TestHunk_NeverResetsTheBackground(t *testing.T) {
	got := Hunk("a.go", []string{`x := "s" // c`, "return nil"}, "github-dark")
	for _, l := range got {
		assert.NotContains(t, l, "\x1b[0m")
		assert.NotContains(t, l, "\x1b[m")
		assert.NotContains(t, l, "\x1b[49m")
	}
}

// Every theme names a chroma style that exists, since an unknown one quietly
// falls back to a style chosen for nothing.
func TestThemes_NameAStyleChromaHas(t *testing.T) {
	for name, th := range theme.Themes {
		_, ok := styles.Registry[th.Syntax]
		assert.True(t, ok, "%s names %q", name, th.Syntax)
	}
}

func TestHunk_IsNilForAFileNoLexerKnows(t *testing.T) {
	assert.Nil(t, Hunk("notes.unknownext", []string{"x"}, "github-dark"))
}

// A file's contents are untrusted: whatever they hold, a lexer that knows the
// file returns one line for each it was given.
func FuzzHunk(f *testing.F) {
	f.Add("a.go", "func f() {\n/* x\n}")
	f.Add("a.py", "'''\nx\n")
	f.Add("a.md", "# h\n```\ncode")
	f.Fuzz(func(t *testing.T, path, text string) {
		lines := strings.Split(text, "\n")
		if got := Hunk(path, lines, "github-dark"); got != nil && len(got) != len(lines) {
			t.Fatalf("%d lines in, %d out", len(lines), len(got))
		}
	})
}

// colourOf is the first foreground a line sets, "" for none.
func colourOf(s string) string {
	i := strings.Index(s, "\x1b[38;2;")
	if i < 0 {
		return ""
	}
	return s[i : i+strings.IndexByte(s[i:], 'm')]
}
