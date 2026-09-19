package reduce

import "fmt"

// Registry holds reducers keyed by the name a capability's `reducer:`
// field (§3) refers to — a reusable name, not necessarily a qualified
// capability name, since one reducer (e.g. "path_lines") can serve every
// capability whose output has that shape.
type Registry struct {
	reducers map[string]Reducer
}

func NewRegistry() *Registry {
	return &Registry{reducers: make(map[string]Reducer)}
}

// Register wires a reducer in under a name. Like exec.Registry.Register,
// a duplicate name is a startup bug, not a runtime condition, so it panics.
func (r *Registry) Register(name string, fn Reducer) {
	if _, exists := r.reducers[name]; exists {
		panic(fmt.Sprintf("reduce: reducer already registered for %q", name))
	}
	r.reducers[name] = fn
}

// Names returns the set of registered reducer names, in the shape
// capabilities.Registry.ValidateReducers expects.
func (r *Registry) Names() map[string]bool {
	names := make(map[string]bool, len(r.reducers))
	for n := range r.reducers {
		names[n] = true
	}
	return names
}

// Reduce runs the named reducer against raw capability output.
func (r *Registry) Reduce(name, output string) (Result, error) {
	fn, ok := r.reducers[name]
	if !ok {
		return Result{}, fmt.Errorf("reduce: no reducer registered for %q", name)
	}
	return fn(output), nil
}
