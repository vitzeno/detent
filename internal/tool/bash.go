package tool

import "errors"

// Bash is why the registry is an optimisation, not a boundary: a model
// that cannot say what it wants routes around you.
type Bash struct{}

func (Bash) Name() string { return "bash" }

func (Bash) Describe() Spec {
	return Spec{
		Description: "Run a shell command. Use a more specific tool when one fits: " +
			"they are cheaper, and their output is easier to read.",
		Params: []Param{
			{Name: "command", Type: TypeString, Desc: "the command to run", Required: true},
		},
		// Unknown on purpose. Only the hook chain can say what an
		// arbitrary command does.
		Mutability: "",
	}
}

func (Bash) Lower(a Args) (string, error) {
	cmd := a.String("command")
	if cmd == "" {
		return "", errors.New("command must not be empty")
	}
	return cmd, nil
}
