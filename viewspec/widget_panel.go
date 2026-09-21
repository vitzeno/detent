package viewspec

import "fmt"

// panelWidget is in Kinds and the guide like anything else; the
// interpreter frames it, for the reason rowWidget states.
type panelWidget struct{}

func (panelWidget) Draw(Block, Data, Frame) ([]string, error) {
	return nil, fmt.Errorf("a panel is framed by the interpreter, not drawn")
}

func (panelWidget) Describe() Description {
	return Description{
		What:     "frames one pane of blocks in a border, with its title written into the top edge",
		Needs:    []string{"panes (exactly one)", "title"},
		NotFor:   "putting two things side by side, which is row",
		Examples: []string{"a summary set apart from the listing beneath it"},
	}
}
