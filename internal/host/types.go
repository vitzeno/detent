package host

import "github.com/vitzeno/detent/internal/capture"

// Result is capture.Result, re-exported so callers need not import capture.
type Result = capture.Result

// StreamEvent is capture.StreamEvent.
type StreamEvent = capture.StreamEvent

// MaxOutputBytes is capture.MaxOutputBytes.
const MaxOutputBytes = capture.MaxOutputBytes
