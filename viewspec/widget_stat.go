package viewspec

import (
	"fmt"
	"strconv"
)

// stat is one number drawn large in three rows of box glyphs, since a
// terminal has one cell size and size has to come from the glyphs.
type statWidget struct{}

var (
	_ Validator = statWidget{}
	_ Described = statWidget{}
)

func (statWidget) Validate(b Block, fields []string) error {
	if b.Title == "" {
		return fmt.Errorf("stat needs a title to label the number")
	}
	if b.CountWhere != "" {
		if err := checkCount(b, fields); err != nil {
			return err
		}
	}
	return checkShared(b, fields)
}

func (statWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	value := strconv.Itoa(len(d.Rows))
	if b.CountWhere != "" {
		hit, err := parseMatch(b.CountWhere)
		if err != nil {
			return nil, err
		}
		of, err := parseMatch(b.Of)
		if err != nil {
			return nil, err
		}
		n, total := countOf(hit, of, d.Rows)
		value = strconv.Itoa(n)
		if b.Of != "" {
			value += "/" + strconv.Itoa(total)
		}
	}
	label := f.Paint.Paint(RoleFaint, f.Paint.Truncate(b.Title, f.Width))
	big, ok := bigNumber(value)
	// Paint.Width, not len: a box glyph is three bytes and one cell.
	if !ok || f.Paint.Width(big[0]) > f.Width {
		// The number keeps its room and the label takes what is left.
		short := f.Paint.Truncate(b.Title, f.Width-f.Paint.Width(value)-1)
		if short == "" {
			return []string{f.Paint.Paint(RoleAccent, value)}, nil
		}
		return []string{f.Paint.Paint(RoleFaint, short) + " " + f.Paint.Paint(RoleAccent, value)}, nil
	}
	out := make([]string, 0, 4)
	for _, line := range big {
		out = append(out, f.Paint.Paint(RoleAccent, line))
	}
	return append(out, label), nil
}

func (statWidget) Describe() Description {
	return Description{
		Summarises: true,
		What:       "one counted number drawn large with a label, for the headline figure of a pane",
		// No Slots: this needs a title, which is prose,
		// so nothing can compose one from field choices alone.
		NotFor:   "a proportion you want drawn as a bar, which is meter",
		Examples: []string{"how many containers are running", "how many files changed"},
	}
}

// bigDigits is a three-row box-drawing face for the glyphs a counted
// number can contain.
var bigDigits = map[rune][3]string{
	'0': {"┌─┐", "│ │", "└─┘"},
	'1': {" ┐ ", " │ ", " ╵ "},
	'2': {"┌─┐", "┌─┘", "└─╴"},
	'3': {"┌─┐", "╶─┤", "└─┘"},
	'4': {"╷ ╷", "└─┤", "  ╵"},
	'5': {"┌─╴", "└─┐", "└─┘"},
	'6': {"┌─╴", "├─┐", "└─┘"},
	'7': {"┌─┐", "  │", "  ╵"},
	'8': {"┌─┐", "├─┤", "└─┘"},
	'9': {"┌─┐", "└─┤", "└─┘"},
	'/': {"  ╱", " ╱ ", "╱  "},
	'%': {"▪ ╱", " ╱ ", "╱ ▪"},
	'.': {"   ", "   ", " ▪ "},
	'-': {"   ", "╶─╴", "   "},
}

// bigNumber renders s three rows tall, or reports that it holds a
// glyph the face has no shape for.
func bigNumber(s string) ([3]string, bool) {
	var out [3]string
	for i, c := range s {
		glyph, ok := bigDigits[c]
		if !ok {
			return out, false
		}
		for row := range out {
			if i > 0 {
				out[row] += " "
			}
			out[row] += glyph[row]
		}
	}
	return out, true
}
