package sandbox

import (
	"maps"
)

// Option configures a Container.
type Option func(*Container)

// WithReadOnly mounts each host directory at its destination, unwritable
// from inside, as skills are.
func WithReadOnly(dirs map[string]string) Option {
	return func(c *Container) { c.readOnly = maps.Clone(dirs) }
}

// WithSocket sets the containerd socket path.
func WithSocket(socket string) Option {
	return func(c *Container) { c.socket = socket }
}

// WithNamespace sets the containerd namespace.
func WithNamespace(ns string) Option {
	return func(c *Container) { c.namespace = ns }
}

// WithImage sets the image reference, fully qualified.
func WithImage(ref string) Option {
	return func(c *Container) { c.image = ref }
}

// WithMountPoint sets where the working directory is mounted in the
// container. The host side is always the working directory itself.
func WithMountPoint(path string) Option {
	return func(c *Container) { c.mountPoint = path }
}

// WithLimit caps the bytes captured from each of stdout and stderr.
func WithLimit(limit int) Option {
	return func(c *Container) { c.limit = limit }
}

// WithRuntime selects the OCI runtime handle (e.g.
// "io.containerd.runsc.v1" for gVisor). Empty means runc.
func WithRuntime(name string) Option {
	return func(c *Container) { c.runtime = name }
}

// WithNetwork picks the posture: NetworkHost or NetworkNone. Start refuses anything else.
func WithNetwork(mode string) Option {
	return func(c *Container) { c.network = mode }
}
