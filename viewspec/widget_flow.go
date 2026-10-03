package viewspec

import "strings"

// flow fills the pane with one field in as many columns as fit, the
// way ls does.
type flowWidget struct{}

var (
	_ Validator = flowWidget{}
	_ Selector  = flowWidget{}
	_ Described = flowWidget{}
)

func (flowWidget) Describe() Description {
	return Description{
		What: "one field filled across the pane in as many columns as fit, the way ls does",
		Needs: []Slot{
			{Name: "field", What: "the value to fill the pane with"},
		},
		NotFor:   "values whose nesting matters, which is tree",
		Examples: []string{"a long list of short names", "branch names", "installed packages"},
	}
}

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

func (flowWidget) CursorLine(b Block, d Data, f Frame) int {
	if f.Cursor < 0 || f.Cursor >= len(d.Rows) {
		return -1
	}
	_, height := flowShape(b, d, f)
	return f.Cursor % height
}

// flowShape is the cell width and how many lines tall the grid runs.
// It fills column by column, as ls does, so a cursor can follow it.
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
