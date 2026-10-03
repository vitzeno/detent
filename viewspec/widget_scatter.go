package viewspec

import (
	"fmt"
	"strings"
)

// scatter plots two numeric fields against each other in braille, two
// by four dots per cell, so it reads as a curve rather than bars.
type scatterWidget struct{}

var (
	_ Validator = scatterWidget{}
	_ Described = scatterWidget{}
)

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
		return titleLine(b, f), nil
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

func (scatterWidget) Describe() Description {
	return Description{
		What: "a braille plot of one numeric field against another, for a relationship or a curve",
		Needs: []Slot{
			{Name: "x", What: "the horizontal number"},
			{Name: "y", What: "the vertical number"},
		},
		NotFor:   "one series read in order, which is sparkline",
		Examples: []string{"size against modified time", "latency against request count"},
	}
}

// scatterRows is how tall the plot draws. Height is resolution here:
// every line is four more dot rows, so a tall pane is a finer curve.
func scatterRows(f Frame) int {
	if f.Height <= 0 {
		return 8
	}
	return min(max(f.Height-2, 4), 24)
}
