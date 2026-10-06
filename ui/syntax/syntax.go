// Package syntax colours code by its language for the review's diff. It sets
// the foreground alone, so a line keeps whatever background it is drawn on.
package syntax

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// Hunk colours a hunk's lines of the file at path in the chroma style named,
// as one run so a comment or string spanning lines is coloured right. It is
// nil when no lexer knows the file, and otherwise one line out for each in.
func Hunk(path string, lines []string, style string) []string {
	lexer := lexers.Match(filepath.Base(path))
	if lexer == nil || len(lines) == 0 {
		return nil
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, strings.Join(lines, "\n"))
	if err != nil {
		return nil
	}
	st := styles.Get(style)
	out := make([]string, 0, len(lines))
	var cur strings.Builder
	for tok := it(); tok != chroma.EOF; tok = it() {
		for i, part := range strings.Split(tok.Value, "\n") {
			if i > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			cur.WriteString(paint(st.Get(tok.Type), part))
		}
	}
	out = append(out, cur.String())
	// A lexer that adds or drops a trailing newline must not shift the lines.
	for len(out) < len(lines) {
		out = append(out, lines[len(out)])
	}
	return out[:len(lines)]
}

// paint is part in entry's colour and weight, reset to the default foreground
// and weight after it, never a full reset, which would clear the background too.
func paint(entry chroma.StyleEntry, part string) string {
	if part == "" || !entry.Colour.IsSet() {
		return part
	}
	c := entry.Colour
	on := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.Red(), c.Green(), c.Blue())
	if entry.Bold == chroma.Yes {
		return on + "\x1b[1m" + part + "\x1b[22m\x1b[39m"
	}
	return on + part + "\x1b[39m"
}
