package viewspec

import "fmt"

// text is model-authored framing, so it renders faint. A generated
// heading must not be able to read as a finding.
type textWidget struct{}

var (
	_ Validator = textWidget{}
	_ Described = textWidget{}
)

func (textWidget) Validate(b Block, _ []string) error {
	if b.Title == "" {
		return fmt.Errorf("text needs a title")
	}
	return nil
}

func (textWidget) Draw(b Block, _ Data, f Frame) ([]string, error) {
	return []string{f.Paint.Paint(RoleFaint, f.Paint.Truncate(b.Title, f.Width))}, nil
}

func (textWidget) Describe() Description {
	return Description{
		What: "one short label, drawn dim because it is your prose rather than output",
		// No Slots: this needs a title, which is prose,
		// so nothing can compose one from field choices alone.
		NotFor:   "anything counted or measured: a meter computes its numbers, a title cannot",
		Examples: []string{"a heading above a table"},
	}
}
