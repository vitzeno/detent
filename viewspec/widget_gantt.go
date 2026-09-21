package viewspec

import (
	"fmt"
	"strings"
)

// gantt lays each row on a shared axis from where it starts to how
// long it ran, which is the shape of anything timed: a boot, a build,
// a test run. bar would draw the lengths and lose when they happened.
type ganttWidget struct{}

func (ganttWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 3 {
		return fmt.Errorf("gantt needs a label column, a start column and a length column")
	}
	return checkColumns(b, fields)
}

func (ganttWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	label, startF, lenF := b.Columns[0].Field, b.Columns[1].Field, b.Columns[2].Field
	starts := make([]float64, len(d.Rows))
	lengths := make([]float64, len(d.Rows))
	notes := make([]string, len(d.Rows))
	labels := make([]string, len(d.Rows))
	lo, hi := 0.0, 0.0
	for i, r := range d.Rows {
		starts[i], _ = instant(r[startF])
		lengths[i], _ = instant(r[lenF])
		labels[i], notes[i] = r[label], r[lenF]
		if i == 0 {
			lo = starts[i]
		}
		lo = min(lo, starts[i])
		hi = max(hi, starts[i]+lengths[i])
	}
	lay, err := layOutBars(labels, notes, f)
	if err != nil {
		return nil, err
	}
	lines := titleLine(b, f)
	for i := range d.Rows {
		at := int(fraction(starts[i], lo, hi) * float64(lay.bar))
		width := 1
		if hi > lo {
			width = max(1, int(lengths[i]/(hi-lo)*float64(lay.bar)))
		}
		width = min(width, lay.bar-at)
		role := accentOr(b, d.Rows[i], RoleAccent)
		if f.Focused && i == f.Cursor {
			role = RoleAccent
		}
		lines = append(lines,
			f.Paint.Paint(RoleMuted, pad(f.Paint.Truncate(labels[i], lay.label), lay.label, f.Paint))+" "+
				strings.Repeat(" ", at)+f.Paint.Paint(role, strings.Repeat("█", width))+
				strings.Repeat(" ", lay.bar-at-width)+" "+
				f.Paint.Paint(RoleFaint, pad(notes[i], lay.note, f.Paint)))
	}
	return lines, nil
}

func (ganttWidget) CursorLine(b Block, d Data, f Frame) int { return chartCursor(b, d, f) }

func (ganttWidget) Describe() Description {
	return Description{
		What:     "one bar per row placed where it started and drawn as long as it ran",
		Needs:    []string{"columns (exactly three: label, start, then length)"},
		NotFor:   "lengths with no start, which is bar",
		Examples: []string{"systemd-analyze blame", "per-package test time", "build phases"},
	}
}
