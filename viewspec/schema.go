package viewspec

import (
	"maps"
	"slices"
	"strings"
)

// Schema describes the registry's vocabulary as a JSON Schema, strict
// enough for a structured-output request, so a registered widget is offered.
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
			// Structured per kind, since one flat blurb costs a model's calibration.
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
				"pairs reads key<sep>value lines; prefix takes the first token and the rest of each line; "+
				"indent turns leading whitespace into a depth; box reads a table drawn with borders; "+
				"json reads objects; none skips extraction"),
			"pattern": str("lines only: a regexp with named captures, one row per matching line"),
			"skip":    map[string]any{"type": "integer", "description": "leading lines to drop before parsing"},
			"header":  map[string]any{"type": "boolean", "description": "columns and delimited: first surviving line names the fields"},
			"fields": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
				"description": "columns and delimited: field names when header is false"},
			"sep": str("pairs and delimited: what to split each line on"),
		},
		"required":             []string{"kind", "pattern", "skip", "header", "fields", "sep"},
		"additionalProperties": false,
	}
}

// blockSchema spells a container's panes out in full rather than with a
// recursive $ref, which strict mode handles unevenly across backends.
func (r *Registry) blockSchema(allowContainers bool) map[string]any {
	kinds := r.Kinds()
	if !allowContainers {
		kinds = slices.DeleteFunc(slices.Clone(kinds), r.isContainer)
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
			"count_where": str(`meter and stat: which rows count as hits, as "field=value"`),
			"of":          str(`meter and stat: the denominator, as "field=value" or "*" for every row`),
			"on_enter": str("a command template using {field}; it seeds the human's prompt and never runs. Only on a kind that draws one row per line: " +
				strings.Join(slices.DeleteFunc(slices.Clone(kinds), func(k string) bool { return !r.Selects(k) }), ", ")),
		},
		"required": []string{"kind", "title", "field", "depth", "columns", "where", "sort",
			"accent", "count_where", "of", "on_enter"},
		"additionalProperties": false,
	}
	if !allowContainers {
		return schema
	}
	props := schema["properties"].(map[string]any)
	props["panes"] = map[string]any{
		"type":        "array",
		"description": "containers only: a row lays its panes side by side and needs at least two, a panel frames exactly one",
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
	out := maps.Clone(schema)
	out["type"] = []string{"object", "null"}
	return out
}
