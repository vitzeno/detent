// Package viewspec interprets a view specification: how to read a
// command's output, and how to draw what was read.
//
// It imports the standard library and nothing else. Everything it
// needs from a caller arrives through Painter, Widget and Extractor,
// all declared here because this package is what calls them.
//
// The three calls are priced by how often they happen: Compile once
// per spec, Bind once per output, Draw once per frame.
package viewspec

// Version is the spec format this package understands.
const Version = 1

// Spec is one view. Blocks render top to bottom.
type Spec struct {
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
}

// Row is one extracted record: field name to the literal bytes that
// matched. Never model-authored.
type Row map[string]string

// Data is what a Widget draws from. Raw is always present, so a widget
// wanting bytes never has to go back to the caller for them.
type Data struct {
	Rows []Row
	Raw  string
}

// Block configures one widget. Kind decides which fields are read; the
// rest are ignored. Flat rather than a per-kind union so the whole
// spec is one fixed object shape under strict JSON schema.
type Block struct {
	Kind string `json:"kind"`

	Title string `json:"title,omitempty"`
	Field string `json:"field,omitempty"`

	Columns []Column `json:"columns,omitempty"`
	Where   string   `json:"where,omitempty"`
	Sort    *Sort    `json:"sort,omitempty"`
	Accent  *Accent  `json:"accent,omitempty"`

	// CountWhere and Of are computed from rows by the interpreter. A
	// meter's numbers are counted here, never written by the model.
	CountWhere string `json:"count_where,omitempty"`
	Of         string `json:"of,omitempty"`

	OnEnter string `json:"on_enter,omitempty"`
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

// title returns what a column is headed by, defaulting to its field.
func (c Column) title() string {
	if c.Title != "" {
		return c.Title
	}
	return c.Field
}
