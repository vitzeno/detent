// Package syntax colours a diff's code by its language, on the tint of what it added
// or removed. Code sets the foreground alone, so a line keeps the tint it is drawn on.
package syntax

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/x/ansi"

	"github.com/vitzeno/detent/ui/theme"
)

// Hunk colours a hunk's lines of the file at path as one run, so a comment spanning
// lines is coloured right: one line out for each in, nil when no lexer knows the file.
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

// Tinted is a line of coloured code behind its marker, one a diff added (op '+') or
// removed ('-') tinted the width of it, so the review, the output pane and a view agree.
func Tinted(op byte, code string, width int, t theme.Theme) string {
	marker, tint := " ", color.Color(nil)
	switch op {
	case '+':
		marker, tint = sgr(38, t.Safe)+"+"+"\x1b[39m", t.DiffAdded
	case '-':
		marker, tint = sgr(38, t.Danger)+"-"+"\x1b[39m", t.DiffRemoved
	}
	// Not layout.Truncate, which defuses: the code was defused before it was coloured,
	// and all the colouring added is its own colour, which must reach the terminal.
	body := marker + ansi.Truncate(code, max(1, width-1), "…") + "\x1b[22m\x1b[39m"
	if tint == nil {
		return body
	}
	pad := strings.Repeat(" ", max(0, width-ansi.StringWidth(body)))
	return sgr(48, tint) + body + pad + "\x1b[49m"
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

// sgr sets the foreground (38) or background (48) to c.
func sgr(code int, c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", code, r>>8, g>>8, b>>8)
}
