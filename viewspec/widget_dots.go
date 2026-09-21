package viewspec

import "fmt"

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

func (dotsWidget) Describe() Description {
	return Description{
		What:     "one row per line led by a coloured status glyph, for output about health",
		Needs:    []string{"field", "accent naming the field that carries the state"},
		NotFor:   "rows with several fields worth reading, which is a table",
		Examples: []string{"systemctl list-units", "docker ps status", "a service health check"},
	}
}
