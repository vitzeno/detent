package viewspec

import (
	"fmt"
	"maps"
	"slices"
)

// Registry is the vocabulary a spec is compiled against, and the
// extension point for a consumer's own widgets and parse kinds.
type Registry struct {
	widgets    map[string]Widget
	extractors map[string]func(Parse) (Extractor, error)
}

// NewRegistry returns an empty registry, for a caller that wants only
// its own vocabulary.
func NewRegistry() *Registry {
	return &Registry{
		widgets:    map[string]Widget{},
		extractors: map[string]func(Parse) (Extractor, error){},
	}
}

// Widget draws one block kind. An error drops the whole view, never
// just this block, since one silently empty column is a lie with a border.
type Widget interface {
	Draw(b Block, d Data, f Frame) ([]string, error)
}

// Widget registers a widget under kind, replacing any built-in of the
// same name, so a consumer can swap in its own without forking.
func (r *Registry) Widget(kind string, w Widget) error {
	if kind == "" {
		return fmt.Errorf("viewspec: widget kind must not be empty")
	}
	if w == nil {
		return fmt.Errorf("viewspec: widget %q must not be nil", kind)
	}
	r.widgets[kind] = w
	return nil
}

// Extractor turns captured bytes into rows. It never sees a Block: a
// parse describes the output, not the view drawn from it.
type Extractor interface {
	Extract(output string) ([]Row, error)
}

// Extractor registers a parse kind. mk is called once per Compile so
// an extractor can do its own setup, such as compiling a pattern.
func (r *Registry) Extractor(kind string, mk func(Parse) (Extractor, error)) error {
	if kind == "" {
		return fmt.Errorf("viewspec: parse kind must not be empty")
	}
	if mk == nil {
		return fmt.Errorf("viewspec: parse kind %q must not be nil", kind)
	}
	r.extractors[kind] = mk
	return nil
}

// Subset returns a registry holding only the named widget kinds, with
// every parse kind intact. Unknown names are ignored.
func (r *Registry) Subset(kinds ...string) *Registry {
	out := NewRegistry()
	maps.Copy(out.extractors, r.extractors)
	for _, kind := range kinds {
		if w, ok := r.widgets[kind]; ok {
			out.widgets[kind] = w
		}
	}
	return out
}

// Kinds lists every registered block kind, sorted.
func (r *Registry) Kinds() []string { return slices.Sorted(maps.Keys(r.widgets)) }

// ParseKinds lists every registered parse kind, sorted.
func (r *Registry) ParseKinds() []string { return slices.Sorted(maps.Keys(r.extractors)) }

// Selects reports whether kind draws one row per line, the kinds a
// cursor moves through and on_enter can act on.
func (r *Registry) Selects(kind string) bool {
	w, ok := r.widgets[kind]
	if !ok {
		return false
	}
	_, ok = w.(Selector)
	return ok
}

// Describe returns what a registered widget says about itself, or false
// for one with no Describe, which stays drawable but is never offered.
func (r *Registry) Describe(kind string) (Description, bool) {
	w, ok := r.widgets[kind]
	if !ok {
		return Description{}, false
	}
	d, ok := w.(Described)
	if !ok {
		return Description{}, false
	}
	return d.Describe(), true
}

// WidgetFunc adapts a plain function, as http.HandlerFunc does.
type WidgetFunc func(Block, Data, Frame) ([]string, error)

func (fn WidgetFunc) Draw(b Block, d Data, f Frame) ([]string, error) { return fn(b, d, f) }

// ExtractorFunc adapts a plain function.
type ExtractorFunc func(string) ([]Row, error)

func (fn ExtractorFunc) Extract(output string) ([]Row, error) { return fn(output) }

// Validator is an optional Widget extension. Bind calls it with the
// fields the parse produced, so a block is rejected before any drawing.
type Validator interface {
	Validate(b Block, fields []string) error
}

// Selector is an optional Widget extension reporting which of its own
// lines the cursor sits on, so a caller can scroll to the selection.
type Selector interface {
	CursorLine(b Block, d Data, f Frame) int
}

// Container is an optional Widget extension: a kind that arranges
// other blocks, which the interpreter resolves and draws for it.
type Container interface {
	// Accept reports whether this is a shape the kind can arrange.
	Accept(panes []Pane) error
	// Widths is how wide each pane should be drawn.
	Widths(panes []Pane, total int) ([]int, error)
	// Arrange assembles the drawn panes and reports where each one's
	// first line landed, which for a stacking layout depends on heights.
	Arrange(cols [][]string, widths []int, b Block, f Frame) (lines []string, paneAt []int)
}

// Described is an optional Widget extension carrying that description
// into Registry.Schema.
type Described interface {
	Describe() Description
}

// Description is what a widget is for, structured with a counter-case,
// since flat one-liners lose a model's calibration over many options.
type Description struct {
	What   string `json:"what"`
	NotFor string `json:"not_for,omitempty"`
	// Needs are the fields it cannot draw without: one fills Block.Field,
	// more fill Block.Columns in order, and a non-field need declares none.
	Needs []Slot `json:"needs,omitempty"`
	// Summarises marks a kind drawing one fact about every row, not the rows.
	Summarises bool `json:"summarises,omitempty"`
	// Raw marks a kind drawing the output as it came, reading no rows.
	Raw      bool     `json:"raw,omitempty"`
	Examples []string `json:"examples,omitempty"`
}

// Slot is one field a widget cannot be drawn without, named for the
// part it plays so a caller can ask for it in those terms.
type Slot struct {
	Name string `json:"name"`
	// What it holds, phrased as the answer to "which field".
	What string `json:"what"`
}

// ColumnOrder is an optional Extractor extension that also returns the
// fields' order and spelling. Without it they sort alphabetically.
type ColumnOrder interface {
	ExtractColumns(output string) ([]Row, []Column, error)
}

func (r *Registry) widget(kind string) (Widget, bool) {
	w, ok := r.widgets[kind]
	return w, ok
}

// isContainer asks the registered widget rather than the kind's name,
// so a consumer can register a layout of its own.
func (r *Registry) isContainer(kind string) bool {
	w, ok := r.widgets[kind]
	if !ok {
		return false
	}
	_, ok = w.(Container)
	return ok
}

func (r *Registry) extractor(p Parse) (Extractor, error) {
	mk, ok := r.extractors[p.Kind]
	if !ok {
		return nil, fmt.Errorf("viewspec: unknown parse kind %q", p.Kind)
	}
	return mk(p)
}

// clone lets Standard hand out a fresh registry per call, so one
// caller's registration can't leak into another's.
func (r *Registry) clone() *Registry {
	out := NewRegistry()
	maps.Copy(out.widgets, r.widgets)
	maps.Copy(out.extractors, r.extractors)
	return out
}

// describe returns every registered widget's description, keyed by
// kind. A widget that says nothing is absent.
func (r *Registry) describe() map[string]Description {
	out := make(map[string]Description, len(r.widgets))
	for kind, w := range r.widgets {
		if d, ok := w.(Described); ok {
			out[kind] = d.Describe()
		}
	}
	return out
}
