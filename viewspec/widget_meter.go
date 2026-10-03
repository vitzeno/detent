package viewspec

import (
	"errors"
	"fmt"
)

// meterFloor keeps a meter legible in a narrow pane rather than
// letting the label squeeze the bar out of existence.
const meterFloor = 8

// meter draws a proportion counted from rows, so a Title containing a
// number cannot change what the bar says.
type meterWidget struct{}

var (
	_ Validator = meterWidget{}
	_ Described = meterWidget{}
)

func (meterWidget) Describe() Description {
	return Description{
		Summarises: true,
		What:       "one proportion counted from the rows, as a bar and a fraction",
		// No Slots: this needs a count_where filter and an of denominator, which are values rather than fields,
		// so nothing can compose one from field choices alone.
		NotFor:   "a value per row, which is bar",
		Examples: []string{"how many tests passed", "how many files are staged"},
	}
}

func (meterWidget) Validate(b Block, fields []string) error {
	if b.CountWhere == "" {
		return errors.New("meter needs count_where")
	}
	if len(fields) == 0 {
		return ErrNoRows
	}
	return checkCount(b, fields)
}

func (meterWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	hit, _ := parseMatch(b.CountWhere)
	of, err := parseMatch(b.Of)
	if err != nil {
		return nil, err
	}
	n, total := countOf(hit, of, d.Rows)
	label := b.Title
	if label == "" {
		label = b.CountWhere
	}
	note := fmt.Sprintf(" %d/%d", n, total)
	// The bar takes whatever the label and the count leave, so a meter
	// in a wide pane reads as a bar rather than as a stub in a corner.
	cells := max(f.Width-f.Paint.Width(label)-f.Paint.Width(note)-1, meterFloor)
	filled := 0
	if total > 0 {
		filled = n * cells / total
	}
	role := RoleSafe
	if n < total {
		role = RoleCaution
	}
	return []string{f.Paint.Paint(RoleFaint, label+" ") +
		f.Paint.Paint(role, repeat("█", filled)) +
		f.Paint.Paint(RoleFaint, repeat("░", cells-filled)) +
		f.Paint.Paint(RoleDefault, note)}, nil
}
