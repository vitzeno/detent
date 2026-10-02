// Package tool is the closed set of things a model may call. A tool
// lowers to one shell command, so the sandbox stays the only executor
// of them. An MCP tool has none, and internal/mcp answers it instead.
package tool

// Tool is a typed front end onto a shell command. Pure: no tool runs
// anything, so the Runner stays the one place with I/O.
type Tool interface {
	Name() string
	Describe() Spec
	// Lower turns validated args into a command. An error here becomes
	// a tool result, never a failed Turn.
	Lower(args Args) (string, error)
}

// Spec is what the model is told about a tool, and what the risk chain
// starts from.
type Spec struct {
	Description string
	Params      []Param
	// Mutability floors the risk chain without a model call. Empty
	// means unknown, which is bash and only bash.
	Mutability string
	// Renders says how to read the output. Empty leaves it to the judge.
	Renders string
	// Executor names what runs this call, empty being a shell command.
	Executor string
	// Raw is a schema detent did not build, handed to the model as
	// given. Set when the parameters are not Param's small subset.
	Raw map[string]any
	// Group is the row /context counts this tool under, empty for "tools".
	// GroupDetail, when set, says what the group holds instead of a count.
	Group       string
	GroupDetail string
}

// Param is one argument, in the subset of JSON Schema every endpoint agrees on.
type Param struct {
	Name     string
	Type     string // string, integer, boolean
	Desc     string
	Required bool
	// Enum, when set, is every value the parameter may take.
	Enum []string
}

// Args are one call's arguments, already validated against the Spec.
type Args map[string]any

// String reads a validated string param, or "" when absent.
func (a Args) String(name string) string {
	s, _ := a[name].(string)
	return s
}

// Int reads a validated integer param, or def when absent.
func (a Args) Int(name string, def int) int {
	switch v := a[name].(type) {
	case int:
		return v
	case float64: // JSON numbers decode to float64
		return int(v)
	}
	return def
}

// Bool reads a validated boolean param, or def when absent.
func (a Args) Bool(name string, def bool) bool {
	if b, ok := a[name].(bool); ok {
		return b
	}
	return def
}

// The parameter types a Spec may use.
const (
	TypeString = "string"
	TypeInt    = "integer"
	TypeBool   = "boolean"
)
