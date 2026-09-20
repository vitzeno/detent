package sandbox

import (
	"context"
	"fmt"

	containerd "github.com/containerd/containerd"
)

// Preflight checks that a containerd daemon is reachable at socket,
// without creating anything. Meant to run once at startup, so a bad
// socket fails loud immediately rather than on the first command.
func Preflight(ctx context.Context, socket string) error {
	client, err := containerd.New(socket)
	if err != nil {
		return fmt.Errorf("sandbox: connect %s: %w", socket, err)
	}
	defer client.Close()
	if _, err := client.Version(ctx); err != nil {
		return fmt.Errorf("sandbox: %s unreachable: %w", socket, err)
	}
	return nil
}
