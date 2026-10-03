package viewspec

import (
	"errors"
	"strings"
)

// panelWidget frames its one pane in a border. A Container for the
// reason rowWidget states.
type panelWidget struct{}

var (
	_ Container = panelWidget{}
	_ Described = panelWidget{}
)

func (panelWidget) Describe() Description {
	return Description{
		What: "frames one pane of blocks in a border, with its title written into the top edge",
		// No Slots: this needs panes, which hold blocks,
		// so nothing can compose one from field choices alone.
		NotFor:   "putting two things side by side, which is row",
		Examples: []string{"a summary set apart from the listing beneath it"},
	}
}

func (panelWidget) Draw(Block, Data, Frame) ([]string, error) {
	return nil, errors.New("a panel is arranged by the interpreter, not drawn")
}

func (panelWidget) Accept(panes []Pane) error {
	if len(panes) != 1 {
		return errors.New("a panel frames exactly one pane")
	}
	return nil
}

// Widths keeps the border out of the pane: four columns for two edges
// and their padding, so the inside is drawn narrower rather than clipped.
func (panelWidget) Widths(_ []Pane, total int) ([]int, error) {
	if total-4 < 1 {
		return nil, errors.New("no width left to frame")
	}
	return []int{total - 4}, nil
}

// Arrange wraps the pane, which starts one line down past the edge.
func (panelWidget) Arrange(cols [][]string, widths []int, b Block, f Frame) ([]string, []int) {
	lines := []string{f.Paint.Paint(RoleFaint, panelTop(b.Title, f))}
	for _, l := range cols[0] {
		gap := strings.Repeat(" ", max(widths[0]-f.Paint.Width(l), 0))
		lines = append(lines, f.Paint.Paint(RoleFaint, "│ ")+l+gap+f.Paint.Paint(RoleFaint, " │"))
	}
	return append(lines,
		f.Paint.Paint(RoleFaint, "╰"+strings.Repeat("─", f.Width-2)+"╯")), []int{1}
}

// panelTop writes the title into the top edge, the way a fieldset does.
func panelTop(title string, f Frame) string {
	if title == "" {
		return "╭" + strings.Repeat("─", f.Width-2) + "╮"
	}
	head := "╭─ " + f.Paint.Truncate(title, max(f.Width-6, 1)) + " "
	return head + strings.Repeat("─", max(f.Width-f.Paint.Width(head)-1, 0)) + "╮"
}
