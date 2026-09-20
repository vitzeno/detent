package sandbox

import "github.com/vitzeno/detent/internal/capture"

// Option configures a Container.
type Option func(*Container)

func WithSocket(socket string) Option {
	return func(c *Container) { c.socket = socket }
}

func WithNamespace(ns string) Option {
	return func(c *Container) { c.namespace = ns }
}

func WithImage(ref string) Option {
	return func(c *Container) { c.image = ref }
}

// WithMountPoint sets the in-container path the harness's working
// directory is bind-mounted to. The host source is never
// configurable: it must match what fileio/ui/tree already operate on.
func WithMountPoint(path string) Option {
	return func(c *Container) { c.mountPoint = path }
}

func WithLimit(limit int) Option {
	return func(c *Container) { c.limit = limit }
}

// WithRuntime selects the OCI runtime handle (e.g.
// "io.containerd.runsc.v1" for gVisor); "" means containerd's own default.
func WithRuntime(name string) Option {
	return func(c *Container) { c.runtime = name }
}

// WithNetwork picks the posture: NetworkHost or NetworkNone.
func WithNetwork(mode string) Option {
	return func(c *Container) { c.network = mode }
}

// NewContainer builds a Container. Call Start before Run.
func NewContainer(opts ...Option) *Container {
	c := &Container{
		namespace:  DefaultNamespace,
		image:      DefaultImage,
		mountPoint: "/workspace",
		network:    DefaultNetwork,
		limit:      capture.MaxOutputBytes,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}
