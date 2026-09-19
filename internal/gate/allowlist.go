package gate

import "github.com/vitzeno/detent/internal/capabilities"

// Layer1Allowed is the structural allowlist (§7): nothing outside the
// loaded capability registry may be dispatched. exec.Registry.Dispatch
// already refuses an unregistered qualified name as a side effect of a map
// lookup, but that check has no name of its own to test directly — this
// gives Layer 1 an explicit, independently testable entry point that runs
// before dispatch is even attempted.
func Layer1Allowed(capReg *capabilities.Registry, qualifiedName string) bool {
	_, ok := capReg.Get(qualifiedName)
	return ok
}
