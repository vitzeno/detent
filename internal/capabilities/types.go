// Package capabilities defines the capability schema (PLAN.md §3), loads
// domain files into it, and holds the registry the rest of the system reads
// from. Nothing here executes anything — that's exec's job (§9 step 2).
package capabilities

// Danger is a property of the capability, fixed at authoring time. It is
// never inferred by a model (§7): a miscalibration must never downgrade a
// destructive action.
type Danger string

const (
	DangerSafe        Danger = "safe"
	DangerCaution     Danger = "caution"
	DangerDestructive Danger = "destructive"
)

// View names the Bubble Tea component that renders a capability's output
// (§8). Presentation detail, carried here because it's authored per-action.
type View string

const (
	ViewTable    View = "table"
	ViewViewport View = "viewport"
	ViewDetail   View = "detail"
	ViewDiff     View = "diff"
)

// ArgType and Produces share one vocabulary (§3): pid, path, path[], text,
// literal, none. Keeping both as plain strings (not two enums) is what lets
// §4.2's type-reachability check compare an action's Produces against
// another action's Args without a conversion step.
type ArgType string

const (
	ArgPath    ArgType = "path"
	ArgPID     ArgType = "pid"
	ArgLiteral ArgType = "literal"
)

// Arg is one named, typed input a capability's handler requires.
type Arg struct {
	Name     string  `yaml:"name" json:"name"`
	Type     ArgType `yaml:"type" json:"type"`
	Required bool    `yaml:"required" json:"required"`
}

// Action is one atomic capability (§3) — one handler, no internal sequence.
//
// Description and NotFor mirror phase 0's validated {what, not_for}
// criteria pattern (SPIKE.md, experiments/scripts/spike.py's CAPABILITIES
// dict) — four rounds of wording iteration found a plain one-line
// description underperforms this structured form for disambiguating
// confusable options (e.g. read_file vs. find_files vs. tail_log), the
// same pattern TypeSafe's own docs recommend for confusable Choice
// options. NotFor is optional: not every action has a confusable sibling
// worth calling out explicitly.
type Action struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"` // the "what"
	NotFor      string `yaml:"not_for" json:"not_for"`         // optional "not_for" — validated disambiguation text
	Danger      Danger `yaml:"danger" json:"danger"`
	View        View   `yaml:"view" json:"view"`
	Produces    string `yaml:"produces" json:"produces"` // pid | path | path[] | text | none
	Reducer     string `yaml:"reducer" json:"reducer"`   // registered reducer name, or "none"
	Args        []Arg  `yaml:"args" json:"args"`
}

// Domain is one capability source file (§3): a namespace, an
// availability predicate name, and the actions it contributes.
type Domain struct {
	Domain        string   `yaml:"domain" json:"domain"`
	Description   string   `yaml:"description" json:"description"`
	AvailableWhen string   `yaml:"available_when" json:"available_when"`
	Actions       []Action `yaml:"actions" json:"actions"`
}

// QualifiedName is the globally-namespaced form (§3) — "unix__read_file" —
// used everywhere except UI display, which uses Action.Name alone.
func QualifiedName(domain, action string) string {
	return domain + "__" + action
}
