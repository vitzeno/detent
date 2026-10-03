package viewspec

import (
	"strconv"
	"strings"
)

// treeWidget draws a hierarchy. Depth names each row's level field,
// and without one Field is read as a path split on slashes.
type treeWidget struct{}

var (
	_ Validator = treeWidget{}
	_ Selector  = treeWidget{}
	_ Described = treeWidget{}
)

func (treeWidget) Describe() Description {
	return Description{
		What: "a hierarchy, from a depth field or from a path's slashes",
		Needs: []Slot{
			{Name: "field", What: "the path or name at each node"},
		},
		NotFor:   "a flat set of names with no nesting, which is a list",
		Examples: []string{"tree", "find . -name '*.go'", "an indented outline"},
	}
}

func (treeWidget) Validate(b Block, fields []string) error {
	if len(fields) == 0 {
		return ErrNoRows
	}
	if err := needField(b.Field, fields); err != nil {
		return err
	}
	if b.Depth != "" {
		if err := needField(b.Depth, fields); err != nil {
			return err
		}
	}
	return checkShared(b, fields)
}

func (treeWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	depths := treeDepths(b, d.Rows, f)
	stems := treeStems(depths)
	lines := make([]string, 0, len(d.Rows))
	for i, r := range d.Rows {
		role := accentRole(b, r)
		if f.Focused && i == f.Cursor {
			role = RoleAccent
		}
		label := r[b.Field]
		if b.Depth == "" {
			label = leaf(label)
		}
		lines = append(lines, f.Paint.Paint(RoleFaint, stems[i])+f.Paint.Paint(role,
			f.Paint.Truncate(label, max(1, f.Width-3*depths[i]))))
	}
	return lines, nil
}

func (treeWidget) CursorLine(_ Block, d Data, f Frame) int { return rowCursor(d, f) }

// treeDepths caps each depth at what the frame can indent, since a depth
// field comes from output and a huge one would allocate without bound.
func treeDepths(b Block, rows []Row, f Frame) []int {
	deepest := max(0, (f.Width-1)/3)
	out := make([]int, len(rows))
	for i, r := range rows {
		n := strings.Count(strings.Trim(r[b.Field], "/"), "/")
		if b.Depth != "" {
			n, _ = strconv.Atoi(r[b.Depth])
		}
		out[i] = min(max(0, n), deepest)
	}
	return out
}

// treeStems is each row's connector: a branch for its own level, and for
// each ancestor a bar only where that ancestor still has rows to come.
func treeStems(depths []int) []string {
	out := make([]string, len(depths))
	// later[level] is whether a row at level follows before something shallower does.
	var later []bool
	for i := len(depths) - 1; i >= 0; i-- {
		d := depths[i]
		if d > 0 {
			var b strings.Builder
			for level := 1; level < d; level++ {
				if level < len(later) && later[level] {
					b.WriteString("│  ")
				} else {
					b.WriteString("   ")
				}
			}
			if d < len(later) && later[d] {
				b.WriteString("├─ ")
			} else {
				b.WriteString("└─ ")
			}
			out[i] = b.String()
		}
		for len(later) <= d {
			later = append(later, false)
		}
		later[d] = true
		clear(later[d+1:])
	}
	return out
}

func leaf(path string) string {
	trimmed := strings.Trim(path, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}
