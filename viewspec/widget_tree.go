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
	depths := treeDepths(b, d.Rows)
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
		stem := f.Paint.Paint(RoleFaint, treeStem(depths, i))
		lines = append(lines, stem+f.Paint.Paint(role,
			f.Paint.Truncate(label, max(1, f.Width-2*depths[i]-2))))
	}
	return lines, nil
}

func (treeWidget) CursorLine(_ Block, d Data, f Frame) int { return rowCursor(d, f) }

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

func treeDepths(b Block, rows []Row) []int {
	out := make([]int, len(rows))
	for i, r := range rows {
		if b.Depth != "" {
			n, _ := strconv.Atoi(r[b.Depth])
			out[i] = max(0, n)
			continue
		}
		out[i] = strings.Count(strings.Trim(r[b.Field], "/"), "/")
	}
	return out
}

// treeStem is row i's connector: a branch for its own level, and for
// each ancestor a bar only where that ancestor still has rows to come.
func treeStem(depths []int, i int) string {
	d := depths[i]
	if d == 0 {
		return ""
	}
	var b strings.Builder
	for level := 1; level < d; level++ {
		if hasLaterSibling(depths, i, level) {
			b.WriteString("│  ")
			continue
		}
		b.WriteString("   ")
	}
	if hasLaterSibling(depths, i, d) {
		return b.String() + "├─ "
	}
	return b.String() + "└─ "
}

// hasLaterSibling reports whether another row at level appears before
// the tree returns to something shallower.
func hasLaterSibling(depths []int, i, level int) bool {
	for j := i + 1; j < len(depths); j++ {
		if depths[j] < level {
			return false
		}
		if depths[j] == level {
			return true
		}
	}
	return false
}

func leaf(path string) string {
	trimmed := strings.Trim(path, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}
