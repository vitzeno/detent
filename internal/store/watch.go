package store

import (
	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/logging"
)

// Watch records every fact and answers the questions ui cannot ask
// directly, since it may not import this package. One subscription,
// not two: the bus delivers in order, so a rename cannot overtake the
// header it updates. Stopping waits for the last write.
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
					logging.For(logging.Engine).Error("this session is not being recorded",
						logging.KeyReason, err.Error(), logging.KeyOrdinal, rec.Ordinal)
				}
				failed++
			}
		}
		if failed > 1 {
			logging.For(logging.Engine).Error("records were lost", "lost", failed)
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
		if err := s.Rename(v.Session, v.Name); err != nil {
			logging.For(logging.Engine).Error("could not rename", logging.KeyReason, err.Error())
			return true
		}
	case event.ListSessions:
	default:
		return false
	}
	all, err := s.Sessions()
	if err != nil {
		logging.For(logging.Engine).Error("could not list sessions", logging.KeyReason, err.Error())
		return true
	}
	bus.Publish(event.SessionsListed{Sessions: all})
	return true
}

// wanted is every fact bar live output, plus the questions this
// package answers. A replayed Call has already finished, so its
// chunks have nothing to redraw and CallEnded carries the output.
func wanted(e event.Event) bool {
	switch e.Kind() {
	case event.OutputChunkKind:
		return false
	case event.ListSessionsKind, event.RenameSessionKind:
		return true
	}
	return !e.Kind().IsIntent()
}
