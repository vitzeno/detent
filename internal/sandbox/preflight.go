package sandbox

import (
	"context"
	"fmt"

	containerd "github.com/containerd/containerd"
)

// Preflight checks that a containerd daemon answers at socket, so a bad
// socket fails at startup rather than on the first command.
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
