// Package view draws a request's view: the model's final answer as markdown
// through glamour, with the typed blocks markdown cannot draw.
package view

import (
	"maps"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/vitzeno/detent/defuse"
	"github.com/vitzeno/detent/ui/markdown"
	"github.com/vitzeno/detent/ui/theme"
)

// Frames is how many frames a view takes to arrive. Frame(Frames) is the view at rest.
const Frames = 18

// margin is glamour's own left margin, which blocks take too so they line up with the text.
const margin = "  "

// drawers are the fenced block languages view draws itself. Any other fence is code.
var drawers = map[string]drawer{
	"chart":  drawChart,
	"stats":  drawStats,
	"status": drawStatus,
	"steps":  drawSteps,
	"tree":   drawTree,
	"flow":   drawFlow,
}

// Blocks names the fenced languages a view draws, which the model must be told of.
func Blocks() []string { return slices.Sorted(maps.Keys(drawers)) }

// drawer parses a block once and returns how to draw it at any point of its arrival.
type drawer func(body string, s styles, width int) (drawn, error)

// drawn draws a block p of the way in, 0 as it starts and 1 at rest. Its height
// must not change with p, so a view never jumps while it arrives.
type drawn func(p float64) []string

func still(lines []string) drawn { return func(float64) []string { return lines } }

// Render draws text width cells wide in t's colours, at rest. No line is wider,
// and a typed block that does not parse is drawn as the code it is, with the reason.
func Render(text string, t theme.Theme, width int) string {
	return Layout(text, t, width).Frame(Frames)
}

// Drawing is a view laid out at one width with its markdown already rendered,
// so a frame of its arrival costs only its blocks.
type Drawing struct {
	parts         []part
	final         string
	width, height int
	faint         func(string) string
}

// part is one piece of a drawing, starting at line at.
type part struct {
	draw drawn
	at   int
}

// Layout renders text's markdown once, leaving its blocks to draw each frame.
func Layout(text string, t theme.Theme, width int) Drawing {
	width = max(width, 1)
	s := newStyles(t)
	d := Drawing{width: width, faint: func(line string) string { return s.faint.Render(ansi.Strip(line)) }}
	var final []string
	for _, p := range split(defuse.Text(text)) {
		draw := p.draw(s, t.Markdown, width)
		lines := draw(1)
		if len(lines) == 0 {
			continue
		}
		if len(d.parts) > 0 {
			final = append(final, "")
		}
		d.parts = append(d.parts, part{draw: draw, at: len(final)})
		final = append(final, lines...)
	}
	d.height = len(final)
	d.final = strings.Join(d.fit(final), "\n")
	return d
}

// sweep is the share of the arrival its top-down reveal takes. Each block then
// grows over the rest, starting as the reveal reaches it.
const sweep = 0.4

// Frame draws the view n frames into its arrival, and at rest from Frames on.
func (d Drawing) Frame(n int) string {
	if n >= Frames || d.height == 0 {
		return d.final
	}
	t := float64(max(n, 0)) / Frames
	out := make([]string, 0, d.height)
	for i, p := range d.parts {
		if i > 0 {
			out = append(out, "")
		}
		start := sweep * float64(p.at) / float64(d.height)
		out = append(out, p.draw(ease(clamp((t-start)/(1-sweep))))...)
	}
	// The front of the reveal is drawn faint, so lines fade in rather than appear.
	shown := int(t / sweep * float64(d.height))
	for i := range out {
		switch {
		case i >= shown:
			out[i] = ""
		case i >= shown-2:
			out[i] = d.faint(out[i])
		}
	}
	return strings.Join(d.fit(out), "\n")
}

func (d Drawing) fit(lines []string) []string {
	for i, line := range lines {
		if ansi.StringWidth(line) > d.width {
			lines[i] = ansi.Truncate(line, d.width, "…")
		}
	}
	return lines
}

// ease starts fast and settles, as a thing does arriving under its own weight.
func ease(p float64) float64 {
	q := 1 - p
	return 1 - q*q*q
}

func clamp(p float64) float64 { return min(max(p, 0), 1) }

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

// piece is one part of a view, drawn as its kind says. lang is a block's language
// and alert a callout's kind. source is the markdown a lifted piece was read from.
type piece struct {
	kind                      pieceKind
	lang, alert, body, source string
}

type pieceKind int

const (
	pieceProse pieceKind = iota
	pieceBlock
	pieceAlert
	pieceTable
	pieceHeading
	pieceCode
)

func (p piece) draw(s styles, style string, width int) drawn {
	switch p.kind {
	case pieceProse:
		return counting(p.body, style, width)
	case pieceAlert:
		return still(callout(p.alert, p.body, s, style, width))
	case pieceHeading:
		return shimmering(prose(p.body, style, width), s)
	case pieceCode:
		return typing(prose(p.body, style, width))
	case pieceTable:
		if d, ok := drawTable(p.body, s, max(width-len(margin), 1)); ok {
			return indented(d)
		}
		return still(prose(p.body, style, width))
	case pieceBlock:
		// Drawn below, by its language.
	}
	d, err := drawers[p.lang](p.body, s, max(width-len(margin), 1))
	if err == nil {
		return indented(d)
	}
	// A block lifted from markdown is drawn as the markdown it was, never as an error.
	if p.source != "" {
		return still(prose(p.source, style, width))
	}
	// Shown as written, so a bad block costs its drawing and never the reply.
	note := margin + s.faint.Render(p.lang+" block not drawn: "+err.Error())
	return still(append([]string{note}, prose("```yaml\n"+p.body+"```\n", style, width)...))
}

// indented sets a block in glamour's margin, so it lines up with the text.
func indented(d drawn) drawn {
	return func(t float64) []string {
		lines := d(t)
		out := make([]string, len(lines))
		for i, line := range lines {
			out[i] = margin + line
		}
		return out
	}
}

func prose(body, style string, width int) []string {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	out, err := markdown.Render(body, style, width)
	if err != nil {
		return strings.Split(strings.TrimRight(body, "\n"), "\n")
	}
	// Glamour pads a piece with blank lines, which would stack with the gap between pieces.
	lines := strings.Split(out, "\n")
	blank := func(line string) bool { return strings.TrimSpace(ansi.Strip(line)) == "" }
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// split cuts text into markdown runs, typed blocks and callouts. A fence closes only
// on its own marker at least as long, so a chart quoted inside another fence stays code.
func split(text string) []piece {
	var pieces []piece
	var run, block strings.Builder
	marker, lang, alert := "", "", ""
	flush := func() {
		if run.Len() > 0 {
			pieces = append(pieces, lift(run.String())...)
			run.Reset()
		}
	}
	for line := range strings.SplitAfterSeq(text, "\n") {
		trimmed := strings.TrimRight(line, "\r\n")
		if alert != "" {
			if rest, ok := quoted(trimmed); ok {
				block.WriteString(rest + "\n")
				continue
			}
			pieces = append(pieces, piece{kind: pieceAlert, alert: alert, body: block.String()})
			block.Reset()
			alert = ""
		}
		switch {
		case marker == "":
			if kind, rest := alertOpens(trimmed); kind != "" {
				flush()
				alert = kind
				if rest != "" {
					block.WriteString(rest + "\n")
				}
				continue
			}
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
				pieces = append(pieces, piece{kind: pieceBlock, lang: lang, body: block.String()})
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
	switch {
	case lang != "":
		pieces = append(pieces, piece{kind: pieceBlock, lang: lang, body: block.String()})
	case alert != "":
		pieces = append(pieces, piece{kind: pieceAlert, alert: alert, body: block.String()})
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
