package viewspec

import (
	"fmt"
	"maps"
	"slices"
)

// Widget draws one block kind. Returning an error drops the whole
// view, never just this block — a table rendering three real columns
// beside one silently empty one is a lie with a border around it.
type Widget interface {
	Draw(b Block, d Data, f Frame) ([]string, error)
}

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

// Description is what a widget is for, stated the way a many-way
// choice needs: structured, with a counter-case. Flat one-liners lose
// a model's calibration once there are more than a handful of options.
type Description struct {
	What     string   `json:"what"`
	NotFor   string   `json:"not_for,omitempty"`
	Examples []string `json:"examples,omitempty"`
}

// Described is an optional Widget extension carrying that description
// into Registry.Schema, so the model choosing a widget reads the same
// criteria the author wrote.
type Described interface {
	Describe() Description
}

// Extractor turns captured bytes into rows. It never sees a Block: a
// parse describes the output, not the view drawn from it.
type Extractor interface {
	Extract(output string) ([]Row, error)
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

// Widget registers a widget under kind, replacing any built-in of the
// same name — which is how a consumer swaps in a richer table without
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

// Kinds lists every registered block kind, sorted.
func (r *Registry) Kinds() []string { return slices.Sorted(maps.Keys(r.widgets)) }

// ParseKinds lists every registered parse kind, sorted.
func (r *Registry) ParseKinds() []string { return slices.Sorted(maps.Keys(r.extractors)) }

func (r *Registry) widget(kind string) (Widget, bool) {
	w, ok := r.widgets[kind]
	return w, ok
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
