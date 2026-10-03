package viewspec

import (
	"errors"
	"fmt"
	"strings"
)

// gauge draws a percentage against a fixed 0 to 100, coloured by how
// full it is, where bar would scale to the largest value.
type gaugeWidget struct{}

var (
	_ Validator = gaugeWidget{}
	_ Selector  = gaugeWidget{}
	_ Described = gaugeWidget{}
)

func (gaugeWidget) Describe() Description {
	return Description{
		What: "one row per gauge on a fixed 0 to 100 scale, coloured green, amber then red as it fills",
		Needs: []Slot{
			{Name: "label", What: "labels each gauge"},
			{Name: "value", What: "the percentage, 0 to 100"},
		},
		NotFor:   "a number that is not a percentage, where bar's relative scale is the readable one",
		Examples: []string{"df -h use%", "battery or memory percentages"},
	}
}

func (gaugeWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 2 {
		return errors.New("gauge needs a label column and a percentage column")
	}
	return checkColumns(b, fields)
}

func (gaugeWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	label, pct := b.Columns[0].Field, b.Columns[1].Field
	labelW := 0
	for _, r := range d.Rows {
		labelW = max(labelW, f.Paint.Width(r[label]))
	}
	labelW = min(labelW, f.Width/3)
	cells := f.Width - labelW - 7
	if cells < 1 {
		return nil, errNoWidth
	}
	lines := titleLine(b, f)
	for i, r := range d.Rows {
		v := min(max(number(r[pct]), 0), 100)
		n := int(v * float64(cells) / 100)
		role := gaugeRole(v)
		if b.Accent != nil {
			role = accentRole(b, r)
		}
		if f.Focused && i == f.Cursor {
			role = RoleAccent
		}
		lines = append(lines,
			labelCell(r[label], labelW, f)+" "+
				f.Paint.Paint(role, strings.Repeat("█", n))+
				f.Paint.Paint(RoleFaint, strings.Repeat("░", cells-n))+
				f.Paint.Paint(RoleDefault, fmt.Sprintf(" %3.0f%%", v)))
	}
	return lines, nil
}

func (gaugeWidget) CursorLine(b Block, d Data, f Frame) int { return chartCursor(b, d, f) }

// gaugeRole is the thresholds a filling disk is usually read against.
func gaugeRole(pct float64) Role {
	switch {
	case pct >= 90:
		return RoleDanger
	case pct >= 70:
		return RoleCaution
	}
	return RoleSafe
}
