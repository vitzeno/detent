package viewspec

import (
	"fmt"
	"strings"
)

// stack draws one line of proportional segments, for composition
// rather than comparison, and a legend naming them.
type stackWidget struct{}

var (
	_ Validator = stackWidget{}
	_ Described = stackWidget{}
)

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

func (stackWidget) Describe() Description {
	return Description{
		What: "one band split into proportional segments with a legend, for what a whole is made of",
		Needs: []Slot{
			{Name: "label", What: "names each segment"},
			{Name: "value", What: "how much of the whole it is"},
		},
		NotFor:   "comparing rows against each other, which is bar",
		Examples: []string{"disk used by directory", "lines by language", "time by phase"},
	}
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
