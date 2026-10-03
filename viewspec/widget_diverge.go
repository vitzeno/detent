package viewspec

import (
	"errors"
	"strings"
)

// diverge charts two numbers per row either side of a centre line, the
// shape a diffstat already has.
type divergeWidget struct{}

var (
	_ Validator = divergeWidget{}
	_ Selector  = divergeWidget{}
	_ Described = divergeWidget{}
)

func (divergeWidget) Describe() Description {
	return Description{
		What: "two numbers per row drawn either side of a centre line, for a pair that opposes",
		Needs: []Slot{
			{Name: "label", What: "names each row"},
			{Name: "left", What: "the number growing leftward"},
			{Name: "right", What: "the number growing rightward"},
		},
		NotFor:   "a single number per row, which is bar",
		Examples: []string{"git diff --numstat added against removed", "passed against failed"},
	}
}

func (divergeWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 3 {
		return errors.New("diverge needs a label column and two value columns")
	}
	return checkColumns(b, fields)
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
		return nil, errNoWidth
	}
	lines := titleLine(b, f)
	for _, r := range d.Rows {
		l, rt := 0, 0
		if hi > 0 {
			l = min(max(int(number(r[left])/hi*float64(wing)), 0), wing)
			rt = min(max(int(number(r[right])/hi*float64(wing)), 0), wing)
		}
		lines = append(lines,
			labelCell(r[label], labelW, f)+" "+
				strings.Repeat(" ", wing-l)+f.Paint.Paint(RoleDanger, strings.Repeat("█", l))+
				f.Paint.Paint(RoleFaint, "│")+
				f.Paint.Paint(RoleSafe, strings.Repeat("█", rt))+strings.Repeat(" ", wing-rt)+" "+
				f.Paint.Paint(RoleFaint, r[left]+" "+r[right]))
	}
	return lines, nil
}

func (divergeWidget) CursorLine(b Block, d Data, f Frame) int { return chartCursor(b, d, f) }
