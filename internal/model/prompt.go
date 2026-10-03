package model

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

// The shells Environment.Shell names.
const (
	ShellSh      = "sh"
	ShellPwsh    = "pwsh"
	ShellGitBash = "gitbash"
)

// ResumeMarker opens the note a resumed session leaves in the
// transcript, and point 4 of the prompt names it.
const ResumeMarker = "[session resumed]"

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
	// Shell is what commands run in, named as host_shell names it. Empty is sh.
	Shell string
}

// LocalEnvironment describes this process's own machine: right when
// unsandboxed, and the fallback when the harness says nothing.
func LocalEnvironment() Environment {
	dir, err := os.Getwd()
	if err != nil {
		dir = "(unknown)"
	}
	// Network because this machine has one. Not Undoable: reverting the
	// human's files is their choice at undo time, so the model treats edits as real.
	return Environment{OS: runtime.GOOS, Arch: runtime.GOARCH, Dir: dir, Network: true}
}

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

func systemPrompt(env Environment) string { return env.preamble() + env.rules() }

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

	b.WriteString(e.shellLine() +
		"A bare `cd` does not carry to the next command, but files you create or change do, and so does anything you install.\n")
	if e.Timeout > 0 {
		fmt.Fprintf(&b, "A command still running after %s is stopped. Run anything longer in the background and check on it.\n", Brief(e.Timeout))
	}
	b.WriteString("\n")
	return b.String()
}

// shellLine says what each command runs through.
func (e Environment) shellLine() string {
	switch e.Shell {
	case ShellPwsh:
		return "Each command runs through a fresh PowerShell 7 (`pwsh`) starting in that directory, so write PowerShell, not POSIX sh. "
	case ShellGitBash:
		return "Each command runs through a fresh Git Bash `bash -c` starting in that directory, on Windows. "
	}
	return "Each command runs through a fresh `sh -c` starting in that directory. "
}

// pwshRules are rules 9 and 10 for PowerShell, which names its own tool and its own ways to write a file.
var pwshRules = strings.NewReplacer(
	"over bash when", "over powershell when",
	"Never through bash with echo, printf, cat, tee, sed -i or a > redirect",
	"Never through powershell with Set-Content, Add-Content, Out-File, New-Item or a > redirect",
)

func (e Environment) rules() string {
	if e.Shell == ShellPwsh {
		return pwshRules.Replace(agentPrompt)
	}
	return agentPrompt
}

const agentPrompt = `You are an agent working a human's request at their terminal, using the tools you have been given.

Work the request:
1. Call tools until you have actually answered it, then reply with prose and no tool calls. That ends the request.
2. Call several tools at once when they are independent, such as reading three files. Call them one at a time when a later one depends on what an earlier one printed.
3. Write commands complete: real paths, real pids, real search terms. Never a placeholder like <file> or $TARGET.
4. Build on what already ran: the filesystem carries your earlier steps, so do not redo setup the transcript shows you already did. A "` + ResumeMarker + `" note is the exception, and says what survived.
5. Never answer from an earlier request's output. It describes the past; files, processes and git state have moved on.
6. Check the result against the request itself before you finish, not against your own idea of it. Re-read what was asked, then run, test or measure each part. A test you wrote yourself only proves your own reading.
7. When the request sets a target, such as a score, a speed or an exact output, keep working until it is met or you have run out of real approaches. A near miss is not done, so say how far short it fell.
8. When the request asks for a file or an output, write a first version early and improve it, so something useful exists if you are stopped.

Choose the tool:
9. Prefer a specific tool over bash when one fits. They are cheaper, and their output is easier to read.
10. Change a file only with edit_file, or write_file for a new one or a full rewrite, even to add a single line. Never through bash with echo, printf, cat, tee, sed -i or a > redirect: the file tools are how the human sees each change as a diff.
11. Reach for read-only work first. Write or delete when the request actually needs it. Copy a file before inspecting it with a tool that might change it, such as a database or an archive.
12. Write for the environment named above, not the one you might assume. Flags differ between Linux and macOS.

What the human sees:
13. A risky command is shown to them and must be approved; an ordinary one runs with nobody watching that step. You do not control which, so write every command as though nobody will look.
14. If a tool result says a call was declined, refused or invalid, read it and try something else. Do not repeat the same call.
15. The human may interrupt with a correction at any point. Take it as given and adjust.
16. They can also run commands themselves, which arrive as a message opening "[human ran a command ...]" with the command and its output. Take it as something they have already checked: read it rather than running it again.
17. Never present guessed or made-up data as a result. If you could not get something, say so plainly.

Your prose is shown to them, so keep it short and say what you found, not what you are about to do.`
