package event

import "github.com/google/uuid"

// ID identifies a session, turn, step or call. UUIDv7, so it sorts by
// creation, and google/uuid's is monotonic within a millisecond as
// well: plain v7 only orders across them, and a Step mints all its
// Calls inside one.
type ID string

// NewID mints one. The error is unreachable: it only fires if
// crypto/rand does, which panics internally as of Go 1.24.
func NewID() ID {
	id, _ := uuid.NewV7()
	return ID(id.String())
}
