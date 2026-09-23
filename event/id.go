package event

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// ID identifies a session, turn, step or call. UUIDv7: the leading 48
// bits are a millisecond timestamp, so IDs sort by creation and a log
// reads in order.
type ID string

// NewID mints one, monotonic even within a millisecond. Plain v7 is
// only ordered across milliseconds, and a Turn opening several Calls
// at once lands them all in the same one; the 12 bits after the
// version hold a counter so those still sort.
func NewID() ID {
	var b [16]byte
	ms, seq := tick()
	for i := range 6 {
		b[i] = byte(ms >> (40 - 8*i))
	}
	_, _ = rand.Read(b[6:]) // cannot fail as of Go 1.24
	b[6] = 0x70 | byte(seq>>8)
	b[7] = byte(seq)
	b[8] = b[8]&0x3f | 0x80 // variant 10
	return ID(format(b))
}

// counterBits is how many IDs one millisecond holds before borrowing
// from the next. 4096 is far more than a Turn can produce.
const counterBits = 12

var clock struct {
	sync.Mutex
	ms  uint64
	seq uint16
}

func tick() (uint64, uint16) {
	clock.Lock()
	defer clock.Unlock()
	now := uint64(time.Now().UnixMilli())
	switch {
	case now > clock.ms:
		clock.ms, clock.seq = now, 0
	case clock.seq < 1<<counterBits-1:
		clock.seq++
	default:
		// Out of counter: take the next millisecond early rather than
		// mint an id that sorts wrong.
		clock.ms++
		clock.seq = 0
	}
	return clock.ms, clock.seq
}

func format(b [16]byte) string {
	var out [36]byte
	hex.Encode(out[:8], b[0:4])
	hex.Encode(out[9:13], b[4:6])
	hex.Encode(out[14:18], b[6:8])
	hex.Encode(out[19:23], b[8:10])
	hex.Encode(out[24:], b[10:])
	out[8], out[13], out[18], out[23] = '-', '-', '-', '-'
	return string(out[:])
}
