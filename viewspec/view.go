package viewspec

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Compiled is a validated spec. Reusable across outputs and frames,
// and cheap to keep: it holds no data and no styling.
type Compiled struct {
	spec Spec
	reg  *Registry
	ext  Extractor
}

// Compile validates spec against the vocabulary: block and parse kinds
// exist, a pattern compiles, and every static field is present.
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
		if err := checkBlock(b, o.reg, true); err != nil {
			var be *BindError
			if errors.As(err, &be) {
				be.Block = i
				return nil, be
			}
			return nil, &BindError{Block: i, Kind: b.Kind, Err: err}
		}
	}
	return &Compiled{spec: spec, reg: o.reg, ext: ext}, nil
}

// Spec returns what was compiled.
func (c *Compiled) Spec() Spec { return c.spec }

// Bind resolves every binding against the rows that actually came
// out. Anything unresolved fails the whole view, never one block.
func (c *Compiled) Bind(output string) (*Bound, error) {
	rows, order, err := extract(c.ext, output)
	if err != nil {
		return nil, fmt.Errorf("viewspec: parse %q: %w", c.spec.Parse.Kind, err)
	}
	fields := fieldsOf(rows)
	out := &Bound{c: c, raw: output, fields: fields, rows: rows, selSeq: -1}
	seq := 0
	out.blocks, err = c.bindBlocks(c.spec.Blocks, rows, Data{
		Rows: nil, Raw: output, Columns: orderedColumns(order, fields)}, fields, &seq)
	if err != nil {
		return nil, err
	}
	if sel, ok := out.selectable(); ok {
		out.selSeq = sel.seq
	}
	return out, nil
}

// Bound is one output resolved against a compiled spec: parsed,
// filtered and sorted, with every binding checked. Once per output.
type Bound struct {
	c      *Compiled
	raw    string
	fields []string
	rows   []Row
	blocks []boundBlock
	selSeq int
}

// Draw assembles lines at a size. It runs every frame, so it parses
// nothing, and draws the view whole so the caller can window it.
func (b *Bound) Draw(f Frame) (Render, error) {
	if f.Paint == nil {
		f.Paint = Plain()
	}
	if f.Width <= 0 {
		return Render{}, errors.New("viewspec: frame has no width")
	}
	lines, cursor, err := drawBlocks(b.blocks, f, b.selSeq)
	if err != nil {
		return Render{}, err
	}
	return Render{Lines: lines, CursorLine: cursor}, nil
}

