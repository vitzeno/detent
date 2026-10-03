package host

import "github.com/vitzeno/detent/internal/capture"

// Option configures a Shell.
type Option func(*Shell)

// NewShell builds a Shell, capping output at capture.MaxOutputBytes unless overridden.
func NewShell(opts ...Option) *Shell {
	s := &Shell{limit: capture.MaxOutputBytes}
	for _, o := range opts {
		o(s)
	}
	return s
}

// WithLimit caps the bytes captured from each of stdout and stderr.
func WithLimit(limit int) Option {
	return func(s *Shell) {
		s.limit = limit
	}
}
