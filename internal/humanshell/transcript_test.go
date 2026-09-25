package humanshell

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vitzeno/detent/event"
)

// The one thing here that cannot be changed later without
// invalidating every stored session, so it is pinned exactly rather
// than by substring.
func TestTranscribe_ReadsAsATerminalDoes(t *testing.T) {
	cases := []struct {
		name  string
		where string
		res   event.Result
		want  string
	}{
		{
			name: "a command that worked", where: "sandbox",
			res:  event.Result{Stdout: " M ui/keys.go\n"},
			want: "[human ran a command in the sandbox]\n$ git status\nexit 0\n M ui/keys.go",
		},
		{
			name: "the host, and a failure", where: "host",
			res:  event.Result{ExitCode: 1, Stderr: "no such file"},
			want: "[human ran a command on the host]\n$ git status\nexit 1\nno such file",
		},
		{
			name: "both streams, each ending as a command leaves it", where: "host",
			res:  event.Result{Stdout: "out\n", Stderr: "err\n"},
			want: "[human ran a command on the host]\n$ git status\nexit 0\nout\nerr",
		},
		{
			// Not "exit 0": it printed something and then stopped.
			name: "stopped part way", where: "sandbox",
			res:  event.Result{Stdout: "half of it\n", Err: stoppedByHuman},
			want: "[human ran a command in the sandbox]\n$ git status\ndid not finish: stopped by the human\nhalf of it",
		},
		{
			name: "nothing printed", where: "sandbox",
			res:  event.Result{ExitCode: 2},
			want: "[human ran a command in the sandbox]\n$ git status\nexit 2",
		},
		{
			name: "capped at capture", where: "sandbox",
			res:  event.Result{Stdout: "lots", Truncated: true},
			want: "[human ran a command in the sandbox]\n$ git status\nexit 0\nlots\n[output truncated at capture]",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, transcribe("git status", c.where, c.res))
		})
	}
}

// One loud command must not crowd out the transcript, which is resent
// whole every Step.
func TestTranscribe_BoundsOneLoudCommand(t *testing.T) {
	got := transcribe("yes", "host", event.Result{Stdout: strings.Repeat("y\n", 8000)})
	assert.LessOrEqual(t, len(got), maxNoteBytes+len("\n…[truncated]"))
	assert.True(t, strings.HasSuffix(got, "…[truncated]"))
	assert.Contains(t, got, "[human ran a command on the host]", "the lead line survives the cut")
}

// An unknown runner name reads as the host rather than as itself: the
// model must never be told it ran somewhere that does not exist.
func TestTranscribe_NamesOnlyTheTwoPlacesThereAre(t *testing.T) {
	assert.Contains(t, transcribe("ls", "", event.Result{}), "on the host")
	assert.Contains(t, transcribe("ls", "sandbox", event.Result{}), "in the sandbox")
}
