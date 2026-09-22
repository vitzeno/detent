package viewspec

import (
	"fmt"
	"slices"
)

// boxplot summarises each group's spread on one line: the box is the
// middle half, the whiskers the rest, the bright cell the median. A
// column of numbers hides the outlier a box shows at once.
type boxplotWidget struct{}

var (
	_ Validator = boxplotWidget{}
	_ Described = boxplotWidget{}
)

func (boxplotWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 2 {
		return fmt.Errorf("boxplot needs a group column and a value column")
	}
	return checkColumns(b, fields)
}

func (boxplotWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	groups, order := groupValues(b, d)
	lo, hi := 0.0, 0.0
	for i, g := range order {
		l, h := bounds(groups[g])
		if i == 0 {
			lo = l
		}
		lo, hi = min(lo, l), max(hi, h)
	}
	notes := make([]string, len(order))
	for i, g := range order {
		notes[i] = fmt.Sprintf("n=%d", len(groups[g]))
	}
	lay, err := layOutBars(order, notes, f)
	if err != nil {
		return nil, err
	}
	lines := titleLine(b, f)
	for i, g := range order {
		v := slices.Clone(groups[g])
		slices.Sort(v)
		cell := func(q float64) int {
			return int(fraction(q, lo, hi) * float64(lay.bar-1))
		}
		box := make([]rune, lay.bar)
		for j := range box {
			box[j] = ' '
		}
		// Whiskers first, then the box over them, then the median:
		// filling in range order lets the last one clip the box edge.
		fill(box, cell(v[0]), cell(v[len(v)-1]), '─')
		fill(box, cell(quantile(v, 0.25)), cell(quantile(v, 0.75)), '▒')
		box[cell(quantile(v, 0.5))] = '█'
		lines = append(lines,
			f.Paint.Paint(RoleMuted, pad(f.Paint.Truncate(g, lay.label), lay.label, f.Paint))+" "+
				f.Paint.Paint(RoleAccent, string(box))+" "+
				f.Paint.Paint(RoleFaint, pad(notes[i], lay.note, f.Paint)))
	}
	return lines, nil
}

// quantile reads q through an already sorted slice, interpolating
// between neighbours. Taking the nearest index instead lands q3 on the
// maximum for a group of four, and the box swallows its own whisker.
func quantile(sorted []float64, q float64) float64 {
	pos := q * float64(len(sorted)-1)
	i := int(pos)
	if i >= len(sorted)-1 {
		return sorted[len(sorted)-1]
	}
	return sorted[i] + (pos-float64(i))*(sorted[i+1]-sorted[i])
}

func fill(dst []rune, from, to int, r rune) {
	for i := max(from, 0); i <= min(to, len(dst)-1); i++ {
		dst[i] = r
	}
}

func (boxplotWidget) Describe() Description {
	return Description{
		What: "each group's spread on one line: whiskers to the extremes, a box over the middle half, a bright median",
		Needs: []Slot{
			{Name: "group", What: "the group each row belongs to"},
			{Name: "value", What: "the number to spread"},
		},
		NotFor:   "one number per row, which is bar",
		Examples: []string{"latency per endpoint", "test time per package", "file size per directory"},
	}
}
