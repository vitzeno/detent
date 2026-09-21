package viewspec

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Frame is everything Draw needs that Bind could not know. Painter is
// here, not on Compiled, so Compile and Bind stay pure data and test
// with no styling at all — see docs/design/viewspec.md.
type Frame struct {
	Width, Height int
	Focused       bool
	Cursor        int
	Paint         Painter
}

// Option configures Compile.
type Option func(*options)

type options struct{ reg *Registry }

// WithRegistry compiles against a custom vocabulary instead of Standard.
func WithRegistry(r *Registry) Option { return func(o *options) { o.reg = r } }

// BindError says which block failed to resolve and why. Callers fall
// back on any error; errors.As is here for one that wants to report
// which block it was.
type BindError struct {
	Block int
	Kind  string
	Field string
	Err   error
}

func (e *BindError) Error() string {
	at := fmt.Sprintf("block %d (%s)", e.Block, e.Kind)
	if e.Field != "" {
		at += fmt.Sprintf(" field %q", e.Field)
	}
	return fmt.Sprintf("viewspec: %s: %v", at, e.Err)
}

func (e *BindError) Unwrap() error { return e.Err }

// ErrNoRows is what a row-consuming block fails with when the parse
// produced nothing to draw.
var ErrNoRows = errors.New("parse produced no rows")

// Compiled is a validated spec. Reusable across outputs and frames,
// and cheap to keep: it holds no data and no styling.
type Compiled struct {
	spec Spec
	reg  *Registry
	ext  Extractor
}

// Spec returns what was compiled.
func (c *Compiled) Spec() Spec { return c.spec }

// Compile validates spec against the vocabulary: every block kind
// exists, the parse kind exists and its pattern compiles, and every
// static field is present. Once per spec.
func Compile(spec Spec, opts ...Option) (*Compiled, error) {
	o := options{reg: Standard()}
	for _, fn := range opts {
		fn(&o)
	}
	if spec.Version != 0 && spec.Version != Version {
		return nil, fmt.Errorf("viewspec: spec version %d, want %d", spec.Version, Version)
	}
	if len(spec.Blocks) == 0 {
		return nil, errors.New("viewspec: spec has no blocks")
	}
	ext, err := o.reg.extractor(spec.Parse)
	if err != nil {
		return nil, err
	}
	for i, b := range spec.Blocks {
		if _, ok := o.reg.widget(b.Kind); !ok {
			return nil, &BindError{Block: i, Kind: b.Kind,
				Err: fmt.Errorf("unknown block kind (have %s)", strings.Join(o.reg.Kinds(), ", "))}
		}
		if err := checkStatic(b); err != nil {
			return nil, &BindError{Block: i, Kind: b.Kind, Err: err}
		}
	}
	return &Compiled{spec: spec, reg: o.reg, ext: ext}, nil
}

// checkStatic catches what is wrong about a block without any data:
// malformed filters and half-filled sub-objects.
func checkStatic(b Block) error {
	for _, expr := range []string{b.Where, b.CountWhere} {
		if _, err := parseMatch(expr); err != nil {
			return err
		}
	}
	if b.Of != "" && b.Of != "*" {
		if _, err := parseMatch(b.Of); err != nil {
			return err
		}
	}
	if b.Sort != nil && b.Sort.Field == "" {
		return errors.New("sort needs a field")
	}
	if b.Accent != nil {
		if b.Accent.Field == "" {
			return errors.New("accent needs a field")
		}
		if len(b.Accent.Map) == 0 {
			return errors.New("accent needs a map")
		}
	}
	for _, c := range b.Columns {
		if c.Field == "" {
			return errors.New("column needs a field")
		}
		if c.Width < 0 {
			return fmt.Errorf("column %q has negative width", c.Field)
		}
	}
	return nil
}

// Bound is one output resolved against a compiled spec: parsed,
// filtered and sorted, with every binding checked. Once per output.
type Bound struct {
	c      *Compiled
	raw    string
	fields []string
	blocks []boundBlock
}

type boundBlock struct {
	block Block
	w     Widget
	data  Data
}

// Fields lists the field names the parse actually produced, sorted.
// Generation uses it to ask a closed question about real columns.
func (b *Bound) Fields() []string { return slices.Clone(b.fields) }

