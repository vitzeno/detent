package viewspec

import (
	"errors"
	"fmt"
)

// delta shows what a number moved to and how far. Direction is drawn
// but never judged, since only the spec's accent knows which way is better.
type deltaWidget struct{}

var (
	_ Validator = deltaWidget{}
	_ Selector  = deltaWidget{}
	_ Described = deltaWidget{}
)

func (deltaWidget) Describe() Description {
	return Description{
		What: "what a number moved to, with the direction and the size of the move worked out for you",
		Needs: []Slot{
			{Name: "label", What: "names each row"},
			{Name: "from", What: "the number before"},
			{Name: "to", What: "the number after"},
		},
		NotFor:   "a single number with nothing to compare against, which is bar",
		Examples: []string{"benchmark before and after", "quota used against limit"},
	}
}

func (deltaWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 3 {
		return errors.New("delta needs a label column, a from column and a to column")
	}
	return checkColumns(b, fields)
}

func (deltaWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	label, from, to := b.Columns[0].Field, b.Columns[1].Field, b.Columns[2].Field
	labelW, valueW := 0, 0
	for _, r := range d.Rows {
		labelW = max(labelW, f.Paint.Width(r[label]))
		valueW = max(valueW, f.Paint.Width(r[to]))
	}
	labelW = min(labelW, f.Width/3)
	lines := titleLine(b, f)
	for i, r := range d.Rows {
		was, now := number(r[from]), number(r[to])
		arrow, move := "→", "no change"
		switch {
		case now > was:
			arrow, move = "▲", fmt.Sprintf("+%g", now-was)
		case now < was:
			arrow, move = "▼", fmt.Sprintf("-%g", was-now)
		}
		if was != 0 && now != was {
			move += fmt.Sprintf(" (%+.0f%%)", (now-was)/was*100)
		}
		role := accentOr(b, r, RoleMuted)
		if f.Focused && i == f.Cursor {
			role = RoleAccent
		}
		lines = append(lines,
			f.Paint.Paint(RoleMuted, pad(f.Paint.Truncate(r[label], labelW), labelW, f.Paint))+" "+
				f.Paint.Paint(RoleDefault, pad(r[to], valueW, f.Paint))+" "+
				f.Paint.Paint(role, arrow+" "+move)+
				f.Paint.Paint(RoleFaint, " from "+r[from]))
	}
	return lines, nil
}

func (deltaWidget) CursorLine(b Block, d Data, f Frame) int { return chartCursor(b, d, f) }
