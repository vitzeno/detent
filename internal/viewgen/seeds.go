package viewgen

import (
	"github.com/vitzeno/detent/views"
	"github.com/vitzeno/detent/viewspec"
)

// seed returns the spec detent ships for a command shape. The specs live
// in views, beside the ones keyed by render kind.
func seed(command string) (*viewspec.Spec, bool) {
	return views.ForCommand(Normalise(command))
}
