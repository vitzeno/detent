package layout

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name    string
		total   int
		weights []int
		min     int
		want    []int
	}{
		{"even split, no remainder", 100, []int{1, 1}, 0, []int{50, 50}},
		{"3:2 weighting, no remainder", 100, []int{3, 2}, 0, []int{60, 40}},
		{"remainder goes to earliest shares", 101, []int{3, 2}, 0, []int{61, 40}},
		{"three-way remainder distributes one each", 10, []int{1, 1, 1}, 0, []int{4, 3, 3}},
		{"min floors a share below its weighted size", 20, []int{1, 9}, 5, []int{5, 15}},
		{"min can make the sum exceed total", 5, []int{1, 1}, 10, []int{10, 10}},
		{"zero weights fall back to an even split", 10, []int{0, 0}, 0, []int{5, 5}},
		{"single weight takes everything", 42, []int{1}, 0, []int{42}},
		{"no weights returns nil", 42, nil, 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Split(tt.total, tt.weights, tt.min)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSplit_SumsToTotalWhenUnconstrained(t *testing.T) {
	for _, total := range []int{0, 1, 7, 13, 100, 137} {
		for _, weights := range [][]int{{1, 1}, {3, 2}, {1, 1, 1}, {5, 3, 2}} {
			got := Split(total, weights, 0)
			sum := 0
			for _, v := range got {
				sum += v
			}
			assert.Equal(t, total, sum, "total=%d weights=%v", total, weights)
		}
	}
}

func TestRow_JoinsLeftToRight(t *testing.T) {
	// Same visual row: "a"/"c" and "b"/"d" must each share a line, not stack.
	assert.Equal(t, "ac\nbd", Row("a\nb", "c\nd"))
}
