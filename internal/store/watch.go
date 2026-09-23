package store

import (
	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/logging"
)

// Watch writes every fact to the store, so a session can be replayed.
// Stopping waits for the last record to land, not merely to arrive.
func Watch(bus *event.Bus, s *Store, session uuid.UUID) func() {
	facts, unsub := bus.Subscribe(worthStoring)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var failed int
		for rec := range facts {
			if err := s.Append(session, rec); err != nil {
				if failed == 0 {
					logging.For(logging.Engine).Error("this session is not being recorded",
						logging.KeyReason, err.Error(), logging.KeyOrdinal, rec.Ordinal)
				}
				failed++
			}
		}
		if failed > 1 {
			// One line, not one per event: an unwritable database
			// fails every append, and a hundred identical errors say
			// no more than the first did.
			logging.For(logging.Engine).Error("records were lost",
				"lost", failed)
		}
	}()
	return func() {
		unsub()
		<-done
	}
}

// worthStoring takes the facts and skips live output. Replay has no
// use for it: a replayed Call has already finished, so its chunks
// have nothing to redraw, and CallEnded carries the whole output. A
// chatty command would also be thousands of rows.
func worthStoring(e event.Event) bool {
	return !e.Kind().IsIntent() && e.Kind() != event.OutputChunkKind
}
