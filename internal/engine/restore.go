package engine

import (
	"github.com/vitzeno/detent/event"
)

// Restore rebuilds a session from its stored facts, never compacting:
// a mark only resolves against a rebuild with no cut point.
func (e *Engine) Restore(records []event.Record) {
	e.resumed = len(records)
	e.trLock(func() {
		for _, r := range records {
			switch v := r.Event.(type) {
			case event.Appended:
				e.tr.msgs = append(e.tr.msgs, v.Messages...)
			case event.TurnStarted:
				e.turns = max(e.turns, v.N)
			case event.SessionStarted:
				e.session = v.Session
			}
		}
	})
}

// Resumable reports the last ordinal a session reached, which is what
// the bus has to continue from.
func Resumable(records []event.Record) uint64 {
	var last uint64
	for _, r := range records {
		last = max(last, r.Ordinal)
	}
	return last
}
