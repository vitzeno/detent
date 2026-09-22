package viewgen

import (
	"github.com/vitzeno/detent/views"
	"github.com/vitzeno/detent/viewspec"
)

// seed returns the spec detent ships for a command shape. The specs
// themselves live in views, beside the ones keyed by render kind,
// because the two had started to overlap and neither could see the
// other.
func seed(command string) (*viewspec.Spec, bool) {
	return views.ForCommand(Normalise(command))
}
