// Package version is what detent calls itself. It imports nothing, so
// anything may ask.
package version

import "runtime/debug"

// Number is the release. Set it at build time for anything else:
//
//	go build -ldflags "-X github.com/vitzeno/detent/version.Number=1.2.0"
var Number = "0.2.0"

// String is the number with the build it came from, where the build
// says anything: a binary built from a dirty tree is a different thing
// from a tagged release and should not claim to be one.
func String() string {
	rev, dirty := build()
	switch {
	case rev == "":
		return Number
	case dirty:
		return Number + "+" + rev + "-dirty"
	}
	return Number + "+" + rev
}

// build reads the revision the linker stamped in. Absent under go run
// and in tests, which is why String falls back to the number alone.
func build() (rev string, dirty bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) > 7 {
				rev = s.Value[:7]
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return rev, dirty
}
