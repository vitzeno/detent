package viewspec

import "errors"

// dots leads each row with a status glyph, giving the state its own
// column so the left edge says which rows are healthy.
type dotsWidget struct{}

var (
	_ Validator = dotsWidget{}
	_ Selector  = dotsWidget{}
	_ Described = dotsWidget{}
)

func (dotsWidget) Validate(b Block, fields []string) error {
	if len(fields) == 0 {
		return ErrNoRows
	}
	if b.Accent == nil {
		return errors.New("dots needs an accent to colour the glyph by")
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

func (dotsWidget) Describe() Description {
	return Description{
		What: "one row per line led by a coloured status glyph, for output about health",
		// No Slots: this needs an accent mapping values to roles, which is data rather than a field,
		// so nothing can compose one from field choices alone.
		NotFor:   "rows with several fields worth reading, which is a table",
		Examples: []string{"systemctl list-units", "docker ps status", "a service health check"},
	}
}
