package viewspec

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// badges summarises one field as its distinct values with counts.
type badgesWidget struct{}

var (
	_ Validator = badgesWidget{}
	_ Described = badgesWidget{}
)

func (badgesWidget) Validate(b Block, fields []string) error {
	if len(fields) == 0 {
		return ErrNoRows
	}
	return needField(b.Field, fields)
}

func (badgesWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	counts := map[string]int{}
	for _, r := range d.Rows {
		counts[r[b.Field]]++
	}
	keys := slices.Sorted(maps.Keys(counts))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		role := RoleMuted
		if b.Accent != nil {
			if got, ok := b.Accent.Map[k]; ok {
				role = got
			}
		}
		parts = append(parts, f.Paint.Paint(role, fmt.Sprintf("%s %d", k, counts[k])))
	}
	return []string{strings.Join(parts, "  ")}, nil
}

func (badgesWidget) Describe() Description {
	return Description{
		Summarises: true,
		What:       "each distinct value of one field with how many rows have it",
		Needs: []Slot{
			{Name: "field", What: "the value to count distinct values of"},
		},
		NotFor:   "showing the rows themselves — this only summarises them",
		Examples: []string{"git status codes", "container states"},
	}
}
