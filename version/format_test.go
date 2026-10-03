package version

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormat(t *testing.T) {
	vcs := func(rev, modified string) []debug.BuildSetting {
		return []debug.BuildSetting{{Key: "vcs.revision", Value: rev}, {Key: "vcs.modified", Value: modified}}
	}
	tests := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{"no build info", nil, "1.0.0"},
		{"go run", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "1.0.0"},
		{"a clean checkout", &debug.BuildInfo{Settings: vcs("23d325e0123456", "false")}, "1.0.0+23d325e"},
		{"a modified checkout", &debug.BuildInfo{Settings: vcs("23d325e0123456", "true")}, "1.0.0+23d325e-dirty"},
		{"a short revision is kept", &debug.BuildInfo{Settings: vcs("abc", "false")}, "1.0.0+abc"},
		{"go install of a tag", &debug.BuildInfo{Main: debug.Module{Version: "v0.3.0"}}, "0.3.0"},
		{"a checkout's pseudo-version loses to the number", &debug.BuildInfo{
			Main:     debug.Module{Version: "v0.0.0-20261003120000-23d325e01234+dirty"},
			Settings: vcs("23d325e0123456", "true")}, "1.0.0+23d325e-dirty"},
		{"a pseudo-version with no revision", &debug.BuildInfo{
			Main: debug.Module{Version: "v0.0.0-20261003120000-23d325e01234"}}, "1.0.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, format("1.0.0", tt.info))
		})
	}
}
