package tool

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
)

// Registry is the vocabulary one session offers, from one Spec. Safe
// for concurrent use: MCP tools register after the built-ins.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	order []string
	// fixed is how many of order are built-ins, kept in their own order.
	fixed int
}

// Standard is what detent ships.
func Standard() *Registry {
	r := &Registry{tools: map[string]Tool{}}
	r.Register(Bash{})
	r.Register(ReadFile{})
	r.Register(WriteFile{})
	r.Register(ListDir{})
	r.Register(WebSearch{})
	r.fixed = len(r.order)
	return r
}

// Prepare validates a call and lowers it. Every error here reaches the
// model as a tool result, so each says what to do instead.
func (r *Registry) Prepare(name string, args map[string]any) (Call, error) {
	t, ok := r.Lookup(name)
	if !ok {
		return Call{}, fmt.Errorf("no tool named %q; available: %s", name, strings.Join(r.Names(), ", "))
	}
	spec := t.Describe()
	// A raw schema is not ours to check. The server that published it
	// validates, and says what was wrong as a tool result.
	clean := Args(args)
	if spec.Raw == nil {
		var err error
		if clean, err = validate(spec, args); err != nil {
			return Call{}, fmt.Errorf("%s: %w", name, err)
		}
	}
	cmd, err := t.Lower(clean)
	if err != nil {
		return Call{}, fmt.Errorf("%s: %w", name, err)
	}
	return Call{Tool: name, Command: cmd, Mutability: spec.Mutability,
		Args: clean, Executor: spec.Executor}, nil
}

// Call is one validated, lowered invocation.
type Call struct {
	Tool       string
	Command    string
	Mutability string
	Args       Args
	// Executor is empty for a shell command, and otherwise names what
	// runs it instead.
	Executor string
}

func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := t.Name()
	if _, dup := r.tools[n]; !dup {
		r.order = append(r.order, n)
		// Sorted after the built-ins, so the order the model sees does
		// not depend on which MCP server answered first.
		slices.Sort(r.order[r.fixed:])
	}
	r.tools[n] = t
}

func (r *Registry) Lookup(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Names are the built-ins, then the rest by name: the order the model sees.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Clone(r.order)
}

// Schemas is the `tools` array for a chat-completions request.
func (r *Registry) Schemas() []map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]map[string]any, 0, len(r.order))
	for _, n := range r.order {
		spec := r.tools[n].Describe()
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        n,
				"description": spec.Description,
				"parameters":  schema(spec),
				// Strict demands a shape an arbitrary schema will not
				// have, so a raw one is offered as it came.
				"strict": spec.Raw == nil,
			},
		})
	}
	return out
}

// schema renders a Spec as JSON Schema. Under strict mode every
// property must appear in required, so an optional parameter is
// nullable rather than omitted; validate already reads an explicit
// null as absent.
func schema(s Spec) map[string]any {
	if s.Raw != nil {
		return s.Raw
	}
	props := map[string]any{}
	required := make([]string, 0, len(s.Params))
	for _, p := range s.Params {
		t := any(p.Type)
		if !p.Required {
			t = []string{p.Type, "null"}
		}
		props[p.Name] = map[string]any{"type": t, "description": p.Desc}
		required = append(required, p.Name)
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
