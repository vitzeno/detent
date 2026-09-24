// Package headless runs one prompt on a terminal with no TUI. A bus
// subscriber like any front-end, which is what makes it a fair test.
package headless

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/vitzeno/detent/event"
)

// Run submits prompt, prints what happens, and returns when the Turn
// ends. approve decides the dangerous calls; nil reads stdin.
func Run(ctx context.Context, bus *event.Bus, prompt string, approve Approver) event.EndReason {
	if approve == nil {
		approve = Ask(os.Stdin, os.Stdout)
	}
	facts, unsub := bus.Subscribe(event.Facts())
	defer unsub()

	bus.Publish(event.SubmitPrompt{Text: prompt})
	for {
		select {
		case <-ctx.Done():
			return event.EndAborted
		case rec, ok := <-facts:
			if !ok {
				return event.EndError
			}
			if r, done := handle(bus, rec.Event, approve); done {
				return r
			}
		}
	}
}

// Approver answers one dangerous call.
type Approver func(event.ApprovalAsked) bool

// Ask reads y/n, and treats anything else as no. Unreadable stdin is a
// decline that says so: this is the gate for dangerous commands.
func Ask(in io.Reader, out io.Writer) Approver {
	r := bufio.NewReader(in)
	return func(a event.ApprovalAsked) bool {
		// The same rendering the row shows, rather than a raw map.
		fmt.Fprintf(out, "\n!! %s\n", event.Command(a.Tool, a.Args))
		if a.Rationale != "" {
			fmt.Fprintf(out, "   flagged: %s\n", a.Rationale)
		}
		fmt.Fprint(out, "[y] run   [n] skip: ")
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			fmt.Fprintf(out, "\ncould not read a decision (%v); skipping\n", err)
			return false
		}
		return strings.TrimSpace(strings.ToLower(line)) == "y"
	}
}

// AutoApprove says yes to everything. Only for a caller that has
// already decided the whole session is unattended.
func AutoApprove(event.ApprovalAsked) bool { return true }

// AutoDecline says no to everything, which is what an unattended run
// should do rather than run something flagged.
func AutoDecline(event.ApprovalAsked) bool { return false }

func handle(bus *event.Bus, ev event.Event, approve Approver) (event.EndReason, bool) {
	switch v := ev.(type) {
	case event.CallProposed:
		fmt.Printf("  → %s %s\n", v.Tool, args(v.Args))
	case event.CallEnded:
		fmt.Printf("    exit %d%s\n", v.Result.ExitCode, errSuffix(v.Result))
	case event.ModelText:
		fmt.Printf("\n%s\n", v.Text)
	case event.ApprovalAsked:
		bus.Publish(event.ResolveApproval{Call: v.Call, Approved: approve(v)})
	case event.BoundReached:
		// Unattended, the bound is where it stops.
		fmt.Printf("\nstopped after %d steps\n", v.Steps)
		bus.Publish(event.Continue{Turn: v.Turn, Approved: false})
	case event.Notice:
		fmt.Fprintf(os.Stderr, "detent: %s: %s\n", v.Level, v.Text)
	case event.TurnEnded:
		return v.Reason, true
	}
	return "", false
}

func args(a map[string]any) string {
	parts := make([]string, 0, len(a))
	for k, v := range a {
		s := fmt.Sprint(v)
		if len(s) > 60 {
			s = s[:60] + "…"
		}
		parts = append(parts, k+"="+s)
	}
	return strings.Join(parts, " ")
}

func errSuffix(r event.Result) string {
	if r.Err != "" {
		return " (" + r.Err + ")"
	}
	return ""
}
