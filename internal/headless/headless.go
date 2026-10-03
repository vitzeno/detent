// Package headless runs one prompt on a terminal with no TUI. A bus
// subscriber like any front-end, which is what makes it a fair test.
// What happened goes to out, notices go to errOut.
package headless

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/termsafe"
)

// Approver answers one dangerous call.
type Approver func(event.ApprovalAsked) bool

// Printer is one headless Turn, subscribed from New so nothing published
// before Run is missed.
type Printer struct {
	bus     *event.Bus
	facts   <-chan event.Record
	unsub   func()
	approve Approver
	out     io.Writer
	errOut  io.Writer
}

// New subscribes to facts. approve decides the dangerous calls, and nil
// reads stdin.
func New(bus *event.Bus, approve Approver, out, errOut io.Writer) *Printer {
	if approve == nil {
		approve = Ask(os.Stdin, out)
	}
	facts, unsub := bus.Subscribe(event.Facts())
	return &Printer{bus: bus, facts: facts, unsub: unsub, approve: approve, out: out, errOut: errOut}
}

// Run submits prompt, prints what happens, and returns when the Turn ends.
func (p *Printer) Run(ctx context.Context, prompt string) event.EndReason {
	defer p.unsub()
	p.bus.Publish(event.SubmitPrompt{Text: prompt})
	for {
		select {
		case <-ctx.Done():
			return event.EndAborted
		case rec, ok := <-p.facts:
			if !ok {
				return event.EndError
			}
			if r, done := p.handle(rec.Event); done {
				return r
			}
		}
	}
}

// Ask reads y/n, and treats anything else as no. Unreadable or closed
// stdin is a decline that says so: this is the gate for dangerous commands.
func Ask(in io.Reader, out io.Writer) Approver {
	r := bufio.NewReader(in)
	return func(a event.ApprovalAsked) bool {
		// The same rendering the row shows, rather than a raw map.
		fmt.Fprintf(out, "\n!! %s\n", termsafe.Printable(event.Command(a.Tool, a.Args)))
		if a.Rationale != "" {
			fmt.Fprintf(out, "   flagged: %s\n", termsafe.Printable(a.Rationale))
		}
		fmt.Fprint(out, "[y] run   [n] skip: ")
		line, err := r.ReadString('\n')
		switch {
		case errors.Is(err, io.EOF) && line == "":
			fmt.Fprintln(out, "\nstdin is closed; skipping (use -unattended or -approve-all)")
			return false
		case err != nil && !errors.Is(err, io.EOF):
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

func (p *Printer) handle(ev event.Event) (event.EndReason, bool) {
	switch v := ev.(type) {
	case event.SessionStarted:
		where := "host"
		if v.Sandbox {
			where = "sandbox"
		}
		fmt.Fprintf(p.errOut, "detent: model %s, commands run on the %s\n", termsafe.Printable(v.Model), where)
	case event.ToolCallProposed:
		fmt.Fprintf(p.out, "  → %s\n", clip(event.Command(v.Tool, v.Args), 120))
	case event.ToolCallEnded:
		fmt.Fprintln(p.out, "    "+termsafe.Printable(outcome(v.Result)))
	case event.ModelText:
		fmt.Fprintf(p.out, "\n%s\n", termsafe.Printable(v.Text))
	case event.ApprovalAsked:
		p.bus.Publish(event.ResolveApproval{ToolCall: v.ToolCall, Approved: p.approve(v)})
	case event.BoundReached:
		// Unattended, the bound is where it stops.
		fmt.Fprintf(p.out, "\nstopped after %d steps\n", v.Steps)
		p.bus.Publish(event.Continue{Turn: v.Turn, Approved: false})
	case event.Notice:
		fmt.Fprintf(p.errOut, "detent: %s: %s\n", v.Level, termsafe.Printable(v.Text))
	case event.TurnEnded:
		return v.Reason, true
	}
	return "", false
}

// clip keeps a tool call to one line of at most n runes, defused.
func clip(s string, n int) string {
	first, _, more := strings.Cut(s, "\n")
	first = termsafe.Printable(first)
	r := []rune(first)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	if more {
		return first + " …"
	}
	return first
}

// outcome is how a tool call ended: its exit code, or why it has none.
func outcome(r event.Result) string {
	if r.Err != "" {
		return r.Err
	}
	return fmt.Sprintf("exit %d", r.ExitCode)
}
