package tool

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

// Registry is the vocabulary one session offers. The schema the model
// sees and the validation it is held to come from one Spec.
type Registry struct {
	tools map[string]Tool
	order []string
}

// Standard is what detent ships.
func Standard() *Registry {
	r := &Registry{tools: map[string]Tool{}}
	r.Register(Bash{})
	r.Register(ReadFile{})
	r.Register(WriteFile{})
	r.Register(ListDir{})
	return r
}

// Prepare validates a call and lowers it. Every error here reaches the
// model as a tool result, so each says what to do instead.
func (r *Registry) Prepare(name string, args map[string]any) (Call, error) {
	t, ok := r.tools[name]
	if !ok {
		return Call{}, fmt.Errorf("no tool named %q; available: %s", name, strings.Join(r.Names(), ", "))
	}
	clean, err := validate(t.Describe(), args)
	if err != nil {
		return Call{}, fmt.Errorf("%s: %w", name, err)
	}
	cmd, err := t.Lower(clean)
	if err != nil {
		return Call{}, fmt.Errorf("%s: %w", name, err)
	}
	return Call{Tool: name, Command: cmd, Mutability: t.Describe().Mutability, Args: clean}, nil
}

// Call is one validated, lowered invocation.
type Call struct {
	Tool       string
	Command    string
	Mutability string
	Args       Args
}

func (r *Registry) Register(t Tool) {
	n := t.Name()
	if _, dup := r.tools[n]; !dup {
		r.order = append(r.order, n)
	}
	r.tools[n] = t
}

func (r *Registry) Lookup(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Names are in registration order, which is the order the model sees.
func (r *Registry) Names() []string { return slices.Clone(r.order) }

// Schemas is the `tools` array for a chat-completions request.
func (r *Registry) Schemas() []map[string]any {
	out := make([]map[string]any, 0, len(r.order))
	for _, n := range r.order {
		t := r.tools[n]
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        n,
				"description": t.Describe().Description,
				"parameters":  schema(t.Describe()),
				"strict":      true,
			},
		})
	}
	return out
}

func schema(s Spec) map[string]any {
	props := map[string]any{}
	var required []string
	for _, p := range s.Params {
		props[p.Name] = map[string]any{"type": p.Type, "description": p.Desc}
		if p.Required {
			required = append(required, p.Name)
		}
	}
	sort.Strings(required)
	return map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	}
}

// validate drops absent optionals and rejects unknown keys: a
// hallucinated parameter means the model misread the schema.
func validate(s Spec, args map[string]any) (Args, error) {
	byName := map[string]Param{}
	for _, p := range s.Params {
		byName[p.Name] = p
	}
	for k := range args {
		if _, ok := byName[k]; !ok {
			known := slices.Sorted(maps.Keys(byName))
			return nil, fmt.Errorf("unknown parameter %q; expected one of: %s", k, strings.Join(known, ", "))
		}
	}
	out := Args{}
	for _, p := range s.Params {
		v, present := args[p.Name]
		if !present || v == nil {
			if p.Required {
				return nil, fmt.Errorf("missing required parameter %q (%s)", p.Name, p.Desc)
			}
			continue
		}
		cast, err := coerce(p, v)
		if err != nil {
			return nil, err
		}
		out[p.Name] = cast
	}
	return out, nil
}

// coerce accepts what JSON actually decodes to, and refuses the rest.
func coerce(p Param, v any) (any, error) {
	switch p.Type {
	case TypeString:
		if s, ok := v.(string); ok {
			return s, nil
		}
	case TypeInt:
		switch n := v.(type) {
		case float64:
			if n == float64(int(n)) {
				return int(n), nil
			}
		case int:
			return n, nil
		}
	case TypeBool:
		if b, ok := v.(bool); ok {
			return b, nil
		}
	}
	return nil, fmt.Errorf("parameter %q must be %s, got %T", p.Name, p.Type, v)
}
