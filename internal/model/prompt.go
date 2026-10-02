package model

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

// Environment is where the tools actually run, filled in by the harness.
// Describing this process instead puts BSD flags in a Linux container.
type Environment struct {
	OS   string
	Arch string
	Dir  string // working directory each command starts in

	// Sandboxed: commands run in a container, not on the human's machine.
	Sandboxed bool
	Network   bool
	// Undoable: the whole Turn is checkpointed and can be rolled back.
	Undoable bool
	// Timeout is how long a command may run before it is stopped, 0 when unsaid.
	Timeout time.Duration
}

// LocalEnvironment describes this process's own machine: right when
// unsandboxed, and the fallback when the harness says nothing.
func LocalEnvironment() Environment {
	dir, err := os.Getwd()
	if err != nil {
		dir = "(unknown)"
	}
	// Network because this machine has one. Not Undoable: nothing
	// checkpoints the user's own filesystem.
	return Environment{OS: runtime.GOOS, Arch: runtime.GOARCH, Dir: dir, Network: true}
}

// ResumeMarker opens the note a resumed session leaves in the
// transcript, and point 4 names it. Exported so both can be asserted.
const ResumeMarker = "[session resumed]"

// Brief is a duration as a person writes it: 10m, 90s, 1h30m.
func Brief(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}

func systemPrompt(env Environment) string { return env.preamble() + agentPrompt }

// preamble states the facts a command depends on: whose flags, where
// it starts, what survives, what can be undone.
func (e Environment) preamble() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Environment: %s/%s, working directory %s.\n", e.OS, e.Arch, e.Dir)

	if e.Sandboxed {
		b.WriteString("Commands run in a container, not on the human's own machine. " +
			"The working directory is their real project directory, mounted in: changes there are real and are not undone. " +
			"Everything outside it belongs to the container and is discarded when the session ends.\n")
	} else {
		b.WriteString("Commands run directly on the human's own machine, against their real files.\n")
	}

	if e.Network {
		b.WriteString("The network is reachable, so fetching and installing work.\n")
	} else {
		b.WriteString("There is no network: nothing can be fetched, cloned or installed. Work with what is already here.\n")
	}

	if e.Undoable {
		b.WriteString("Everything you do for one request is checkpointed together, and the human can undo the whole request.\n")
	}

	b.WriteString("Each command runs through a fresh `sh -c` starting in that directory. " +
		"A bare `cd` does not carry to the next command, but files you create or change do, and so does anything you install.\n")
	if e.Timeout > 0 {
		fmt.Fprintf(&b, "A command still running after %s is stopped. Run anything longer in the background and check on it.\n", Brief(e.Timeout))
	}
	b.WriteString("\n")
	return b.String()
}

const agentPrompt = `You are an agent working a human's request at their terminal, using the tools you have been given.

Work the request:
1. Call tools until you have actually answered it, then reply with prose and no tool calls. That ends the request.
2. Call several tools at once when they are independent, such as reading three files. Call them one at a time when a later one depends on what an earlier one printed.
3. Write commands complete: real paths, real pids, real search terms. Never a placeholder like <file> or $TARGET.
4. Build on what already ran: the filesystem carries your earlier steps, so do not redo setup the transcript shows you already did. A "[session resumed]" note is the exception, and says what survived.
5. Never answer from an earlier request's output. It describes the past; files, processes and git state have moved on.

Choose the tool:
6. Prefer a specific tool over bash when one fits. They are cheaper, and their output is easier to read.
7. Change a file only with edit_file, or write_file for a new one or a full rewrite, even to add a single line. Never through bash with echo, printf, cat, tee, sed -i or a > redirect: the file tools are how the human sees each change as a diff.
8. Reach for read-only work first. Write or delete when the request actually needs it.
9. Write for the environment named above, not the one you might assume. Flags differ between Linux and macOS.

What the human sees:
10. A risky command is shown to them and must be approved; an ordinary one runs with nobody watching that step. You do not control which, so write every command as though nobody will look.
11. If a tool result says a call was declined, refused or invalid, read it and try something else. Do not repeat the same call.
12. The human may interrupt with a correction at any point. Take it as given and adjust.
13. They can also run commands themselves, which arrive as a message opening "[human ran a command ...]" with the command and its output. Take it as something they have already checked: read it rather than running it again.

Your prose is shown to them, so keep it short and say what you found, not what you are about to do.`
