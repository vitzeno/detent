// Package version is what detent calls itself. It imports only the
// standard library, so anything may ask.
package version

import (
	"runtime/debug"
	"strings"
)

// Number is the release, set at build time for anything else with
// go build -ldflags "-X github.com/vitzeno/detent/version.Number=1.2.0"
var Number = "0.6.0"

// String is the number plus the build's revision when known, marked
// dirty so a modified tree never claims to be a release.
func String() string {
	info, _ := debug.ReadBuildInfo()
	return format(Number, info)
}

// format is String over a given build. A go install of a tagged module
// carries no revision but does carry its version, which beats Number.
func format(number string, info *debug.BuildInfo) string {
	rev, dirty := stamp(info)
	switch {
	case rev == "" && info != nil && released(info.Main.Version):
		return strings.TrimPrefix(info.Main.Version, "v")
	case rev == "":
		return number
	case dirty:
		return number + "+" + rev + "-dirty"
	}
	return number + "+" + rev
}

// stamp reads the revision the linker stamped in. Absent under go run
// and in tests, which is why String falls back to the number alone.
func stamp(info *debug.BuildInfo) (rev string, dirty bool) {
	if info == nil {
		return "", false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value[:min(7, len(s.Value))]
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return rev, dirty
}

// released is a plain vX.Y.Z, not "(devel)", a pseudo-version or a pre-release.
func released(v string) bool {
	return strings.HasPrefix(v, "v") && !strings.ContainsAny(v, "-+")
}
