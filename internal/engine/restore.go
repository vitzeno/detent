package engine

import (
	"github.com/vitzeno/detent/event"
)

// Restore rebuilds a session from its stored facts: the transcript
// from what was appended, and the Turn count so the next one is
// numbered where the last left off.
//
// It never compacts. A mark is only valid against the compaction it
// was taken under, so a rebuild with no cut point is the one that
// resolves the marks a stored Turn still holds.
func (e *Engine) Restore(records []event.Record) {
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
