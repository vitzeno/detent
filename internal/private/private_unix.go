//go:build !windows

package private

import "io/fs"

func is(info fs.FileInfo) bool { return info.Mode().Perm()&0o077 == 0 }
