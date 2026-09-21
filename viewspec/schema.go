package viewspec

import "slices"

// Schema describes the registry's vocabulary as a JSON Schema, strict
// enough for a structured-output request. Register a widget and the
// model's schema includes it; a hand-written list would rot silently.
func (r *Registry) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"version": map[string]any{"type": "integer", "enum": []int{Version}},
			"match":   str("the normalised command this view is for"),
			"parse":   r.parseSchema(),
			"blocks": map[string]any{
				"type":        "array",
				"description": "widgets, drawn top to bottom",
				"items":       r.blockSchema(true),
			},
			// Structured {what, not_for, examples} per kind, not one
			// shared blurb: with this many widgets a flat description
			// costs the model's calibration. Same lesson as the
			// render_kind criteria in internal/agent/judge.go.
			"widget_guide": map[string]any{
				"type":        "object",
				"description": "what each block kind is for. Read before choosing kind; ignore when emitting.",
				"const":       r.describe(),
			},
		},
		"required":             []string{"version", "match", "parse", "blocks"},
		"additionalProperties": false,
	}
}

func (r *Registry) parseSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "how to read the output into rows; describe where fields are, never what they contain",
		"properties": map[string]any{
			"kind": enum(r.ParseKinds(), "lines applies pattern per line; columns splits on whitespace; "+
				"fixed slices at the header's own offsets, for multi-word headings; delimited splits on sep; "+
				"pairs reads key<sep>value lines; indent turns leading whitespace into a depth; "+
				"json reads objects; none skips extraction"),
			"pattern": str("lines only: a regexp with named captures, one row per matching line"),
			"skip":    map[string]any{"type": "integer", "description": "leading lines to drop before parsing"},
			"header":  map[string]any{"type": "boolean", "description": "columns only: first surviving line names the fields"},
			"fields": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
				"description": "columns and delimited: field names when header is false"},
			"sep": str("pairs and delimited: what to split each line on"),
		},
		"required":             []string{"kind", "pattern", "skip", "header", "fields", "sep"},
		"additionalProperties": false,
	}
}

// blockSchema spells a row's panes out in full rather than pointing
// back at itself. Nesting is capped at one level, so the schema is
// finite. Strict mode needs that: a recursive $ref is where backend
// portability gets thin.
func (r *Registry) blockSchema(allowRow bool) map[string]any {
	kinds := r.Kinds()
	if !allowRow {
		kinds = slices.DeleteFunc(slices.Clone(kinds), func(k string) bool { return k == RowKind })
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"kind":  enum(kinds, "which widget draws this block; see widget_guide"),
			"title": str("a short label; renders dimmed because it is your prose, not output"),
			"field": str("the field this widget reads, for single-field widgets"),
			"depth": str("tree only: the field holding each row's level; empty reads field as a path"),
			"columns": map[string]any{"type": "array", "items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"field": str("a field name the parse produces"),
					"title": str("column heading; defaults to the field name"),
					"width": map[string]any{"type": "integer", "description": "0 shares width proportionally"},
				},
				"required":             []string{"field", "title", "width"},
				"additionalProperties": false,
			}},
			"where": str(`filter as "field=value", or empty for every row`),
			"sort": nullable(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"field":   str("field to order by"),
					"numeric": map[string]any{"type": "boolean", "description": "compare as numbers"},
					"desc":    map[string]any{"type": "boolean"},
				},
				"required":             []string{"field", "numeric", "desc"},
				"additionalProperties": false,
			}),
			"accent": nullable(map[string]any{
				"type":        "object",
				"description": "colour rows by a field's value",
				"properties": map[string]any{
					"field": str("field carrying the status"),
					"map": map[string]any{"type": "object",
						"description":          "value to role",
						"additionalProperties": enum(RoleNames(), "display intent, not a colour")},
				},
				"required":             []string{"field", "map"},
				"additionalProperties": false,
			}),
			"count_where": str(`meter only: which rows count as hits, as "field=value"`),
			"of":          str(`meter only: the denominator, as "field=value" or "*" for every row`),
			"on_enter":    str("a command template using {field}; it seeds the human's prompt and never runs"),
		},
		"required": []string{"kind", "title", "field", "depth", "columns", "where", "sort",
			"accent", "count_where", "of", "on_enter"},
		"additionalProperties": false,
	}
	if !allowRow {
		return schema
	}
	props := schema["properties"].(map[string]any)
	props["panes"] = map[string]any{
		"type":        "array",
		"description": "row only: the columns laid side by side, at least two",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"weight": map[string]any{"type": "integer",
					"description": "share of the width; 0 means an equal share"},
				"blocks": map[string]any{"type": "array", "items": r.blockSchema(false)},
			},
			"required":             []string{"weight", "blocks"},
			"additionalProperties": false,
		},
	}
	schema["required"] = append(schema["required"].([]string), "panes")
	return schema
}

func str(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func enum[T any](values []T, desc string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": desc}
}

// nullable keeps an optional object expressible under strict schemas,
// where every property must be required.
func nullable(schema map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range schema {
		out[k] = v
	}
	out["type"] = []string{"object", "null"}
	return out
}
