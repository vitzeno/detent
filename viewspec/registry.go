package viewspec

import (
	"fmt"
	"maps"
	"slices"
)

// Registry is the vocabulary a spec is compiled against, and the
// extension point: a consumer registers its own widgets and parse
// kinds rather than forking the package.
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

// Widget draws one block kind. Returning an error drops the whole
// view, never just this block. A table rendering three real columns
// beside one silently empty one is a lie with a border around it.
type Widget interface {
	Draw(b Block, d Data, f Frame) ([]string, error)
}

// Widget registers a widget under kind, replacing any built-in of the
// same name. That is how a consumer swaps in a richer table without
// this package learning what its table library is.
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
// every parse kind intact. Narrowing what a model may choose from is
// what keeps a many-way choice calibrated; compiling against the same
// subset is what stops it choosing outside the set anyway. Unknown
// names are ignored, so a caller can name kinds it isn't sure exist.
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

// WidgetFunc adapts a plain function, as http.HandlerFunc does.
type WidgetFunc func(Block, Data, Frame) ([]string, error)

func (fn WidgetFunc) Draw(b Block, d Data, f Frame) ([]string, error) { return fn(b, d, f) }

// Validator is an optional Widget extension. Bind calls it with the
// fields the parse actually produced, so a widget can reject a block
// before any frame is drawn rather than failing mid-render.
type Validator interface {
	Validate(b Block, fields []string) error
}

// Selector is an optional Widget extension: a widget drawing a cursor
// reports which of its own lines the cursor sits on, so a caller can
// scroll to a selection it cannot see the layout of.
type Selector interface {
	CursorLine(b Block, d Data, f Frame) int
}

// Container is an optional Widget extension: a kind that arranges
// other blocks instead of drawing data. The interpreter resolves and
// draws the children, because a Widget never sees the registry, then
// hands them back here to be put together.
type Container interface {
	// Accept reports whether this is a shape the kind can arrange.
	Accept(panes []Pane) error
	// Widths is how wide each pane should be drawn.
	Widths(panes []Pane, total int) ([]int, error)
	// Arrange assembles the drawn panes and reports where each one's
	// first line landed. A single offset would do for a row or a
	// panel and not for a layout that stacks, where how far a pane
	// moved depends on how tall the panes above it came out.
	Arrange(cols [][]string, widths []int, b Block, f Frame) (lines []string, paneAt []int)
}

// Description is what a widget is for, stated the way a many-way
// choice needs: structured, with a counter-case. Flat one-liners lose
// a model's calibration once there are more than a handful of options.
type Description struct {
	What   string `json:"what"`
	NotFor string `json:"not_for,omitempty"`
	// Needs are the fields this kind cannot draw without, in the order
	// it reads them. One slot fills Block.Field, two or more fill
	// Block.Columns in order, and a kind needing something that is not
	// a field declares none. Structured rather than prose because
	// whatever composes a block has to ask for each one, and a
	// sentence has to be kept in step with that by hand.
	Needs []Slot `json:"needs,omitempty"`
	// Summarises marks a kind drawing one fact about every row rather
	// than the rows themselves. A caller asks for a body and a summary
	// separately, and which a widget is belongs beside the widget.
	Summarises bool `json:"summarises,omitempty"`
	// Raw marks a kind drawing the output as it came, reading no rows,
	// so a view holding one hides nothing whatever its parse read.
	Raw      bool     `json:"raw,omitempty"`
	Examples []string `json:"examples,omitempty"`
}

// Slot is one field a widget cannot be drawn without, named for the
// part it plays so a caller can ask for it in those terms.
type Slot struct {
	Name string `json:"name"`
	// What it should hold, phrased as the answer to "which field".
	What string `json:"what"`
}

// Described is an optional Widget extension carrying that description
// into Registry.Schema, so the model choosing a widget reads the same
// criteria the author wrote.
type Described interface {
	Describe() Description
}

// ColumnOrder is an optional Extractor extension reporting the order
// fields appeared in and how the output spelled them. Without it they
// sort alphabetically and display by key.
type ColumnOrder interface {
	Columns() []Column
}

// ExtractorFunc adapts a plain function.
type ExtractorFunc func(string) ([]Row, error)

func (fn ExtractorFunc) Extract(output string) ([]Row, error) { return fn(output) }

func (r *Registry) widget(kind string) (Widget, bool) {
	w, ok := r.widgets[kind]
	return w, ok
}

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

// Describe returns what a registered widget says about itself. A
// widget with no Describe reports false: it is drawable and invisible
// to anything choosing between kinds, which is how a plain WidgetFunc
// opts out.
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

// describe returns every registered widget's description, keyed by
// kind. A widget that says nothing is simply absent, which is how a
// consumer-registered widget opts out.
func (r *Registry) describe() map[string]Description {
	out := make(map[string]Description, len(r.widgets))
	for kind, w := range r.widgets {
		if d, ok := w.(Described); ok {
			out[kind] = d.Describe()
		}
	}
	return out
}