// Action resolves the on_enter template for the cursor row. What the
// string is for is the caller's business.
func (b *Bound) Action(f Frame) (string, bool) {
	bb, ok := b.selectable()
	if !ok || bb.block.OnEnter == "" || f.Cursor < 0 || f.Cursor >= len(bb.data.Rows) {
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

// Fields lists the field names the parse actually produced, sorted.
func (b *Bound) Fields() []string { return slices.Clone(b.fields) }

// Hides reports whether most of the output reaches no block: rows came
// from under half its lines, and nothing draws the text as it came.
func (b *Bound) Hides() bool {
	if !perLine(b.c.ext) {
		return false
	}
	for _, bb := range leaves(b.blocks) {
		if d, ok := bb.w.(Described); ok && d.Describe().Raw {
			return false
		}
	}
	lines := 0
	for _, l := range splitLines(b.raw) {
		if strings.TrimSpace(l) != "" {
			lines++
		}
	}
	return len(b.rows)*2 < lines
}

// Rows is how many rows the parse produced, before any block filtered.
func (b *Bound) Rows() int { return len(b.rows) }

// Sample returns copies of up to n parsed rows, before any block's filter
// or sort, so a caller asking which field to draw can show what each holds.
func (b *Bound) Sample(n int) []Row {
	if n <= 0 || len(b.rows) == 0 {
		return nil
	}
	out := make([]Row, min(n, len(b.rows)))
	for i := range out {
		out[i] = maps.Clone(b.rows[i])
	}
	return out
}

// Render is one drawn view. A struct rather than a bare []string so a
// caller can scroll to the selection without knowing the layout.
type Render struct {
	Lines []string
	// CursorLine indexes Lines for the selected row, or -1 when
	// nothing in the view takes a selection.
	CursorLine int
}

// Frame is everything Draw needs that Bind could not know. Painter is
// here so Compile and Bind stay pure data.
type Frame struct {
	Width int
	// Height is for widgets that grow into the pane. 0 is unknown, and it clips nothing.
	Height  int
	Focused bool
	Cursor  int
	Paint   Painter
}

// Option configures Compile.
type Option func(*options)

// WithRegistry compiles against a custom vocabulary instead of Standard.
// A nil registry leaves Standard in place.
func WithRegistry(r *Registry) Option {
	return func(o *options) {
		if r != nil {
			o.reg = r
		}
	}
}

type options struct{ reg *Registry }

// BindError says which block failed to resolve and why.
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

// checkBlock validates one block against the vocabulary. topLevel
// gates rows: nesting is capped at one, so a pane holds leaves only.
func checkBlock(b Block, reg *Registry, topLevel bool) error {
	w, ok := reg.widget(b.Kind)
	if !ok {
		return &BindError{Kind: b.Kind,
			Err: fmt.Errorf("unknown block kind (have %s)", strings.Join(reg.Kinds(), ", "))}
	}
	// A kind that cannot say which row the cursor is on cannot act on one.
	if b.OnEnter != "" {
		if _, ok := w.(Selector); !ok {
			return &BindError{Kind: b.Kind,
				Err: errors.New("on_enter needs a kind that draws one row per line")}
		}
	}
	c, ok := w.(Container)
	if !ok {
		if len(b.Panes) > 0 {
			return &BindError{Kind: b.Kind, Err: errors.New("only a container has panes")}
		}
		return checkStatic(b)
	}
	if !topLevel {
		return &BindError{Kind: b.Kind, Err: errors.New("containers do not nest")}
	}
	if err := c.Accept(b.Panes); err != nil {
		return &BindError{Kind: b.Kind, Err: err}
	}
	for _, pane := range b.Panes {
		if len(pane.Blocks) == 0 {
			return &BindError{Kind: b.Kind, Err: errors.New("a pane needs at least one block")}
		}
		if pane.Weight < 0 {
			return &BindError{Kind: b.Kind, Err: errors.New("a pane's weight must not be negative")}
		}
		for _, inner := range pane.Blocks {
			if err := checkBlock(inner, reg, false); err != nil {
				return err
			}
		}
	}
	return nil
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

type boundBlock struct {
	block Block
	w     Widget
	data  Data
	// panes is set only on a container, whose Draw is never called.
	panes [][]boundBlock
	// seq numbers leaves in draw order, to find the one holding the cursor.
	seq int
}

// bindBlocks resolves one level of blocks, descending once into a
// row's panes. Errors carry the block that failed, not the depth.
func (c *Compiled) bindBlocks(blocks []Block, rows []Row, shared Data, fields []string, seq *int) ([]boundBlock, error) {
	out := make([]boundBlock, 0, len(blocks))
	for i, b := range blocks {
		w, _ := c.reg.widget(b.Kind)
		if _, ok := w.(Container); ok {
			bb := boundBlock{block: b, w: w, seq: -1}
			for _, pane := range b.Panes {
				inner, err := c.bindBlocks(pane.Blocks, rows, shared, fields, seq)
				if err != nil {
					return nil, err
				}
				bb.panes = append(bb.panes, inner)
			}
			out = append(out, bb)
			continue
		}
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
		data := shared
		data.Rows = sel
		out = append(out, boundBlock{block: b, w: w, data: data, seq: *seq})
		*seq++
	}
	return out, nil
}

// drawBlocks stacks blocks vertically, reporting where the cursor
// landed relative to what it returned.
func drawBlocks(blocks []boundBlock, f Frame, sel int) ([]string, int, error) {
	var lines []string
	cursor := -1
	for i, bb := range blocks {
		var out []string
		at := -1
		var err error
		if bb.panes != nil {
			out, at, err = drawContainer(bb, f, sel)
		} else {
			// Only the block the cursor addresses may highlight a row.
			lf := f
			lf.Focused = f.Focused && bb.seq == sel
			out, err = bb.w.Draw(bb.block, bb.data, lf)
			if bb.seq == sel {
				if s, ok := bb.w.(Selector); ok {
					at = s.CursorLine(bb.block, bb.data, lf)
				}
			}
		}
		if err != nil {
			return nil, -1, &BindError{Block: i, Kind: bb.block.Kind, Err: err}
		}
		// No line is wider than the frame, whatever a widget drew.
		for j, l := range out {
			if f.Paint.Width(l) > f.Width {
				out[j] = f.Paint.Truncate(l, f.Width)
			}
		}
		if at >= 0 && cursor < 0 {
			cursor = len(lines) + at
		}
		lines = append(lines, out...)
	}
	return lines, cursor, nil
}

// drawContainer draws a container's panes at the widths it asks for
// and hands them back to it to assemble.
func drawContainer(bb boundBlock, f Frame, sel int) ([]string, int, error) {
	c, ok := bb.w.(Container)
	if !ok {
		return nil, -1, fmt.Errorf("%q holds panes but cannot arrange them", bb.block.Kind)
	}
	widths, err := c.Widths(bb.block.Panes, f.Width)
	if err != nil {
		return nil, -1, err
	}
	cols := make([][]string, len(bb.panes))
	holder, within := -1, -1
	for i, pane := range bb.panes {
		pf := f
		pf.Width = widths[i]
		out, at, err := drawBlocks(pane, pf, sel)
		if err != nil {
			return nil, -1, err
		}
		if at >= 0 && holder < 0 {
			holder, within = i, at
		}
		cols[i] = out
	}
	out, paneAt := c.Arrange(cols, widths, bb.block, f)
	cursor := -1
	if holder >= 0 && holder < len(paneAt) {
		cursor = paneAt[holder] + within
	}
	return out, cursor, nil
}

// selectable is the block the cursor addresses: the first with on_enter,
// else the first that draws a cursor at all.
func (b *Bound) selectable() (boundBlock, bool) {
	var first boundBlock
	found := false
	for _, bb := range leaves(b.blocks) {
		if _, ok := bb.w.(Selector); !ok {
			continue
		}
		if bb.block.OnEnter != "" {
			return bb, true
		}
		if !found {
			first, found = bb, true
		}
	}
	return first, found
}

// perLine reports whether an extractor reads a record per line, which
// is what makes counting lines meaningful: a JSON document is not.
func perLine(e Extractor) bool {
	if s, ok := e.(skipExtractor); ok {
		e = s.inner
	}
	switch e.(type) {
	case noneExtractor, jsonExtractor:
		return false
	}
	return true
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
		n = cmp.Compare(number(a), number(bb))
	} else {
		n = strings.Compare(a, bb)
	}
	if s.Desc {
		return -n
	}
	return n
}

// orderedColumns prefers the extractor's own order and spelling, keeping
// only what the rows produced, and falls back to alphabetical by key.
func orderedColumns(order []Column, present []string) []Column {
	var out []Column
	seen := map[string]bool{}
	for _, c := range order {
		if slices.Contains(present, c.Field) && !seen[c.Field] {
			out, seen[c.Field] = append(out, c), true
		}
	}
	for _, f := range present {
		if !seen[f] {
			out = append(out, Column{Field: f})
		}
	}
	return out
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

// leaves is every drawable block in draw order, a row's panes
// flattened in, so selection never has to know about layout.
func leaves(blocks []boundBlock) []boundBlock {
	var out []boundBlock
	for _, bb := range blocks {
		if bb.panes == nil {
			out = append(out, bb)
			continue
		}
		for _, pane := range bb.panes {
			out = append(out, leaves(pane)...)
		}
	}
	return out
}
