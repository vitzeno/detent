package viewspec

import (
	"fmt"
	"strings"
)

var sparkCells = []rune("▁▂▃▄▅▆▇█")

// sparklineWidget draws one numeric field as a bar strip, scaled to
// the values present rather than to zero. The shape is the point.
type sparklineWidget struct{}

var (
	_ Validator = sparklineWidget{}
	_ Described = sparklineWidget{}
)

func (sparklineWidget) Describe() Description {
	return Description{
		Summarises: true,
		What:       "one compact strip showing the shape of a numeric field across rows",
		Needs: []Slot{
			{Name: "field", What: "the number to plot across rows"},
		},
		NotFor:   "comparing individual rows, where bar is readable and this is not",
		Examples: []string{"a latency series", "sizes over time"},
	}
}

func (sparklineWidget) Validate(b Block, fields []string) error {
	if len(fields) == 0 {
		return ErrNoRows
	}
	return needField(b.Field, fields)
}

func (sparklineWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	values := make([]float64, 0, len(d.Rows))
	for _, r := range d.Rows {
		values = append(values, number(r[b.Field]))
	}
	if len(values) == 0 {
		return titleLine(b, f), nil
	}
	lo, hi := bounds(values)
	var strip strings.Builder
	for _, v := range values {
		i := 0
		if hi > lo {
			i = int((v - lo) / (hi - lo) * float64(len(sparkCells)-1))
		}
		strip.WriteRune(sparkCells[i])
	}
	line := f.Paint.Paint(RoleAccent, strip.String())
	if b.Title != "" {
		line = f.Paint.Paint(RoleFaint, b.Title+" ") + line
	}
	return []string{line + f.Paint.Paint(RoleFaint,
		fmt.Sprintf("  %g–%g", lo, hi))}, nil
}
