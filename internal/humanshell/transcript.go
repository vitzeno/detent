package humanshell

import (
	"fmt"
	"strings"

	"github.com/vitzeno/detent/event"
)

// What the model reads. A user message, because the human ran this:
// a tool message answers a call, and there is no call to answer.

// Marker opens every one of these messages, and the system prompt
// names it so the model knows the shape. Exported to be asserted.
const Marker = "[human ran a command"

// maxNoteBytes matches engine.MaxResultBytes without importing it:
// one loud command must not crowd out the transcript.
const maxNoteBytes = 4 * 1024

// transcribe renders one command as a terminal shows it, under the
// bracketed line the transcript keeps for machinery, not speech.
func transcribe(command, where string, r event.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s]\n$ %s\n", Marker, location(where), command)
	// Not "exit 0": a cancelled or timed-out command never got to exit.
	if r.Err != "" {
		fmt.Fprintf(&b, "did not finish: %s", r.Err)
	} else {
		fmt.Fprintf(&b, "exit %d", r.ExitCode)
	}
	if out := strings.TrimRight(body(r), "\n"); out != "" {
		b.WriteString("\n" + out)
	}
	if r.Truncated {
		b.WriteString("\n[output truncated at capture]")
	}
	return bound(b.String())
}

// location says whose filesystem this was, because the model's own
// commands may go somewhere else entirely.
func location(where string) string {
	if where == "sandbox" {
		return "in the sandbox"
	}
	return "on the host"
}

func body(r event.Result) string {
	switch {
	case r.Stdout != "" && r.Stderr != "":
		return strings.TrimRight(r.Stdout, "\n") + "\n" + r.Stderr
	case r.Stderr != "":
		return r.Stderr
	}
	return r.Stdout
}

func bound(s string) string {
	if len(s) <= maxNoteBytes {
		return s
	}
	return s[:maxNoteBytes] + "\n…[truncated]"
}
