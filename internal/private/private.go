// Package private says whether only a file's owner can reach it, as ssh
// asks of a key before trusting what is in it.
package private

import "io/fs"

// Is reports whether info grants nothing to group or other.
func Is(info fs.FileInfo) bool { return is(info) }
