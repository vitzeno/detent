package tool

import "errors"

// Bash is why the registry is an optimisation, not a boundary: a model
// that cannot say what it wants routes around you.
type Bash struct{}

func (Bash) Name() string { return "bash" }

func (Bash) Describe() Spec {
	return Spec{
		Description: "Run a shell command: build, test, run, install, git. Not for files: " +
			"read with read_file, search with grep and find_files, and change a file only with " +
			"edit_file or write_file, never echo, printf, cat, tee, sed -i or a > redirect, " +
			"since those two are how the human sees what changed.",
		Params: []Param{
			{Name: "command", Type: TypeString, Desc: "the command to run", Required: true},
		},
		// Unknown on purpose. Only the hook chain can say what an
		// arbitrary command does.
		Mutability: "",
	}
}

// Lower is the command as given.
func (Bash) Lower(a Args) (string, error) {
	cmd := a.String("command")
	if cmd == "" {
		return "", errors.New("command must not be empty")
	}
	return cmd, nil
}
