package host

import "github.com/vitzeno/detent/internal/capture"

// Result, StreamEvent, and MaxOutputBytes are aliases onto
// internal/capture, which internal/sandbox also builds on. This
// package stays the single re-export point so every existing
// host.Result/host.StreamEvent reference elsewhere is unaffected.
type Result = capture.Result

type StreamEvent = capture.StreamEvent

const MaxOutputBytes = capture.MaxOutputBytes
