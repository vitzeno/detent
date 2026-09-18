package capabilities

import (
	"fmt"
	"sort"
)

// Registered is one action merged with its owning domain, keyed by its
// qualified name. Nothing downstream should carry a bare Action without
// knowing which domain it came from.
type Registered struct {
	QualifiedName string
	Domain        string
	Action        Action
}

// Registry holds the currently loaded capabilities. It is mutable, not
// load-once (§3): MCP servers connect and disconnect mid-session, so
// callers add domains over the registry's lifetime rather than rebuilding
// it once at boot.
type Registry struct {
	entries map[string]Registered
}

func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]Registered)}
}

// Load adds every action in a domain under its qualified name. Handler
// existence is checked separately by Validate, since capabilities and
// exec (§9 step 2) load independently and either may come up first.
func (r *Registry) Load(d Domain) error {
	for _, a := range d.Actions {
		qn := QualifiedName(d.Domain, a.Name)
		if _, exists := r.entries[qn]; exists {
			return fmt.Errorf("capabilities: duplicate action %q", qn)
		}
		r.entries[qn] = Registered{QualifiedName: qn, Domain: d.Domain, Action: a}
	}
	return nil
}

// Get looks up one capability by its qualified name ("unix__read_file").
func (r *Registry) Get(qualifiedName string) (Registered, bool) {
	reg, ok := r.entries[qualifiedName]
	return reg, ok
}

// All returns every loaded capability, sorted by qualified name for
// deterministic output (Choice criteria built from this must not vary
// run to run for reasons unrelated to the registry's actual contents).
func (r *Registry) All() []Registered {
	out := make([]Registered, 0, len(r.entries))
	for _, reg := range r.entries {
		out = append(out, reg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].QualifiedName < out[j].QualifiedName })
	return out
}

// Validate fails loudly (§3) if any loaded action has no entry in
// handlerNames — the set exec.Register calls have populated. Kept as a
// plain set rather than a stored callback so this package never imports
// exec: the critical seam (§2) applies to this relationship too, even
// though only classify/gate/loop/tui are named there explicitly.
func (r *Registry) Validate(handlerNames map[string]bool) error {
	var missing []string
	for qn := range r.entries {
		if !handlerNames[qn] {
			missing = append(missing, qn)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("capabilities: no handler registered for: %v", missing)
	}
	return nil
}

// ValidateReducers fails loudly (§3) if any loaded action names a reducer
// — other than the "none" sentinel — that isn't in reducerNames, the set
// reduce.Register calls have populated. "none" means the action has no
// output worth reducing (e.g. a destructive action with nothing to read
// back) and is never looked up, the same way Produces "none" names no real
// type. Same decoupling reasoning as Validate: this package never imports
// reduce.
func (r *Registry) ValidateReducers(reducerNames map[string]bool) error {
	var missing []string
	for qn, reg := range r.entries {
		if reg.Action.Reducer == "none" {
			continue
		}
		if !reducerNames[reg.Action.Reducer] {
			missing = append(missing, fmt.Sprintf("%s (reducer %q)", qn, reg.Action.Reducer))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("capabilities: no reducer registered for: %v", missing)
	}
	return nil
}
