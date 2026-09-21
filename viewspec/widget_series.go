package viewspec

import (
	"fmt"
	"strings"
)

// series draws one sparkline per group against one shared scale, so
// the strips can be compared with each other rather than each read on
// its own the way a single sparkline is.
type seriesWidget struct{}

var (
	_ Validator = seriesWidget{}
	_ Described = seriesWidget{}
)

func (seriesWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 2 {
		return fmt.Errorf("series needs a group column and a value column")
	}
	return checkColumns(b, fields)
}

func (seriesWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	groups, order := groupValues(b, d)
	all := make([]float64, 0, len(d.Rows))
	for _, g := range order {
		all = append(all, groups[g]...)
	}
	lo, hi := bounds(all)
	labelW := 0
	for _, g := range order {
		labelW = max(labelW, f.Paint.Width(g))
	}
	labelW = min(labelW, f.Width/3)
	lines := titleLine(b, f)
	for _, g := range order {
		var strip strings.Builder
		for _, v := range groups[g] {
			strip.WriteRune(sparkCells[int(fraction(v, lo, hi)*float64(len(sparkCells)-1))])
		}
		lines = append(lines,
			f.Paint.Paint(RoleMuted, pad(f.Paint.Truncate(g, labelW), labelW, f.Paint))+" "+
				f.Paint.Paint(RoleAccent, f.Paint.Truncate(strip.String(), f.Width-labelW-1)))
	}
	return append(lines, f.Paint.Paint(RoleFaint, fmt.Sprintf("%g..%g on one scale", lo, hi))), nil
}

func (seriesWidget) Describe() Description {
	return Description{
		What:     "one sparkline per group on a shared scale, for comparing shapes rather than reading one",
		Needs:    []string{"columns (exactly two: the group, then the number)"},
		NotFor:   "a single series, which is sparkline",
		Examples: []string{"requests per host over time", "usage per core"},
	}
}
