package gate

import (
	"fmt"
	"path/filepath"
	"strings"
)

// DefaultDeniedRoots are rejected regardless of PathRules.AllowedRoots
// (§7): "reject /etc, /sys, /dev by default."
var DefaultDeniedRoots = []string{"/etc", "/sys", "/dev"}

// PathRules is the per-call context ValidatePath checks against. Gate
// itself has no notion of "cwd" or "configured roots" — the caller (the
// loop, eventually) supplies both, so gate stays a pure function of its
// inputs and every rule stays testable without touching the real
// filesystem's actual cwd.
type PathRules struct {
	AllowedRoots []string // the resolved path must fall inside one of these
	DeniedRoots  []string // checked first; defaults to DefaultDeniedRoots when nil
}

// ValidatePath applies Layer 2's path rules (§7): resolve symlinks, reject
// anything inside a denied root, reject anything outside every allowed
// root. It returns the fully-resolved absolute path rather than the input
// string — the gate's job is to hand back the real target, not the
// possibly-deceptive path a goal or a Judge wrote, so a symlink pointing
// outside an allowed root is caught by resolving it before the comparison,
// not by pattern-matching the string.
func ValidatePath(path string, rules PathRules) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("gate: resolving %q: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("gate: resolving %q: %w", path, err)
	}

	denied := rules.DeniedRoots
	if denied == nil {
		denied = DefaultDeniedRoots
	}
	for _, d := range denied {
		// Resolved the same way as allowed roots: on macOS /etc is itself a
		// symlink to /private/etc, so comparing against the raw string
		// would silently miss anything EvalSymlinks already resolved into
		// /private/etc. An unresolvable denied root (e.g. /sys on macOS,
		// which doesn't exist) is skipped rather than erroring — if it
		// doesn't exist, resolved couldn't have landed inside it, since
		// resolving resolved itself would already have failed.
		resolvedDenied, err := resolveRoot(d)
		if err != nil {
			continue
		}
		if within(resolved, resolvedDenied) {
			return "", fmt.Errorf("gate: %q is inside denied root %q", resolved, d)
		}
	}

	for _, root := range rules.AllowedRoots {
		resolvedRoot, err := resolveRoot(root)
		if err != nil {
			continue // an allowed root that doesn't exist can't match anything
		}
		if within(resolved, resolvedRoot) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("gate: %q is outside all allowed roots", resolved)
}

func resolveRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// within reports whether path is root itself or lies beneath it, computed
// component-wise via filepath.Rel rather than a string-prefix check — a
// naive strings.HasPrefix(path, root) would wrongly admit "/a/bc" under
// root "/a/b".
func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}
