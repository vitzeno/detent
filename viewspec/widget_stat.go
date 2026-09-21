package viewspec

import "fmt"

// stat is one number drawn large, for a pane's worth of summary. Three
// rows of box glyphs rather than a bigger font: a terminal has exactly
// one cell size, so size has to come out of the glyphs themselves.
type statWidget struct{}

var (
	_ Validator = statWidget{}
	_ Described = statWidget{}
)

func (statWidget) Validate(b Block, fields []string) error {
	if b.Title == "" {
		return fmt.Errorf("stat needs a title to label the number")
	}
	if b.CountWhere == "" {
		return checkShared(b, fields)
	}
	m, err := parseMatch(b.CountWhere)
	if err != nil {
		return err
	}
	if !m.all {
		if err := needField(m.field, fields); err != nil {
			return err
		}
	}
	return checkShared(b, fields)
}

func (statWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	value := fmt.Sprintf("%d", len(d.Rows))
	if b.CountWhere != "" {
		hit, err := parseMatch(b.CountWhere)
		if err != nil {
			return nil, err
		}
		value = fmt.Sprintf("%d", hit.count(d.Rows))
		if b.Of != "" {
			of, err := parseMatch(b.Of)
			if err != nil {
				return nil, err
			}
			value += "/" + fmt.Sprintf("%d", of.count(d.Rows))
		}
	}
	label := f.Paint.Paint(RoleFaint, f.Paint.Truncate(b.Title, f.Width))
	big, ok := bigNumber(value)
	// Paint.Width, not len: a box glyph is three bytes and one cell.
	if !ok || f.Paint.Width(big[0]) > f.Width {
		return []string{label + " " + f.Paint.Paint(RoleAccent, value)}, nil
	}
	out := make([]string, 0, 4)
	for _, line := range big {
		out = append(out, f.Paint.Paint(RoleAccent, line))
	}
	return append(out, label), nil
}

// bigDigits is a three-row box-drawing face. Only the glyphs a counted
// number can contain, since nothing else reaches it.
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

// What each panel is for, and the one it is most likely confused with.

func (statWidget) Describe() Description {
	return Description{
		What:     "one counted number drawn large with a label, for the headline figure of a pane",
		Needs:    []string{"title", "count_where as field=value when counting a subset, else omit it", "of"},
		NotFor:   "a proportion you want drawn as a bar, which is meter",
		Examples: []string{"how many containers are running", "how many files changed"},
	}
}
