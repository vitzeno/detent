package viewspec

import (
	"fmt"
	"slices"
	"strings"
)

// boxplot summarises each group's spread on one line: the box is the
// middle half, the whiskers the rest, the bright cell the median. A
// column of numbers hides the outlier a box shows at once.
type boxplotWidget struct{}

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

// series draws one sparkline per group against one shared scale, so
// the strips can be compared with each other rather than each read on
// its own the way a single sparkline is.
type seriesWidget struct{}

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

// delta shows what a number moved to and how far, because two columns
// of digits make the reader do the subtraction. Direction is drawn but
// never judged: smaller is better for a build, worse for coverage, and
// only the spec's accent knows which this is.
type deltaWidget struct{}

func (deltaWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 3 {
		return fmt.Errorf("delta needs a label column, a from column and a to column")
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

// groupValues buckets the rows by the first column, keeping the order
// each group was first seen in. Sorting them would throw away whatever
// order the command chose to print, which is usually the useful one.
func groupValues(b Block, d Data) (map[string][]float64, []string) {
	key, value := b.Columns[0].Field, b.Columns[1].Field
	groups := map[string][]float64{}
	var order []string
	for _, r := range d.Rows {
		if _, seen := groups[r[key]]; !seen {
			order = append(order, r[key])
		}
		groups[r[key]] = append(groups[r[key]], number(r[value]))
	}
	return groups, order
}

func (boxplotWidget) Describe() Description {
	return Description{
		What:     "each group's spread on one line: whiskers to the extremes, a box over the middle half, a bright median",
		Needs:    []string{"columns (exactly two: the group, then the number)"},
		NotFor:   "one number per row, which is bar",
		Examples: []string{"latency per endpoint", "test time per package", "file size per directory"},
	}
}

func (seriesWidget) Describe() Description {
	return Description{
		What:     "one sparkline per group on a shared scale, for comparing shapes rather than reading one",
		Needs:    []string{"columns (exactly two: the group, then the number)"},
		NotFor:   "a single series, which is sparkline",
		Examples: []string{"requests per host over time", "usage per core"},
	}
}

func (deltaWidget) Describe() Description {
	return Description{
		What:     "what a number moved to, with the direction and the size of the move worked out for you",
		Needs:    []string{"columns (exactly three: label, the old number, then the new one)"},
		NotFor:   "a single number with nothing to compare against, which is bar",
		Examples: []string{"benchmark before and after", "quota used against limit"},
	}
}
