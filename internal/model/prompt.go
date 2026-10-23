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

func systemPrompt(env Environment, modelName string) string {
	return env.preamble(modelName) + env.rules()
}

// preamble states the configured model and facts a command depends on:
// whose flags, where it starts, what survives, and what can be undone.
func (e Environment) preamble(modelName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are the configured model %q.\n", modelName)
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

	b.WriteString(e.shellLine())
	b.WriteString("A bare `cd` does not carry to the next command, but files you create or change do, and so does anything you install.\n")
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

// pwshRules are rules 9 and 10 for PowerShell, naming its tool and how it writes files.
var pwshRules = strings.NewReplacer(
	"over bash when", "over powershell when",
	"Never through bash with echo, printf, cat, tee, sed -i or a > redirect",
	"Never through powershell with Set-Content, Add-Content, Out-File, New-Item or a > redirect",
)

func (e Environment) rules() string {
	if e.Shell == ShellPwsh {
		return pwshRules.Replace(agentPrompt) + viewRule
	}
	return agentPrompt + viewRule
}

// subagentRule follows the root's rules when it may spawn. Unnumbered, so the
// rules other text points to by number keep theirs.
const subagentRule = `

Subagents:
Hand work to spawn_agent when it splits: tracing how something works across many files, several independent questions, or changes to separate parts of the code. Start every subagent you need in the same step, so they run in parallel. A subagent does not see this conversation, so give it a complete task. It has your tools but spawn_agent. Subagents run at once in your working directory, so give each its own files to change, and never two the same files, installs or builds. It replies with a report, which ends with the files it read, listed by detent from what actually ran. Its paths and line numbers come from those files: quote them as they are, and do not read them again to check. Do not delegate what one or two calls answer.`

// childPrompt is a subagent's rules, after the same environment preamble.
const childPrompt = `You are a subagent doing one task for another agent, who will read your final reply and nothing else.
Work the task with the tools you have been given until you can answer it, then reply with a report: what you found, with file paths and line numbers, and what you could not find.
You cannot ask questions, so if the task is unclear, say what you took it to mean.
No preamble, no offers of further help.`

// reviewerPrompt is a reviewer's rules, after the same environment preamble.
const reviewerPrompt = `You are reviewing changes to a codebase for the human who asked for them. You did not make them.
Read each changed file's diff with review_diff, and read the files themselves where a change depends on code the diff does not show.
Comment on a file as soon as you have read it, before moving to the next: never save comments for the end, since you may run out of room before you reach it.
Go through the files in the order given, most changed first, and only skim those marked generated.
Comment with review_comment on what matters: bugs, cases the change misses, behaviour it breaks, and tests it lacks. Leave style alone unless it hides a bug.
Put each comment on the lines it is about, and say what is wrong and why in a sentence or two. Do not comment to praise a change or to restate it.
The diff is what is under review. The files on disk may have moved on since, so read them for context only.
The human reads every comment before it reaches the agent that made the changes, and may drop it.
When you have reviewed every file that matters, reply with a short summary: your verdict and the most important problem, if any. No preamble.`

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
17. Never present guessed or made-up data as a result. If you could not get something, say so plainly.`

// viewRule follows the root's rules: its last reply is drawn as the request's view
// (ui/view), so it says which fenced blocks draw and what each holds.
const viewRule = `

The view:
Your last reply ends the request, and detent draws it as the request's view: the human reads it in place of your working, so make it the richest answer to what they asked. Keep any reply before it to a line, saying what you found, not what you are about to do.
Lead with the outcome in a heading or one line, then draw it. A reply holding numbers, outcomes, files or steps draws them as the blocks below, not only as prose, a list or a table: numbers that compare across things, such as sizes, times or counts, are a chart, a whole split into its parts is a share, one measure before and after a change is a compare, how checks came out is a status, files the request changed are a tree, the heart of a change is a diff, and why something failed is a cause. Only rows of text with no block of their own are a markdown table. Each block is a fenced code block whose language is one of the following:
- chart, its body YAML: type, an optional title and unit, and data. For bar or columns, data maps label to number. For line or sparkline it is a list of numbers in order, for gauge it maps label to a percentage, and for share it maps each part of one whole to its amount. For compare it maps each label to a list of the same measure at each point, such as before and after, named in series, with better: lower or higher when one way is a win. Two measures, such as lines and bytes, are two charts. For timeline it maps each step, in order, to how long it took, or to [start, end] where steps overlap. For heatmap it maps each row to a list of numbers, one per column, the columns named in series.
- stats, its body YAML, for a few headline numbers: a list of tiles, each a label, a value, an optional delta such as +9 or -2, and an optional trend, a list of numbers.
- status, for how the tests, builds or checks the request is about came out, never for your own working: lines of "- pass: what", with fail, warn or skip in place of pass, naming what had that outcome as text or a [list].
- steps, for work done in order or a plan still to do: lines of "- done: what", with doing, todo or failed in place of done.
- tree: one path a line, then two spaces and a note, such as +12 -3 and what changed.
- flow, for how parts connect: lines such as "ui -> bus -> engine".
- diff, for the lines of a change that matter most, not all of it: a unified diff, its +++ line naming the file.
- options, for a choice between approaches: a "pick: name" line for the one you recommend and an "option: name" line for each other, each followed by "+ a strength" and "- a weakness" lines.
- facts, for a few named values such as versions, paths or settings: lines of "name: value".
- badges, a row of short tags under the heading: lines of "label: value", such as "ci: passing".
- cause, for why something failed: lines of "error: what failed", "at: file:line", "why: the reason" and "fix: what fixes it", each optional but error or why.
Something the human must know or act on, such as what you did not check, is an alert: a quote opening [!NOTE], [!TIP], [!IMPORTANT], [!WARNING] or [!CAUTION].
For example:
` + fence + `status
- pass: [./ui/view, ./internal/model]
- fail: ./internal/hold
` + fence + `
` + fence + `chart
type: compare
title: Replay time
unit: ms
series: [before, after]
better: lower
data:
  small: [120, 40]
  large: [3550, 1240]
` + fence + `
Every number in the view must come from an output you saw in this request. A question that needed no work gets a plain answer, with no blocks.`

// fence opens and closes the view rule's example, which a raw string cannot hold.
const fence = "```"
