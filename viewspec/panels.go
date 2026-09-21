package viewspec

import (
	"fmt"
	"strings"
)

// stat is one number drawn large, for a pane's worth of summary. Three
// rows of box glyphs rather than a bigger font: a terminal has exactly
// one cell size, so size has to come out of the glyphs themselves.
type statWidget struct{}

func (statWidget) Validate(b Block, fields []string) error {
	if b.Title == "" {
		return fmt.Errorf("stat needs a title to label the number")
	}
	if b.CountWhere == "" {
		return checkShared(b, fields)
	}
	m, err := parseMatch(b.CountWhere)
	if err != nil {
		return err
	}
	if !m.all {
		if err := needField(m.field, fields); err != nil {
			return err
		}
	}
	return checkShared(b, fields)
}

func (statWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	value := fmt.Sprintf("%d", len(d.Rows))
	if b.CountWhere != "" {
		hit, err := parseMatch(b.CountWhere)
		if err != nil {
			return nil, err
		}
		value = fmt.Sprintf("%d", hit.count(d.Rows))
		if b.Of != "" {
			of, err := parseMatch(b.Of)
			if err != nil {
				return nil, err
			}
			value += "/" + fmt.Sprintf("%d", of.count(d.Rows))
		}
	}
	label := f.Paint.Paint(RoleFaint, f.Paint.Truncate(b.Title, f.Width))
	big, ok := bigNumber(value)
	// Paint.Width, not len: a box glyph is three bytes and one cell.
	if !ok || f.Paint.Width(big[0]) > f.Width {
		return []string{label + " " + f.Paint.Paint(RoleAccent, value)}, nil
	}
	out := make([]string, 0, 4)
	for _, line := range big {
		out = append(out, f.Paint.Paint(RoleAccent, line))
	}
	return append(out, label), nil
}

// dots leads each row with a status glyph, for output whose point is
// which rows are healthy. A list with an accent colours the text; this
// gives the state its own column, so the left edge answers it.
type dotsWidget struct{}

func (dotsWidget) Validate(b Block, fields []string) error {
	if len(fields) == 0 {
		return ErrNoRows
	}
	if b.Accent == nil {
		return fmt.Errorf("dots needs an accent to colour the glyph by")
	}
	if err := needField(b.Field, fields); err != nil {
		return err
	}
	return checkShared(b, fields)
}

func (dotsWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	lines := make([]string, 0, len(d.Rows))
	labelW := 0
	for _, r := range d.Rows {
		labelW = max(labelW, f.Paint.Width(r[b.Field]))
	}
	labelW = min(labelW, f.Width-4)
	for i, r := range d.Rows {
		label := pad(f.Paint.Truncate(r[b.Field], labelW), labelW, f.Paint)
		role := RoleDefault
		if f.Focused && i == f.Cursor {
			role = RoleAccent
		}
		lines = append(lines, f.Paint.Paint(accentRole(b, r), "● ")+
			f.Paint.Paint(role, label)+" "+
			f.Paint.Paint(RoleFaint, r[b.Accent.Field]))
	}
	return lines, nil
}

func (dotsWidget) CursorLine(_ Block, d Data, f Frame) int { return rowCursor(d, f) }

// flow fills the pane with one field in as many columns as fit, the
// way ls does. Two hundred paths down a single column waste four
// fifths of the width and scroll for no reason.
type flowWidget struct{}

func (flowWidget) Validate(b Block, fields []string) error {
	if len(fields) == 0 {
		return ErrNoRows
	}
	if err := needField(b.Field, fields); err != nil {
		return err
	}
	return checkShared(b, fields)
}

func (flowWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	cols, height := flowShape(b, d, f)
	lines := make([]string, height)
	for i, r := range d.Rows {
		at := i % height
		cell := pad(f.Paint.Truncate(r[b.Field], cols-1), cols, f.Paint)
		role := RoleDefault
		if f.Focused && i == f.Cursor {
			role = RoleAccent
		}
		lines[at] += f.Paint.Paint(role, cell)
	}
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return lines, nil
}

// flowShape is the cell width and how many lines tall the grid runs.
// Filling column by column keeps the values in reading order down each
// column, which is the order ls uses and the one a cursor can follow.
func flowShape(b Block, d Data, f Frame) (cellWidth, height int) {
	widest := 0
	for _, r := range d.Rows {
		widest = max(widest, f.Paint.Width(r[b.Field]))
	}
	cellWidth = min(widest+2, f.Width)
	across := max(f.Width/max(cellWidth, 1), 1)
	height = (len(d.Rows) + across - 1) / across
	return cellWidth, max(height, 1)
}

func (flowWidget) CursorLine(b Block, d Data, f Frame) int {
	if f.Cursor < 0 || f.Cursor >= len(d.Rows) {
		return -1
	}
	_, height := flowShape(b, d, f)
	return f.Cursor % height
}

// bigDigits is a three-row box-drawing face. Only the glyphs a counted
// number can contain, since nothing else reaches it.
var bigDigits = map[rune][3]string{
	'0': {"┌─┐", "│ │", "└─┘"},
	'1': {" ┐ ", " │ ", " ╵ "},
	'2': {"┌─┐", "┌─┘", "└─╴"},
	'3': {"┌─┐", "╶─┤", "└─┘"},
	'4': {"╷ ╷", "└─┤", "  ╵"},
	'5': {"┌─╴", "└─┐", "└─┘"},
	'6': {"┌─╴", "├─┐", "└─┘"},
	'7': {"┌─┐", "  │", "  ╵"},
	'8': {"┌─┐", "├─┤", "└─┘"},
	'9': {"┌─┐", "└─┤", "└─┘"},
	'/': {"  ╱", " ╱ ", "╱  "},
	'%': {"▪ ╱", " ╱ ", "╱ ▪"},
	'.': {"   ", "   ", " ▪ "},
	'-': {"   ", "╶─╴", "   "},
}

// bigNumber renders s three rows tall, or reports that it holds a
// glyph the face has no shape for.
func bigNumber(s string) ([3]string, bool) {
	var out [3]string
	for i, c := range s {
		glyph, ok := bigDigits[c]
		if !ok {
			return out, false
		}
		for row := range out {
			if i > 0 {
				out[row] += " "
			}
			out[row] += glyph[row]
		}
	}
	return out, true
}

// What each panel is for, and the one it is most likely confused with.

func (statWidget) Describe() Description {
	return Description{
		What:     "one counted number drawn large with a label, for the headline figure of a pane",
		Needs:    []string{"title", "count_where as field=value when counting a subset, else omit it", "of"},
		NotFor:   "a proportion you want drawn as a bar, which is meter",
		Examples: []string{"how many containers are running", "how many files changed"},
	}
}

func (dotsWidget) Describe() Description {
	return Description{
		What:     "one row per line led by a coloured status glyph, for output about health",
		Needs:    []string{"field", "accent naming the field that carries the state"},
		NotFor:   "rows with several fields worth reading, which is a table",
		Examples: []string{"systemctl list-units", "docker ps status", "a service health check"},
	}
}

func (flowWidget) Describe() Description {
	return Description{
		What:     "one field filled across the pane in as many columns as fit, the way ls does",
		Needs:    []string{"field"},
		NotFor:   "values whose nesting matters, which is tree",
		Examples: []string{"a long list of short names", "branch names", "installed packages"},
	}
}
