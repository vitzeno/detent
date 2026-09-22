package viewspec

type listWidget struct{}

var (
	_ Validator = listWidget{}
	_ Selector  = listWidget{}
	_ Described = listWidget{}
)

func (listWidget) Validate(b Block, fields []string) error {
	if len(fields) == 0 {
		return ErrNoRows
	}
	if err := needField(b.Field, fields); err != nil {
		return err
	}
	return checkShared(b, fields)
}

func (listWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	lines := make([]string, 0, len(d.Rows))
	for i, r := range d.Rows {
		role := accentRole(b, r)
		if f.Focused && i == f.Cursor {
			role = RoleAccent
		}
		lines = append(lines, f.Paint.Paint(role, f.Paint.Truncate(r[b.Field], f.Width)))
	}
	return lines, nil
}

func (listWidget) CursorLine(_ Block, d Data, f Frame) int { return rowCursor(d, f) }

func (listWidget) Describe() Description {
	return Description{
		What: "one field per line, for a set of names or paths",
		Needs: []Slot{
			{Name: "field", What: "the value to list, one per line"},
		},
		NotFor:   "paths whose nesting matters — tree draws that",
		Examples: []string{"changed files", "branch names"},
	}
}
