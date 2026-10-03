package engine

import (
	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

// Restore rebuilds a session from its stored facts, undo and reset included,
// never compacting. Call it before Run, or it races the Turn goroutine.
func (e *Engine) Restore(records []event.Record) {
	e.resumed = len(records)
	type begun struct{ n, mark int }
	turns := map[uuid.UUID]begun{}
	e.trLock(func() {
		for _, r := range records {
			switch v := r.Event.(type) {
			case event.Appended:
				e.tr.msgs = append(e.tr.msgs, v.Messages...)
			case event.TurnStarted:
				turns[v.Turn] = begun{n: v.N, mark: e.tr.mark()}
				e.turns = v.N
				e.tr.begin(v.N, v.Prompt)
			case event.RolledBack:
				if b, ok := turns[v.Turn]; ok && e.tr.truncate(b.mark) {
					e.turns = b.n - 1
				}
			case event.SessionReset:
				e.tr.reset()
				e.turns = 0
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
