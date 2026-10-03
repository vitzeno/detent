// Package viewspec interprets a view specification: how to read a
// command's output, and how to draw what was read. It imports the
// standard library and nothing else.
package viewspec

import (
	"maps"
	"slices"
)

// Version is the spec format this package understands.
const Version = 1

// RowKind lays its panes side by side and PanelKind frames its one pane.
// The interpreter draws both, since a Widget never sees the registry.
const (
	RowKind   = "row"
	PanelKind = "panel"
)

// Spec is one view. Blocks render top to bottom.
type Spec struct {
	// Version 0 means the current one.
	Version int     `json:"version"`
	Match   string  `json:"match"`
	Parse   Parse   `json:"parse"`
	Blocks  []Block `json:"blocks"`
}

// Parse turns captured bytes into rows. Kind "none" skips extraction
// and blocks bind to raw text only.
type Parse struct {
	Kind    string   `json:"kind"`
	Pattern string   `json:"pattern,omitempty"`
	Skip    int      `json:"skip,omitempty"`
	Header  bool     `json:"header,omitempty"`
	Fields  []string `json:"fields,omitempty"`
	// Sep is what pairs and delimited split on.
	Sep string `json:"sep,omitempty"`
}

// Row is one extracted record: field name to the literal bytes that
// matched. Never model-authored.
type Row map[string]string

// Data is what a Widget draws from. Raw is always present, so a widget
// wanting bytes never has to go back to the caller for them.
type Data struct {
	Rows []Row
	Raw  string
	// Columns is the row keys in parse order and the output's spelling, which a map cannot keep.
	Columns []Column
}

// Block configures one widget. Kind decides which fields are read. Flat
// rather than a per-kind union, so every saved spec has one shape.
type Block struct {
	Kind string `json:"kind"`

	Title string `json:"title,omitempty"`
	Field string `json:"field,omitempty"`
	// Depth is a tree's level field. Empty reads Field as a path.
	Depth string `json:"depth,omitempty"`

	Columns []Column `json:"columns,omitempty"`
	Where   string   `json:"where,omitempty"`
	Sort    *Sort    `json:"sort,omitempty"`
	Accent  *Accent  `json:"accent,omitempty"`

	// CountWhere and Of count rows, so a meter's numbers are never model-written.
	CountWhere string `json:"count_where,omitempty"`
	Of         string `json:"of,omitempty"`

	OnEnter string `json:"on_enter,omitempty"`

	// Panes is set only on a container. Nesting stops at one level.
	Panes []Pane `json:"panes,omitempty"`
}

// Pane is one column of a row. Weight shares the width, and 0 means an
// equal share with every other 0.
type Pane struct {
	Weight int     `json:"weight,omitempty"`
	Blocks []Block `json:"blocks"`
}

// Column is one table or keyvalue column. Width 0 shares the frame
// proportionally.
type Column struct {
	Field string `json:"field"`
	Title string `json:"title,omitempty"`
	Width int    `json:"width,omitempty"`
}

// Sort orders rows by one field. Numeric compares as numbers, so 9.5
// sorts before 10.2 rather than after it.
type Sort struct {
	Field   string `json:"field"`
	Numeric bool   `json:"numeric,omitempty"`
	Desc    bool   `json:"desc,omitempty"`
}

// Accent maps a field's values onto roles. Role names, never colours.
type Accent struct {
	Field string          `json:"field"`
	Map   map[string]Role `json:"map"`
}

// Clone is a deep copy, so editing it cannot reach a spec it was copied from.
func (s Spec) Clone() Spec {
	s.Parse.Fields = slices.Clone(s.Parse.Fields)
	s.Blocks = cloneBlocks(s.Blocks)
	return s
}

func cloneBlocks(in []Block) []Block {
	if in == nil {
		return nil
	}
	out := make([]Block, len(in))
	for i, b := range in {
		b.Columns = slices.Clone(b.Columns)
		if b.Sort != nil {
			sort := *b.Sort
			b.Sort = &sort
		}
		if b.Accent != nil {
			b.Accent = &Accent{Field: b.Accent.Field, Map: maps.Clone(b.Accent.Map)}
		}
		if b.Panes != nil {
			panes := make([]Pane, len(b.Panes))
			for j, p := range b.Panes {
				panes[j] = Pane{Weight: p.Weight, Blocks: cloneBlocks(p.Blocks)}
			}
			b.Panes = panes
		}
		out[i] = b
	}
	return out
}

// title returns what a column is headed by, defaulting to its field.
func (c Column) title() string {
	if c.Title != "" {
		return c.Title
	}
	return c.Field
}
