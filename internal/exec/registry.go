// Package exec holds the handler registry and the code that actually runs a
// capability. It is the only place in the tree that touches a real
// filesystem or process — everything upstream (classify, gate, loop) only
// ever names a capability by its qualified name (PLAN.md §2's critical seam).
package exec

import (
	"context"
	"fmt"
)

// Args is what a handler receives — already-resolved values, one per
// capabilities.Arg. Step 2 dispatches these by hand; from step 6 onward
// they come from extract.Constructor + a Judge's target Choice, but a
// handler can't tell the difference and shouldn't need to.
type Args map[string]any

// Result is a handler's raw output. It stays a plain string deliberately —
// reduce (§4.4, step 4) is what turns this into decision-relevant facts,
// and the human render (§8) always shows this raw form, never a reduced
// one. Handlers should not pre-shape their output for either consumer.
type Result struct {
	Output string
}

// Handler executes one capability. Registered by qualified name
// ("unix__read_file"), never by the bare action name (§3).
type Handler func(ctx context.Context, args Args) (Result, error)

// Registry is the handler side of PLAN.md §3's "handlers register in Go by
// name at startup" rule. It knows nothing about the capability schema —
// capabilities.Registry.Validate takes this registry's Names() as a plain
// set precisely so neither package has to import the other.
type Registry struct {
	handlers map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

// Register wires a handler in under a qualified name. Registering the same
// name twice is a startup bug, not a runtime condition to tolerate, so it
// panics rather than returning an error — consistent with how Go's own
// database/sql and image packages treat duplicate driver registration.
func (r *Registry) Register(qualifiedName string, h Handler) {
	if _, exists := r.handlers[qualifiedName]; exists {
		panic(fmt.Sprintf("exec: handler already registered for %q", qualifiedName))
	}
	r.handlers[qualifiedName] = h
}

// Names returns the set of qualified names with a registered handler, in
// the exact shape capabilities.Registry.Validate expects.
func (r *Registry) Names() map[string]bool {
	names := make(map[string]bool, len(r.handlers))
	for qn := range r.handlers {
		names[qn] = true
	}
	return names
}

// Dispatch runs the handler registered under qualifiedName. Step 2 calls
// this with hand-picked args to prove the wiring; nothing about Dispatch
// itself changes once the loop (step 7) is the caller instead.
func (r *Registry) Dispatch(ctx context.Context, qualifiedName string, args Args) (Result, error) {
	h, ok := r.handlers[qualifiedName]
	if !ok {
		return Result{}, fmt.Errorf("exec: no handler registered for %q", qualifiedName)
	}
	return h(ctx, args)
}
