package viewspec

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

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

func (heatmapWidget) Describe() Description {
	return Description{
		What:     "a shaded grid of two fields crossed against each other, darker where there is more",
		Needs:    []string{"columns (two to cross and count, or three where the third is the value)"},
		NotFor:   "one field summarised on its own, which is histogram",
		Examples: []string{"commits by weekday and hour", "errors by host and service"},
	}
}