// Bind parses output and resolves every binding against the rows that
// actually came out. Any unresolved binding fails the whole view: a
// confidently wrong view is worse than none, because none is honest
// about not knowing.
func (c *Compiled) Bind(output string) (*Bound, error) {
	rows, err := c.ext.Extract(output)
	if err != nil {
		return nil, fmt.Errorf("viewspec: parse %q: %w", c.spec.Parse.Kind, err)
	}
	fields := fieldsOf(rows)
	out := &Bound{c: c, raw: output, fields: fields}
	for i, b := range c.spec.Blocks {
		w, _ := c.reg.widget(b.Kind)
		if v, ok := w.(Validator); ok {
			if err := v.Validate(b, fields); err != nil {
				var be *BindError
				if errors.As(err, &be) {
					be.Block, be.Kind = i, b.Kind
					return nil, be
				}
				return nil, &BindError{Block: i, Kind: b.Kind, Err: err}
			}
		}
		sel, err := selectRows(b, rows)
		if err != nil {
			return nil, &BindError{Block: i, Kind: b.Kind, Err: err}
		}
		out.blocks = append(out.blocks, boundBlock{block: b, w: w, data: Data{Rows: sel, Raw: output}})
	}
	return out, nil
}

// Draw assembles lines at a size. Called on every resize, scroll and
// focus change, so it parses nothing and validates nothing.
func (b *Bound) Draw(f Frame) ([]string, error) {
	if f.Paint == nil {
		f.Paint = Plain()
	}
	if f.Width <= 0 {
		return nil, errors.New("viewspec: frame has no width")
	}
	var lines []string
	for i, bb := range b.blocks {
		out, err := bb.w.Draw(bb.block, bb.data, f)
		if err != nil {
			return nil, &BindError{Block: i, Kind: bb.block.Kind, Err: err}
		}
		lines = append(lines, out...)
	}
	if f.Height > 0 && len(lines) > f.Height {
		lines = lines[:f.Height]
	}
	return lines, nil
}

// Action resolves the on_enter template for the cursor row. A string
// and nothing else: this package has no idea what a prompt or a shell
// is.
func (b *Bound) Action(f Frame) (string, bool) {
	bb, ok := b.selectable()
	if !ok || f.Cursor < 0 || f.Cursor >= len(bb.data.Rows) {
		return "", false
	}
	out, err := substitute(bb.block.OnEnter, bb.data.Rows[f.Cursor])
	if err != nil {
		return "", false
	}
	return out, true
}

// SelectableRows reports how many rows Frame.Cursor addresses, so a
// caller can clamp its own cursor without knowing the view's shape.
func (b *Bound) SelectableRows() (int, bool) {
	bb, ok := b.selectable()
	if !ok {
		return 0, false
	}
	return len(bb.data.Rows), true
}

// selectable is the block the cursor addresses. v1 takes the first one
// carrying on_enter; a view wanting two interactive blocks needs
// Frame.Cursor to become a (block, row) pair.
func (b *Bound) selectable() (boundBlock, bool) {
	for _, bb := range b.blocks {
		if bb.block.OnEnter != "" {
			return bb, true
		}
	}
	return boundBlock{}, false
}

// selectRows applies a block's Where filter then its Sort. Both are
// per-block, so two blocks can show different slices of one parse.
func selectRows(b Block, rows []Row) ([]Row, error) {
	m, err := parseMatch(b.Where)
	if err != nil {
		return nil, err
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		if m.match(r) {
			out = append(out, r)
		}
	}
	if b.Sort != nil {
		s := *b.Sort
		slices.SortStableFunc(out, func(x, y Row) int { return compareRows(x, y, s) })
	}
	return out, nil
}

func compareRows(x, y Row, s Sort) int {
	a, bb := x[s.Field], y[s.Field]
	var n int
	if s.Numeric {
		n = cmpFloat(parseFloat(a), parseFloat(bb))
	} else {
		n = strings.Compare(a, bb)
	}
	if s.Desc {
		return -n
	}
	return n
}

func parseFloat(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func fieldsOf(rows []Row) []string {
	set := map[string]struct{}{}
	for _, r := range rows {
		for k := range r {
			set[k] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(set))
}
