package main

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/store"
)

// openSession picks a stored session to continue, or a new one, and
// returns the records the engine and UI rebuild themselves from.
func openSession(resume string) (uuid.UUID, []event.Record, error) {
	if resume == "" {
		return uuid.Must(uuid.NewV7()), nil, nil
	}
	events, err := store.Open(store.DefaultPath())
	if err != nil {
		return uuid.Nil, nil, err
	}
	defer func() { _ = events.Close() }()

	id, err := resolveSession(events, resume)
	if err != nil {
		return uuid.Nil, nil, err
	}
	records, err := events.Replay(id)
	if err != nil {
		return uuid.Nil, nil, err
	}
	if len(records) == 0 {
		return uuid.Nil, nil, fmt.Errorf("session %s has nothing recorded", id)
	}
	return id, records, nil
}

// resolveSession reads an id, then "last", then a name. store.Rename
// refuses a name shaped like the first two, since it would never be read.
func resolveSession(events *store.Store, want string) (uuid.UUID, error) {
	if id, err := uuid.Parse(want); err == nil {
		return id, nil
	}
	all, err := events.Sessions()
	if err != nil {
		return uuid.Nil, err
	}
	if len(all) == 0 {
		return uuid.Nil, fmt.Errorf("no sessions recorded yet")
	}
	if strings.EqualFold(want, store.ReservedName) {
		return all[0].ID, nil
	}
	for _, s := range all {
		if strings.EqualFold(s.Name, want) {
			return s.ID, nil
		}
	}
	return uuid.Nil, fmt.Errorf("no session named %q, try -sessions or -resume %s", want, store.ReservedName)
}

// announceResume marks the seam and says what did not come back.
// Both or neither: a boundary nothing explains is worse than none.
func announceResume(bus *event.Bus, session uuid.UUID, records []event.Record, env model.Environment) {
	if len(records) == 0 {
		return
	}
	bus.Publish(event.SessionResumed{
		Session: session, Records: len(records), Sandbox: env.Sandboxed})
	bus.Publish(event.NoteContext{Text: resumeNote(records, env)})
}

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
