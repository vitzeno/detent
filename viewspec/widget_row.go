package viewspec

import (
	"errors"
	"fmt"
	"strings"
)

// rowWidget lays its panes side by side. It is a Container because its
// blocks must be resolved against the registry, which a Widget never sees.
type rowWidget struct{}

var (
	_ Container = rowWidget{}
	_ Described = rowWidget{}
)

func (rowWidget) Draw(Block, Data, Frame) ([]string, error) {
	return nil, fmt.Errorf("a row is arranged by the interpreter, not drawn")
}

func (rowWidget) Accept(panes []Pane) error {
	if len(panes) < 2 {
		return errors.New("a row needs at least two panes")
	}
	return nil
}

func (rowWidget) Widths(panes []Pane, total int) ([]int, error) {
	return paneWidths(panes, total), nil
}

// Arrange joins the panes horizontally, which keeps line indexes, so
// every pane starts on line zero and a pane's cursor line is the row's.
func (rowWidget) Arrange(cols [][]string, widths []int, _ Block, f Frame) ([]string, []int) {
	height := 0
	for _, col := range cols {
		height = max(height, len(col))
	}
	lines := make([]string, height)
	for row := range height {
		var line strings.Builder
		for i, col := range cols {
			if i > 0 {
				line.WriteString(" ")
			}
			cell := ""
			if row < len(col) {
				cell = col[row]
			}
			line.WriteString(pad(f.Paint.Truncate(cell, widths[i]), widths[i], f.Paint))
		}
		lines[row] = strings.TrimRight(line.String(), " ")
	}
	return lines, make([]int, len(cols))
}

func (rowWidget) Describe() Description {
	return Description{
		What: "lays its panes side by side, for putting a summary next to the thing it summarises",
		// No Slots: this needs panes, which hold blocks,
		// so nothing can compose one from field choices alone.
		NotFor:   "blocks that simply follow one another, which stack without a row",
		Examples: []string{"a meter beside the table it counts", "a chart beside its legend"},
	}
}

// paneWidths shares the row across its panes by weight, with a floor
// so a pane never vanishes, and one space of gutter between them.
func paneWidths(panes []Pane, total int) []int {
	const floor = 6
	n := len(panes)
	avail := total - (n - 1)
	sum := 0
	for _, p := range panes {
		sum += max(1, p.Weight)
	}
	out := make([]int, n)
	used := 0
	for i, p := range panes {
		out[i] = max(floor, avail*max(1, p.Weight)/sum)
		used += out[i]
	}
	if used < avail {
		out[0] += avail - used
		used = avail
	}
	// Stops once every pane is at its floor, and Draw clips the rest.
	for shrunk := true; used > avail && shrunk; {
		shrunk = false
		for i := 0; i < n && used > avail; i++ {
			if out[i] > floor {
				out[i]--
				used--
				shrunk = true
			}
		}
	}
	return out
}
