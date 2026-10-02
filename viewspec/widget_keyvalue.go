package viewspec

import "fmt"

// keyvalue reads the first two columns as label and value, one pair
// per row, aligned on the widest label.
type keyvalueWidget struct{}

var (
	_ Validator = keyvalueWidget{}
	_ Selector  = keyvalueWidget{}
	_ Described = keyvalueWidget{}
)

func (keyvalueWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 2 {
		return fmt.Errorf("keyvalue needs exactly two columns")
	}
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

func (keyvalueWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	key, val := b.Columns[0].Field, b.Columns[1].Field
	w := 0
	for _, r := range d.Rows {
		if n := f.Paint.Width(r[key]); n > w {
			w = n
		}
	}
	w = min(w, f.Width/2)
	lines := make([]string, 0, len(d.Rows))
	for i, r := range d.Rows {
		role := accentRole(b, r)
		if f.Focused && i == f.Cursor {
			role = RoleAccent
		}
		label := f.Paint.Paint(RoleMuted, pad(f.Paint.Truncate(r[key], w), w, f.Paint))
		lines = append(lines, label+"  "+
			f.Paint.Paint(role, f.Paint.Truncate(r[val], max(1, f.Width-w-2))))
	}
	return lines, nil
}

func (keyvalueWidget) CursorLine(_ Block, d Data, f Frame) int { return rowCursor(d, f) }

func (keyvalueWidget) Describe() Description {
	return Description{
		What: "label and value per row, aligned on the label",
		Needs: []Slot{
			{Name: "label", What: "the key"},
			{Name: "value", What: "the value beside it"},
		},
		NotFor:   "many records of the same shape, which is a table",
		Examples: []string{"env", "git config -l", "one object's fields"},
	}
}
