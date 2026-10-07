package store

import (
	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/logging"
)

// Watch records every fact and answers what ui cannot ask directly. One
// subscription, so a rename cannot overtake the header it updates.
func Watch(bus *event.Bus, s *Store, session uuid.UUID) func() {
	var failed int
	unhandle := bus.Handle(wanted, func(rec event.Record) {
		if s.serve(bus, rec.Event) {
			return
		}
		// /new starts another session, and its records, this first one
		// included, go under its id. The old session is left as it was.
		if v, ok := rec.Event.(event.SessionStarted); ok && v.Session != uuid.Nil {
			session = v.Session
		}
		if err := s.Append(session, rec); err != nil {
			if failed == 0 {
				logging.For(logging.Store).Error("this session is not being recorded",
					logging.KeyReason, err.Error(), logging.KeyOrdinal, rec.Ordinal)
				// Said once on screen too, since resume will be missing whatever follows.
				bus.Publish(event.Notice{Level: "warn",
					Text: "this session stopped being recorded, so resuming it will be incomplete: " + err.Error()})
			}
			failed++
		}
	})
	return func() {
		unhandle()
		if failed > 0 {
			logging.For(logging.Store).Error("records were lost", "lost", failed)
		}
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
	case event.LoadSession:
		records, err := s.Replay(v.Session)
		loaded := event.SessionLoaded{Session: v.Session, Records: records}
		if err != nil {
			loaded.Err = err.Error()
		}
		bus.Publish(loaded)
		return true
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

// wanted is every fact bar live output, which CallEnded carries whole, a listing,
// another session's records and a diff, plus the questions this package answers.
func wanted(e event.Event) bool {
	switch e.Kind() {
	case event.OutputChunkKind, event.SessionsListedKind, event.SessionLoadedKind, event.DiffLoadedKind:
		return false
	case event.ListSessionsKind, event.RenameSessionKind, event.LoadSessionKind:
		return true
	default:
		return !e.Kind().IsIntent()
	}
}
