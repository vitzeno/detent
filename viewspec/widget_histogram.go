package viewspec

import (
	"maps"
	"slices"
	"strconv"
)

// histogram groups rows by a field and charts how many landed in each.
// A spec carries no data, so counting has to happen here.
type histogramWidget struct{}

var (
	_ Validator = histogramWidget{}
	_ Described = histogramWidget{}
)

func (histogramWidget) Validate(b Block, fields []string) error {
	if len(fields) == 0 {
		return ErrNoRows
	}
	if err := needField(b.Field, fields); err != nil {
		return err
	}
	return checkShared(b, fields)
}

func (histogramWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	counts := map[string]int{}
	for _, r := range d.Rows {
		counts[r[b.Field]]++
	}
	keys := slices.Sorted(maps.Keys(counts))
	// Biggest first, so the shape reads without counting cells.
	slices.SortStableFunc(keys, func(x, y string) int { return counts[y] - counts[x] })

	notes := make([]string, len(keys))
	hi := 0
	for i, k := range keys {
		notes[i] = strconv.Itoa(counts[k])
		hi = max(hi, counts[k])
	}
	lay, err := layOutBars(keys, notes, f)
	if err != nil {
		return nil, err
	}
	lines := titleLine(b, f)
	for i, k := range keys {
		n := 0
		if hi > 0 {
			n = counts[k] * lay.bar / hi
		}
		lines = append(lines, lay.row(k, n, RoleAccent, notes[i], f))
	}
	return lines, nil
}

func (histogramWidget) Describe() Description {
	return Description{
		What: "groups rows by a field and charts how many fell in each, counting them for you",
		Needs: []Slot{
			{Name: "field", What: "the value to group and count rows by"},
		},
		NotFor:   "a number the rows already carry, which is bar",
		Examples: []string{"commits per author", "processes per user", "responses per status code"},
	}
}
