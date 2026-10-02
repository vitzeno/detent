package viewspec

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// Role is display intent, not colour. A consumer maps roles onto its
// own theme.
type Role int

const (
	RoleDefault Role = iota
	RoleMuted
	RoleFaint
	RoleHeading
	RoleAccent
	RoleSafe
	RoleCaution
	RoleDanger
)

var roleNames = map[Role]string{
	RoleDefault: "default",
	RoleMuted:   "muted",
	RoleFaint:   "faint",
	RoleHeading: "heading",
	RoleAccent:  "accent",
	RoleSafe:    "safe",
	RoleCaution: "caution",
	RoleDanger:  "danger",
}

// RoleNames lists every role name, in role order. Registry.Schema uses
// it so a spec's accent values are constrained to what exists.
func RoleNames() []string {
	out := make([]string, 0, len(roleNames))
	for r := RoleDefault; r <= RoleDanger; r++ {
		out = append(out, roleNames[r])
	}
	return out
}

func (r Role) String() string {
	if n, ok := roleNames[r]; ok {
		return n
	}
	return fmt.Sprintf("Role(%d)", int(r))
}

// MarshalJSON writes the name, so a spec on disk reads as "danger"
// rather than 7.
func (r Role) MarshalJSON() ([]byte, error) {
	n, ok := roleNames[r]
	if !ok {
		return nil, fmt.Errorf("viewspec: unknown role %d", int(r))
	}
	return json.Marshal(n)
}

func (r *Role) UnmarshalJSON(b []byte) error {
	var name string
	if err := json.Unmarshal(b, &name); err != nil {
		return fmt.Errorf("viewspec: role must be a name: %w", err)
	}
	for role, n := range roleNames {
		if n == name {
			*r = role
			return nil
		}
	}
	return fmt.Errorf("viewspec: unknown role %q", name)
}

// Painter turns intent into display text. Width and Truncate live here
// because only the implementation knows whether its output carries escapes.
type Painter interface {
	Paint(r Role, s string) string
	Width(s string) int
	Truncate(s string, n int) string
}

// Plain paints nothing and measures in runes, so Draw tests need no
// escape sequences in their fixtures.
func Plain() Painter { return plain{} }

type plain struct{}

func (plain) Paint(_ Role, s string) string { return s }
func (plain) Width(s string) int            { return utf8.RuneCountInString(s) }

func (plain) Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
