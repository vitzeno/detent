package viewspec

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// histogram is the one widget that aggregates: it groups rows by a
// field and charts how many landed in each. The model cannot do this
// for itself, since it writes no data and a count is data.
type histogramWidget struct{}

func (histogramWidget) Validate(b Block, fields []string) error {
	if len(fields) == 0 {
		return ErrNoRows
	}
	if err := needField(b.Field, fields); err != nil {
		return err
	}
	return checkShared(b, fields)
}

func (histogramWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	counts := map[string]int{}
	for _, r := range d.Rows {
		counts[r[b.Field]]++
	}
	keys := slices.Sorted(maps.Keys(counts))
	// Biggest first, so the shape reads without counting cells.
	slices.SortStableFunc(keys, func(x, y string) int { return counts[y] - counts[x] })

	notes := make([]string, len(keys))
	hi := 0
	for i, k := range keys {
		notes[i] = strconv.Itoa(counts[k])
		hi = max(hi, counts[k])
	}
	lay, err := layOutBars(keys, notes, f)
	if err != nil {
		return nil, err
	}
	lines := titleLine(b, f)
	for i, k := range keys {
		n := 0
		if hi > 0 {
			n = counts[k] * lay.bar / hi
		}
		lines = append(lines, lay.row(k, n, RoleAccent, notes[i], f))
	}
	return lines, nil
}

// gauge draws a percentage against a fixed 0 to 100, coloured by how
// full it is. bar scales to the largest value in its column, which is
// the wrong picture for a disk at 90% beside one at 95%.
type gaugeWidget struct{}

