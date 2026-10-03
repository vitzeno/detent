package store

import (
	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/logging"
)

// Watch records every fact and answers what ui cannot ask directly. One
// subscription, so a rename cannot overtake the header it updates.
func Watch(bus *event.Bus, s *Store, session uuid.UUID) func() {
	records, unsub := bus.Subscribe(wanted)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var failed int
		for rec := range records {
			if s.serve(bus, rec.Event) {
				continue
			}
			if err := s.Append(session, rec); err != nil {
				if failed == 0 {
					logging.For(logging.Store).Error("this session is not being recorded",
						logging.KeyReason, err.Error(), logging.KeyOrdinal, rec.Ordinal)
				}
				failed++
			}
		}
		if failed > 1 {
			logging.For(logging.Store).Error("records were lost", "lost", failed)
		}
	}()
	return func() {
		unsub()
		<-done
	}
}

// serve handles a question rather than a fact, and says whether it
// did. A rename replies with a fresh listing, so the asker sees it.
func (s *Store) serve(bus *event.Bus, e event.Event) bool {
	switch v := e.(type) {
	case event.RenameSession:
		// Answered either way, and said rather than logged: the human
		// just typed it, and only this knows whether it took.
		if err := s.Rename(v.Session, v.Name); err != nil {
			bus.Publish(event.Notice{Level: "error", Text: err.Error()})
			return true
		}
		bus.Publish(event.Notice{Level: "info", Text: "named " + v.Name})
	case event.ListSessions:
	default:
		return false
	}
	all, err := s.Sessions()
	if err != nil {
		logging.For(logging.Store).Error("could not list sessions", logging.KeyReason, err.Error())
		return true
	}
	bus.Publish(event.SessionsListed{Sessions: all})
	return true
}

// wanted is every fact bar live output, which CallEnded carries whole,
// plus the questions this package answers.
func wanted(e event.Event) bool {
	switch e.Kind() {
	case event.OutputChunkKind:
		return false
	case event.ListSessionsKind, event.RenameSessionKind:
		return true
	}
	return !e.Kind().IsIntent()
}
