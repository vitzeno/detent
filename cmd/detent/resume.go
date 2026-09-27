package main

import (
	"fmt"
	"strings"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/model"
)

// What a resumed session owes the model: the prompt says the
// filesystem carries every earlier step, and a resume makes that
// true only of the human's own directory.

// resumeNote says what did not come back. Each sentence is added
// only when it applies, so none of it is true of the wrong resume.
func resumeNote(records []event.Record, env model.Environment) string {
	var b strings.Builder
	b.WriteString(model.ResumeMarker + "\n")
	b.WriteString("The steps above ran in an earlier session. Do not assume what they set up is still here.\n")

	was, known := wasSandboxed(records)
	if known && was != env.Sandboxed {
		if env.Sandboxed {
			b.WriteString("They ran directly on the human's machine; this session runs in a container.\n")
		} else {
			b.WriteString("They ran in a container; this session runs directly on the human's machine.\n")
		}
	}
	if known && was {
		b.WriteString("That container is gone, so anything they installed or created inside it went with it.\n")
	}
	if env.Sandboxed {
		fmt.Fprintf(&b, "What persisted is %s, the human's own directory mounted in.\n", env.Dir)
	} else {
		fmt.Fprintf(&b, "What persisted is this machine's own filesystem, including %s.\n", env.Dir)
	}
	return b.String()
}

// wasSandboxed reads the earlier mode off the header fact already
// recorded. The last wins: a session resumed twice has two.
func wasSandboxed(records []event.Record) (was, known bool) {
	for i := len(records) - 1; i >= 0; i-- {
		if s, ok := records[i].Event.(event.SessionStarted); ok {
			return s.Sandbox, true
		}
	}
	return false, false
}
