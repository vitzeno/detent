// Package view draws a request's view: the model's final answer as markdown
// through glamour, with the typed blocks markdown cannot draw.
package view

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/vitzeno/detent/defuse"
	"github.com/vitzeno/detent/ui/markdown"
	"github.com/vitzeno/detent/ui/theme"
)

// margin is glamour's own left margin, which blocks take too so they line up with the text.
const margin = "  "

// drawers are the fenced block languages view draws itself. Any other fence is code.
var drawers = map[string]func(body string, s styles, width int) ([]string, error){
	"chart":  drawChart,
	"stats":  drawStats,
	"status": drawStatus,
}

// Render draws text width cells wide in t's colours. No line is wider, and a
// typed block that does not parse is drawn as the code it is, with the reason.
func Render(text string, t theme.Theme, width int) string {
	width = max(width, 1)
	s := newStyles(t)
	var out []string
	for i, p := range split(defuse.Text(text)) {
		lines := p.draw(s, t.Markdown, width)
		if len(lines) == 0 {
			continue
		}
		if i > 0 && len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, lines...)
	}
	for i, line := range out {
		if ansi.StringWidth(line) > width {
			out[i] = ansi.Truncate(line, width, "…")
		}
	}
	return strings.Join(out, "\n")
}

// Title is what history names a view by: its first heading, else its first line,
// whose leading bold is a title of its own, as a model opens its reasoning.
func Title(text string) string {
	first := ""
	for line := range strings.SplitSeq(defuse.Text(text), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			return strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
		if first == "" && line != "" {
			first = line
		}
	}
	if rest, ok := strings.CutPrefix(first, "**"); ok {
		if bold, _, closed := strings.Cut(rest, "**"); closed && bold != "" {
			return bold
		}
	}
	return first
}

// piece is a run of markdown, or one typed block when lang is set.
type piece struct {
	lang, body string
}

func (p piece) draw(s styles, style string, width int) []string {
	if p.lang == "" {
		return prose(p.body, style, width)
	}
	lines, err := drawers[p.lang](p.body, s, max(width-len(margin), 1))
	if err == nil {
		for i, line := range lines {
			lines[i] = margin + line
		}
		return lines
	}
	// Shown as written, so a bad block costs its drawing and never the reply.
	note := margin + s.faint.Render(p.lang+" block not drawn: "+err.Error())
	return append([]string{note}, prose("```yaml\n"+p.body+"```\n", style, width)...)
}

func prose(body, style string, width int) []string {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	out, err := markdown.Render(body, style, width)
	if err != nil {
		return strings.Split(strings.TrimRight(body, "\n"), "\n")
	}
	return strings.Split(strings.Trim(out, "\n"), "\n")
}

// split cuts text into markdown runs and typed blocks. A fence closes only on
// its own marker at least as long, so a chart quoted inside another fence stays code.
func split(text string) []piece {
	var pieces []piece
	var run, block strings.Builder
	marker, lang := "", ""
	flush := func() {
		if run.Len() > 0 {
			pieces = append(pieces, piece{body: run.String()})
			run.Reset()
		}
	}
	for line := range strings.SplitAfterSeq(text, "\n") {
		trimmed := strings.TrimRight(line, "\r\n")
		switch {
		case marker == "":
			m, l := fence(trimmed)
			if m != "" && drawers[l] != nil {
				flush()
				marker, lang = m, l
				continue
			}
			if m != "" {
				marker, lang = m, ""
			}
			run.WriteString(line)
		case closes(trimmed, marker):
			if lang != "" {
				pieces = append(pieces, piece{lang: lang, body: block.String()})
				block.Reset()
			} else {
				run.WriteString(line)
			}
			marker, lang = "", ""
		case lang != "":
			block.WriteString(line)
		default:
			run.WriteString(line)
		}
	}
	// An unclosed typed block is drawn as far as it goes, as markdown draws an unclosed fence.
	if lang != "" {
		pieces = append(pieces, piece{lang: lang, body: block.String()})
	}
	flush()
	return pieces
}

// fence reads an opening fence of three or more backticks or tildes, with its language.
func fence(line string) (marker, lang string) {
	if len(line) < 3 || (line[0] != '`' && line[0] != '~') {
		return "", ""
	}
	n := 0
	for n < len(line) && line[n] == line[0] {
		n++
	}
	if n < 3 {
		return "", ""
	}
	info := strings.TrimSpace(line[n:])
	if line[0] == '`' && strings.Contains(info, "`") {
		return "", ""
	}
	lang, _, _ = strings.Cut(info, " ")
	return line[:n], strings.ToLower(lang)
}

func closes(line, marker string) bool {
	trimmed := strings.TrimSpace(line)
	return len(trimmed) >= len(marker) && strings.Trim(trimmed, marker[:1]) == "" && trimmed[0] == marker[0]
}
