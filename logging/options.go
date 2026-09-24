package logging

// Setup takes the session positionally and the rest as options, the
// way every other constructor here does.

// Option is one knob on Setup.
type Option func(*settings)

// WithDir holds one file per session. Without it, DefaultDir.
func WithDir(dir string) Option { return func(s *settings) { s.dir = dir } }

// WithLevel is "debug", "info", "warn" or "error".
func WithLevel(level string) Option { return func(s *settings) { s.level = level } }

// WithBodies allows prompts, replies and command output into the log.
// Off without it: they carry secrets and bulk.
func WithBodies(on bool) Option { return func(s *settings) { s.bodies = on } }

type settings struct {
	dir    string
	level  string
	bodies bool
}
