package viewspec

import "fmt"

// rowWidget exists so a row is in Kinds, Schema and the widget guide
// like anything else. Draw is unreachable: the interpreter lays panes
// out itself, because a Widget never sees the registry.
type rowWidget struct{}

func (rowWidget) Draw(Block, Data, Frame) ([]string, error) {
	return nil, fmt.Errorf("a row is laid out by the interpreter, not drawn")
}

func (rowWidget) Describe() Description {
	return Description{
		What:     "lays its panes side by side, for putting a summary next to the thing it summarises",
		Needs:    []string{"panes (at least two)"},
		NotFor:   "blocks that simply follow one another — those stack without a row",
		Examples: []string{"a meter beside the table it counts", "a chart beside its legend"},
	}
}
