package viewgen

import (
	"fmt"
	"strings"
)

// promptLines caps what the model sees. Enough to recognise a shape,
// far short of enough to transcribe it — which is the point.
const promptLines = 40

const systemPrompt = `You design how a terminal pane shows one command's output.

You are given a command and what it printed. Reply with one view specification: a parse saying how to READ the output into rows, and blocks saying how to DRAW what was read.

The rule that decides everything else: you describe HOW TO READ the bytes, never WHAT THEY SAY. The program runs your parse against the real output. A pattern can point at the wrong place, and that is recoverable. A value you typed in yourself is a fabrication the human cannot tell from a real one.

So:
1. Never put output values in the spec. No package names, no counts, no paths, no statuses. Titles are labels like "passed", never "3 of 4 passed" — the program counts, you do not.
2. Write the parse against the lines you were shown, but for the command in general. The next run prints different values in the same shape.
3. A lines pattern needs named captures: ^(?P<status>ok|FAIL)\s+(?P<pkg>\S+). Go's regexp, no lookahead.
4. Prefer the parse that matches the shape exactly over one that matches everything. A pattern that captures whole lines tells the drawing nothing.
5. Choose blocks from the guide in the schema. Read each kind's not_for: it names the one it is most often confused with.
6. Two or three blocks is usually right. A summary above the detail, or a row putting them side by side.
7. Set on_enter only where a row names something worth acting on, as a read-only command using {field}. It is offered to the human to edit, never run.

Reply with the JSON object only.`

// userPrompt states what ran, how it ended and what it printed.
func userPrompt(req Request) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Command:\n%s\n\nExit code: %d\n\n", req.Command, req.ExitCode)
	lines := strings.Split(strings.TrimSuffix(req.Output, "\n"), "\n")
	truncated := false
	if len(lines) > promptLines {
		lines, truncated = lines[:promptLines], true
	}
	b.WriteString("Output:\n")
	b.WriteString(strings.Join(lines, "\n"))
	if truncated {
		b.WriteString("\n…(more lines followed; the shape is what matters)")
	}
	return b.String()
}
