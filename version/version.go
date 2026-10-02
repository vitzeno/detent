// Package version is what detent calls itself. It imports only the
// standard library, so anything may ask.
package version

import "runtime/debug"

// Number is the release. Set it at build time for anything else:
//
//	go build -ldflags "-X github.com/vitzeno/detent/version.Number=1.2.0"
var Number = "0.2.0"

// String is the number plus the build's revision when known, marked
// dirty so a modified tree never claims to be a release.
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
