package tool

import (
	"fmt"

	"github.com/vitzeno/detent/event"
)

// ListDir lists a directory. It lowers to a command so a sandboxed
// session sees the container's filesystem, not the host's.
type ListDir struct{}

func (ListDir) Name() string { return "list_dir" }

func (ListDir) Describe() Spec {
	return Spec{
		Description: "List a directory's contents.",
		Params: []Param{
			{Name: "path", Type: TypeString, Desc: "directory to list (default .)"},
			{Name: "all", Type: TypeBool, Desc: "include dotfiles"},
		},
		Mutability: event.MutRead,
	}
}

func (ListDir) Lower(a Args) (string, error) {
	path := a.String("path")
	if path == "" {
		path = "."
	}
	flags := "-l"
	if a.Bool("all", false) {
		flags = "-la"
	}
	return fmt.Sprintf("ls %s -- %s", flags, quote(path)), nil
}
