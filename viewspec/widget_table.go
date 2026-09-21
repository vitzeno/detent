package viewspec

import (
	"fmt"
	"strings"
)

type tableWidget struct{}

func (tableWidget) Validate(b Block, fields []string) error {
	if len(fields) == 0 {
		return ErrNoRows
	}
	for _, c := range b.Columns {
		if err := needField(c.Field, fields); err != nil {
			return err
		}
	}
	return checkShared(b, fields)
}

func (tableWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	cols := tableColumns(b, d)
	if len(cols) == 0 {
		return nil, fmt.Errorf("no columns to draw")
	}
	widths := fitColumns(cols, d.Rows, f.Width, f.Paint)
	head := make([]string, len(cols))
	for i, c := range cols {
		head[i] = pad(f.Paint.Truncate(c.title(), widths[i]), widths[i], f.Paint)
	}
	lines := []string{f.Paint.Paint(RoleHeading, strings.Join(head, " "))}
	for i, r := range d.Rows {
		cells := make([]string, len(cols))
		for j, c := range cols {
			cells[j] = pad(f.Paint.Truncate(r[c.Field], widths[j]), widths[j], f.Paint)
		}
		line := strings.Join(cells, " ")
		role := accentRole(b, r)
		if f.Focused && i == f.Cursor {
			role = RoleAccent
		}
		lines = append(lines, f.Paint.Paint(role, line))
	}
	return lines, nil
}

// tableColumns is the block's own columns, or every parsed field in
// source order when it names none. That lets one table spec serve
// output whose columns are not known until it is parsed. A block
// column with no title of its own takes the parse's, so naming a
// column does not cost you the heading the output printed.
func tableColumns(b Block, d Data) []Column {
	if len(b.Columns) == 0 {
		return d.Columns
	}
	titles := make(map[string]string, len(d.Columns))
	for _, c := range d.Columns {
		if c.Title != "" {
			titles[c.Field] = c.Title
		}
	}
	out := make([]Column, len(b.Columns))
	for i, c := range b.Columns {
		if c.Title == "" {
			c.Title = titles[c.Field]
		}
		out[i] = c
	}
	return out
}

// The two row widgets draw a cursor, so both report where it landed.
// A table's header sits above its rows; a list's does not.
func (tableWidget) CursorLine(_ Block, d Data, f Frame) int {
	if n := rowCursor(d, f); n >= 0 {
		return n + 1 // the header sits above the rows
	}
	return -1
}

func (tableWidget) Describe() Description {
	return Description{
		What:     "rows in aligned columns, for reading several fields per record",
		NotFor:   "comparing one number across rows, where bar shows the shape at a glance",
		Examples: []string{"docker ps", "ps aux", "a package list with status and duration"},
	}
}