func (gaugeWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 2 {
		return fmt.Errorf("gauge needs a label column and a percentage column")
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

func (gaugeWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	label, pct := b.Columns[0].Field, b.Columns[1].Field
	labelW := 0
	for _, r := range d.Rows {
		labelW = max(labelW, f.Paint.Width(r[label]))
	}
	labelW = min(labelW, f.Width/3)
	cells := f.Width - labelW - 7
	if cells < 1 {
		return nil, fmt.Errorf("no width to chart in")
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
			f.Paint.Paint(RoleMuted, pad(f.Paint.Truncate(r[label], labelW), labelW, f.Paint))+" "+
				f.Paint.Paint(role, strings.Repeat("█", n))+
				f.Paint.Paint(RoleFaint, strings.Repeat("░", cells-n))+
				f.Paint.Paint(RoleDefault, fmt.Sprintf(" %3.0f%%", v)))
	}
	return lines, nil
}

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

// stack draws one line of proportional segments, for composition
// rather than comparison. The legend is not decoration: a band of
// colour with nothing naming the bands says nothing at all.
type stackWidget struct{}

func (stackWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 2 {
		return fmt.Errorf("stack needs a label column and a value column")
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

// stackRoles cycle when no accent map names a colour per label.
var stackRoles = []Role{RoleAccent, RoleSafe, RoleCaution, RoleDanger, RoleMuted}

func (stackWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	label, value := b.Columns[0].Field, b.Columns[1].Field
	total := 0.0
	for _, r := range d.Rows {
		total += max(number(r[value]), 0)
	}
	if total <= 0 {
		return nil, fmt.Errorf("stack has nothing to divide")
	}
	widths := shareWidth(d.Rows, value, total, f.Width)

	var band, legend strings.Builder
	left := f.Width
	for i, r := range d.Rows {
		role := stackRoles[i%len(stackRoles)]
		if b.Accent != nil {
			role = accentRole(b, r)
		}
		band.WriteString(f.Paint.Paint(role, strings.Repeat("█", widths[i])))

		// Measured before painting: escapes are not cells, and a
		// truncate over painted text cuts one in half.
		name := fmt.Sprintf("%s %.0f%%", r[label], number(r[value])/total*100)
		if cost := f.Paint.Width(name) + 4; cost <= left {
			legend.WriteString(f.Paint.Paint(role, "■ ") + f.Paint.Paint(RoleMuted, name) + "  ")
			left -= cost
		}
	}
	lines := titleLine(b, f)
	return append(lines, band.String(), strings.TrimRight(legend.String(), " ")), nil
}

// shareWidth splits total width by each row's share, then hands the
// rounding remainder to the largest so the band always fills exactly.
func shareWidth(rows []Row, field string, total float64, width int) []int {
	out := make([]int, len(rows))
	used, biggest := 0, 0
	for i, r := range rows {
		out[i] = int(max(number(r[field]), 0) / total * float64(width))
		used += out[i]
		if out[i] > out[biggest] {
			biggest = i
		}
	}
	if len(out) > 0 {
		out[biggest] += width - used
	}
	return out
}

// diverge charts two numbers per row either side of a centre line,
// which is the shape a diffstat already has: forty added against three
// removed, read in one glance rather than two columns.
type divergeWidget struct{}

func (divergeWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 3 {
		return fmt.Errorf("diverge needs a label column and two value columns")
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

func (divergeWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	label, left, right := b.Columns[0].Field, b.Columns[1].Field, b.Columns[2].Field
	labelW, noteW, hi := 0, 0, 0.0
	for _, r := range d.Rows {
		labelW = max(labelW, f.Paint.Width(r[label]))
		noteW = max(noteW, f.Paint.Width(r[left])+f.Paint.Width(r[right])+1)
		hi = max(hi, max(number(r[left]), number(r[right])))
	}
	labelW = min(labelW, f.Width/3)
	wing := (f.Width - labelW - noteW - 4) / 2
	if wing < 1 {
		return nil, fmt.Errorf("no width to chart in")
	}
	lines := titleLine(b, f)
	for _, r := range d.Rows {
		l, rt := 0, 0
		if hi > 0 {
			l = int(number(r[left]) / hi * float64(wing))
			rt = int(number(r[right]) / hi * float64(wing))
		}
		lines = append(lines,
			f.Paint.Paint(RoleMuted, pad(f.Paint.Truncate(r[label], labelW), labelW, f.Paint))+" "+
				strings.Repeat(" ", wing-l)+f.Paint.Paint(RoleDanger, strings.Repeat("█", l))+
				f.Paint.Paint(RoleFaint, "│")+
				f.Paint.Paint(RoleSafe, strings.Repeat("█", rt))+strings.Repeat(" ", wing-rt)+" "+
				f.Paint.Paint(RoleFaint, r[left]+" "+r[right]))
	}
	return lines, nil
}

// scatter plots two numeric fields against each other. Braille packs
// two by four dots per cell, so a pane sixty wide carries a hundred
// and twenty plot columns and reads as a curve, not a row of bars.
type scatterWidget struct{}

func (scatterWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 2 {
		return fmt.Errorf("scatter needs an x column and a y column")
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

// scatterRows is how tall the plot draws. Height is resolution here:
// every line is four more dot rows, so a tall pane is a finer curve.
func scatterRows(f Frame) int {
	if f.Height <= 0 {
		return 8
	}
	return min(max(f.Height-2, 4), 24)
}

// brailleBits maps a dot's row and column inside a cell onto its bit.
// The order is the Unicode block's own, which is not the reading order.
var brailleBits = [4][2]byte{
	{0x01, 0x08},
	{0x02, 0x10},
	{0x04, 0x20},
	{0x40, 0x80},
}

func (scatterWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	xf, yf := b.Columns[0].Field, b.Columns[1].Field
	xs := make([]float64, len(d.Rows))
	ys := make([]float64, len(d.Rows))
	for i, r := range d.Rows {
		xs[i], ys[i] = number(r[xf]), number(r[yf])
	}
	if len(xs) == 0 {
		return nil, fmt.Errorf("no points to plot")
	}
	xlo, xhi := bounds(xs)
	ylo, yhi := bounds(ys)
	if f.Width < 4 {
		return nil, fmt.Errorf("no width to plot in")
	}
	rows := scatterRows(f)
	cells := make([][]byte, rows)
	for i := range cells {
		cells[i] = make([]byte, f.Width)
	}
	for i := range xs {
		px := int(fraction(xs[i], xlo, xhi) * float64(f.Width*2-1))
		py := int((1 - fraction(ys[i], ylo, yhi)) * float64(rows*4-1))
		cells[py/4][px/2] |= brailleBits[py%4][px%2]
	}
	lines := titleLine(b, f)
	for _, row := range cells {
		var line strings.Builder
		for _, bits := range row {
			line.WriteRune(rune(0x2800 + int(bits)))
		}
		lines = append(lines, f.Paint.Paint(RoleAccent, line.String()))
	}
	return append(lines, f.Paint.Paint(RoleFaint,
		fmt.Sprintf("%s %g..%g   %s %g..%g", xf, xlo, xhi, yf, ylo, yhi))), nil
}

// heatmap shades a grid of two fields crossed against each other:
// commits by weekday and hour, errors by host and service. A third
// column is the value; without one it counts the rows in each cell.
type heatmapWidget struct{}

func (heatmapWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 2 && len(b.Columns) != 3 {
		return fmt.Errorf("heatmap needs a row column, a column column, and optionally a value")
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

var shades = []rune("·░▒▓█")

func (heatmapWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	rowF, colF := b.Columns[0].Field, b.Columns[1].Field
	cell := map[string]float64{}
	rowKeys, colKeys := map[string]bool{}, map[string]bool{}
	hi := 0.0
	for _, r := range d.Rows {
		rowKeys[r[rowF]], colKeys[r[colF]] = true, true
		key := r[rowF] + "\x00" + r[colF]
		if len(b.Columns) == 3 {
			cell[key] += number(r[b.Columns[2].Field])
		} else {
			cell[key]++
		}
		hi = max(hi, cell[key])
	}
	rows := slices.Sorted(maps.Keys(rowKeys))
	cols := slices.Sorted(maps.Keys(colKeys))

	labelW := 0
	for _, k := range rows {
		labelW = max(labelW, f.Paint.Width(k))
	}
	labelW = min(labelW, f.Width/3)
	if width := f.Width - labelW - 1; len(cols) > width {
		if width < 1 {
			return nil, fmt.Errorf("no width to chart in")
		}
		cols = cols[:width]
	}

	// A grid of single cells leaves most of a wide pane empty, so each
	// one takes the spare width up to a square-ish three.
	cw := min(max((f.Width-labelW-1)/len(cols), 1), 3)
	lines := titleLine(b, f)
	for _, head := range headerRows(cols) {
		lines = append(lines, strings.Repeat(" ", labelW+1)+
			f.Paint.Paint(RoleFaint, strings.TrimRight(spread(head, cw), " ")))
	}
	for _, rk := range rows {
		var band strings.Builder
		for _, ck := range cols {
			band.WriteString(strings.Repeat(string(shadeFor(cell[rk+"\x00"+ck], hi)), cw))
		}
		lines = append(lines,
			f.Paint.Paint(RoleMuted, pad(f.Paint.Truncate(rk, labelW), labelW, f.Paint))+" "+
				f.Paint.Paint(RoleAccent, band.String()))
	}
	return lines, nil
}

// spread pads each rune of a header out to the cell width, so the
// axis keeps sitting over the column it names.
func spread(s string, cw int) string {
	var out strings.Builder
	for _, r := range s {
		out.WriteRune(r)
		out.WriteString(strings.Repeat(" ", cw-1))
	}
	return out.String()
}

func shadeFor(v, hi float64) rune {
	if hi <= 0 || v <= 0 {
		return shades[0]
	}
	i := int(v / hi * float64(len(shades)-1))
	return shades[min(i, len(shades)-1)]
}

// barLayout is the label, bar and value columns every row-per-bar
// widget shares, so stacking two of them in one view lines them up.
type barLayout struct{ label, bar, note int }

func layOutBars(labels, notes []string, f Frame) (barLayout, error) {
	var l barLayout
	for _, s := range labels {
		l.label = max(l.label, f.Paint.Width(s))
	}
	for _, s := range notes {
		l.note = max(l.note, f.Paint.Width(s))
	}
	l.label = min(l.label, f.Width/3)
	l.bar = f.Width - l.label - l.note - 2
	if l.bar < 1 {
		return l, fmt.Errorf("no width to chart in")
	}
	return l, nil
}

func (l barLayout) row(label string, filled int, role Role, note string, f Frame) string {
	return f.Paint.Paint(RoleMuted, pad(f.Paint.Truncate(label, l.label), l.label, f.Paint)) + " " +
		f.Paint.Paint(role, strings.Repeat("█", filled)) +
		strings.Repeat(" ", l.bar-filled) + " " +
		f.Paint.Paint(RoleFaint, pad(note, l.note, f.Paint))
}

// titleLine starts a chart's lines with its label, or with nothing.
func titleLine(b Block, f Frame) []string {
	if b.Title == "" {
		return nil
	}
	return []string{f.Paint.Paint(RoleFaint, f.Paint.Truncate(b.Title, f.Width))}
}

func bounds(v []float64) (lo, hi float64) {
	lo, hi = v[0], v[0]
	for _, x := range v {
		lo, hi = min(lo, x), max(hi, x)
	}
	return lo, hi
}

// fraction places v in 0..1 across the span, centring it when every
// value is the same rather than dividing by nothing.
func fraction(v, lo, hi float64) float64 {
	if hi <= lo {
		return 0.5
	}
	return (v - lo) / (hi - lo)
}

// headerRows stacks a column key one rune per line, so "09" and "10"
// keep both digits instead of collapsing onto a row of zeroes and ones.
func headerRows(cols []string) []string {
	depth := 0
	for _, k := range cols {
		depth = max(depth, len([]rune(k)))
	}
	out := make([]string, min(depth, 2))
	for d := range out {
		row := make([]rune, len(cols))
		for i, k := range cols {
			row[i] = runeAt(k, d)
		}
		out[d] = string(row)
	}
	return out
}

func runeAt(s string, i int) rune {
	if r := []rune(s); i < len(r) {
		return r[i]
	}
	return ' '
}

func (gaugeWidget) CursorLine(b Block, d Data, f Frame) int { return chartCursor(b, d, f) }

// chartCursor is rowCursor shifted past the title, since every chart
// here draws one when the block names it.
func chartCursor(b Block, d Data, f Frame) int {
	at := rowCursor(d, f)
	if at < 0 || b.Title == "" {
		return at
	}
	return at + 1
}

// What each chart is for, and the one it is most likely confused with.

func (histogramWidget) Describe() Description {
	return Description{
		What:     "groups rows by a field and charts how many fell in each, counting them for you",
		Needs:    []string{"field"},
		NotFor:   "a number the rows already carry, which is bar",
		Examples: []string{"commits per author", "processes per user", "responses per status code"},
	}
}

func (gaugeWidget) Describe() Description {
	return Description{
		What:     "one row per gauge on a fixed 0 to 100 scale, coloured green, amber then red as it fills",
		Needs:    []string{"columns (exactly two: label, then a percentage)"},
		NotFor:   "a number that is not a percentage, where bar's relative scale is the readable one",
		Examples: []string{"df -h use%", "battery or memory percentages"},
	}
}

func (stackWidget) Describe() Description {
	return Description{
		What:     "one band split into proportional segments with a legend, for what a whole is made of",
		Needs:    []string{"columns (exactly two: label, then the number)"},
		NotFor:   "comparing rows against each other, which is bar",
		Examples: []string{"disk used by directory", "lines by language", "time by phase"},
	}
}

func (divergeWidget) Describe() Description {
	return Description{
		What:     "two numbers per row drawn either side of a centre line, for a pair that opposes",
		Needs:    []string{"columns (exactly three: label, left number, right number)"},
		NotFor:   "a single number per row, which is bar",
		Examples: []string{"git diff --numstat added against removed", "passed against failed"},
	}
}

func (scatterWidget) Describe() Description {
	return Description{
		What:     "a braille plot of one numeric field against another, for a relationship or a curve",
		Needs:    []string{"columns (exactly two: x, then y)"},
		NotFor:   "one series read in order, which is sparkline",
		Examples: []string{"size against modified time", "latency against request count"},
	}
}

func (heatmapWidget) Describe() Description {
	return Description{
		What:     "a shaded grid of two fields crossed against each other, darker where there is more",
		Needs:    []string{"columns (two to cross and count, or three where the third is the value)"},
		NotFor:   "one field summarised on its own, which is histogram",
		Examples: []string{"commits by weekday and hour", "errors by host and service"},
	}
}
