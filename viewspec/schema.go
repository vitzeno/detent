package viewspec

// Schema describes the registry's vocabulary as a JSON Schema, strict
// enough for a structured-output request. Register a widget and the
// schema a model gets includes it — a hand-written list in the
// generator would rot silently, never emitting the new kind.
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
				"items":       r.blockSchema(),
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
			"kind":    enum(r.ParseKinds(), "lines applies pattern per line; columns splits whitespace; json reads objects; none skips extraction"),
			"pattern": str("lines only: a regexp with named captures, one row per matching line"),
			"skip":    map[string]any{"type": "integer", "description": "leading lines to drop before parsing"},
			"header":  map[string]any{"type": "boolean", "description": "columns only: first surviving line names the fields"},
			"fields": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
				"description": "columns only: field names when header is false"},
		},
		"required":             []string{"kind", "pattern", "skip", "header", "fields"},
		"additionalProperties": false,
	}
}

func (r *Registry) blockSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"kind":  enum(r.Kinds(), "which widget draws this block"),
			"title": str("a short label; renders dimmed because it is your prose, not output"),
			"field": str("the field this widget reads, for single-field widgets"),
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
		"required": []string{"kind", "title", "field", "columns", "where", "sort",
			"accent", "count_where", "of", "on_enter"},
		"additionalProperties": false,
	}
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
