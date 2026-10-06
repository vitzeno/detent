package tool

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/vitzeno/detent/event"
)

// Registry is the vocabulary one session offers, from one Spec. Safe
// for concurrent use: MCP tools register after the built-ins.
type Registry struct {
	mu    sync.RWMutex
	tools map[event.ToolName]Tool
	order []event.ToolName
	// fixed is how many of order are built-ins, kept in their own order.
	fixed int
}

// Standard is what detent ships, plus extra built-ins such as skill.
func Standard(extra ...Tool) *Registry { return StandardFor(Bash{}, extra...) }

// Shell is the tool commands run through: powershell when pwsh is set, else bash.
func Shell(pwsh bool) Tool {
	if pwsh {
		return PowerShell{}
	}
	return Bash{}
}

// StandardFor is Standard with shell in bash's place, such as PowerShell.
func StandardFor(shell Tool, extra ...Tool) *Registry {
	r := &Registry{tools: map[event.ToolName]Tool{}}
	builtins := append([]Tool{shell, ReadFile{}, WriteFile{}, EditFile{}, ListDir{}, Grep{}, FindFiles{}, WebSearch{}}, extra...)
	for _, t := range builtins {
		if _, dup := r.tools[t.Name()]; !dup {
			r.order = append(r.order, t.Name())
		}
		r.tools[t.Name()] = t
	}
	r.fixed = len(r.order)
	return r
}

// Without is a registry of every tool but the named ones, sharing them and
// keeping their order, such as what a subagent may call.
func (r *Registry) Without(names ...event.ToolName) *Registry {
	r.mu.RLock()
	keep := slices.DeleteFunc(slices.Clone(r.order), func(n event.ToolName) bool { return slices.Contains(names, n) })
	r.mu.RUnlock()
	return r.Only(keep...)
}

// Only is a registry of just the named tools, sharing them and keeping their
// order, such as what a subagent may call. A name not registered is skipped.
func (r *Registry) Only(names ...event.ToolName) *Registry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := &Registry{tools: map[event.ToolName]Tool{}}
	for i, n := range r.order {
		if t, ok := r.tools[n]; ok && slices.Contains(names, n) {
			out.tools[n] = t
			out.order = append(out.order, n)
			if i < r.fixed {
				out.fixed++
			}
		}
	}
	return out
}

// Call is one validated, lowered tool call.
type Call struct {
	Tool       event.ToolName
	Command    string
	Mutability string
	Args       Args
	// Executor is empty for a shell command, and otherwise names what
	// runs it instead.
	Executor  string
	Delegates bool
	Internal  bool
}

// Prepare validates a call and lowers it. Every error here reaches the
// model as a tool result, so each says what to do instead.
func (r *Registry) Prepare(name event.ToolName, args map[string]any) (Call, error) {
	t, ok := r.Lookup(name)
	if !ok {
		return Call{}, fmt.Errorf("no tool named %q; available: %s", name, joined(r.Names()))
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
		Args: clean, Executor: spec.Executor, Delegates: spec.Delegates, Internal: spec.Internal}, nil
}

// Register adds a tool, replacing any of the same name except a built-in,
// which always wins a collision.
func (r *Registry) Register(t Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := t.Name()
	if r.builtin(n) {
		return fmt.Errorf("%s is a built-in tool and cannot be replaced", n)
	}
	if _, dup := r.tools[n]; !dup {
		r.order = append(r.order, n)
		// Sorted after the built-ins, so the order the model sees does
		// not depend on which MCP server answered first.
		slices.Sort(r.order[r.fixed:])
	}
	r.tools[n] = t
	return nil
}

// Unregister removes tools other than built-ins, as when an MCP server is
// dialled again and its old session's tools must not outlive it.
func (r *Registry) Unregister(names ...event.ToolName) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range names {
		if _, ok := r.tools[n]; ok && !r.builtin(n) {
			delete(r.tools, n)
			r.order = slices.DeleteFunc(r.order, func(o event.ToolName) bool { return o == n })
		}
	}
}

// Lookup finds a tool by name.
func (r *Registry) Lookup(name event.ToolName) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Names are the built-ins, then the rest by name: the order the model sees.
func (r *Registry) Names() []event.ToolName {
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
				// A string, since the wire and whatever reads a schema back expect one.
				"name":        string(n),
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

// Native is the named tool when it can run in this process.
func (r *Registry) Native(name event.ToolName) (Native, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.tools[name].(Native)
	return n, ok
}

// builtin reports whether name is one of the tools Standard fixed in place.
func (r *Registry) builtin(name event.ToolName) bool {
	return slices.Contains(r.order[:r.fixed], name)
}

// joined lists names for a message.
func joined(names []event.ToolName) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = string(n)
	}
	return strings.Join(out, ", ")
}

// schema renders a Spec as JSON Schema. Strict mode wants every property
// in required, so an optional one is nullable, which validate reads as absent.
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
		prop := map[string]any{"type": t, "description": p.Desc}
		if len(p.Enum) > 0 {
			values := make([]any, 0, len(p.Enum)+1)
			for _, v := range p.Enum {
				values = append(values, v)
			}
			if !p.Required {
				values = append(values, nil)
			}
			prop["enum"] = values
		}
		props[p.Name] = prop
		required = append(required, p.Name)
	}
	slices.Sort(required)
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
		// Strict mode enforces an enum, but not every endpoint is strict.
		if s, ok := cast.(string); ok && len(p.Enum) > 0 && !slices.Contains(p.Enum, s) {
			return nil, fmt.Errorf("parameter %q must be one of: %s, got %q", p.Name, strings.Join(p.Enum, ", "), s)
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
