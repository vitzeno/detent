package private

import "io/fs"

// Windows has no mode bits to read, and a profile's files are private by ACL.
func is(fs.FileInfo) bool { return true }
