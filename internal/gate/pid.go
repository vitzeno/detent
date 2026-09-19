package gate

import "fmt"

// ProcessInfo is the minimal shape ValidatePID needs. Owner is required
// because §7's "not owned by the current user" rule can't be checked from
// a bare pid — the loop is expected to have already fetched this from a
// real process listing before offering a pid as a candidate. Gate never
// fetches its own process list, so a check always reflects the same
// snapshot the rest of the step saw, not a fresher (or staler) one.
type ProcessInfo struct {
	PID   int
	Owner string
}

// ValidatePID applies Layer 2's pid rules (§7): reject pid 1 outright,
// reject anything absent from the fetched process list, reject processes
// not owned by currentUser.
func ValidatePID(pid int, known []ProcessInfo, currentUser string) error {
	if pid == 1 {
		return fmt.Errorf("gate: refusing pid 1")
	}
	for _, p := range known {
		if p.PID != pid {
			continue
		}
		if p.Owner != currentUser {
			return fmt.Errorf("gate: pid %d not owned by %q", pid, currentUser)
		}
		return nil
	}
	return fmt.Errorf("gate: pid %d not present in the fetched process list", pid)
}
