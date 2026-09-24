package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strings"

	containerd "github.com/containerd/containerd"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/leases"
)

// Prune removes what abandoned sessions left in containerd. Anything
// without a running task goes: starting a session clears its own
// leftovers, so an idle container can never be adopted.
func Prune(ctx context.Context, socket, namespace string) (Pruned, error) {
	client, err := containerd.New(socket, containerd.WithDefaultNamespace(namespace))
	if err != nil {
		return Pruned{}, fmt.Errorf("sandbox: connect %s: %w", socket, err)
	}
	defer client.Close()

	var out Pruned
	conts, err := client.Containers(ctx)
	if err != nil {
		return out, fmt.Errorf("sandbox: list containers: %w", err)
	}
	for _, cont := range conts {
		id, ours := strings.CutPrefix(cont.ID(), containerPrefix)
		if !ours {
			continue
		}
		switch err := dropContainer(ctx, cont, id); {
		case errors.Is(err, ErrSessionLive):
			out.Kept = append(out.Kept, id)
		case err != nil:
			return out, err
		default:
			out.Containers = append(out.Containers, id)
		}
	}
	return out, pruneLeases(ctx, client, &out)
}

// Pruned is what a prune removed, by session id, and what it left
// alone because something was still running in it.
type Pruned struct {
	Containers []string
	Leases     []string
	Kept       []string
}

func (p Pruned) Empty() bool { return len(p.Containers) == 0 && len(p.Leases) == 0 }

// pruneLeases drops a checkpoint lease whose container is gone. The
// snapshots it rooted went with that container, so it roots nothing.
func pruneLeases(ctx context.Context, client *containerd.Client, out *Pruned) error {
	all, err := client.LeasesService().List(ctx)
	if err != nil {
		return fmt.Errorf("sandbox: list leases: %w", err)
	}
	for _, l := range all {
		id, ours := strings.CutPrefix(l.ID, containerPrefix)
		id, checkpoints := strings.CutSuffix(id, leaseSuffix)
		if !ours || !checkpoints {
			continue
		}
		if _, err := client.LoadContainer(ctx, containerID(id)); err == nil {
			continue
		} else if !errdefs.IsNotFound(err) {
			return fmt.Errorf("sandbox: look for %s: %w", containerID(id), err)
		}
		if err := client.LeasesService().Delete(ctx, leases.Lease{ID: l.ID}); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("sandbox: delete lease %s: %w", l.ID, err)
		}
		out.Leases = append(out.Leases, id)
	}
	return nil
}

// Forget removes one session's container, snapshot and lease, so a
// session being deleted does not leave a container nothing will
// resume. Refuses one whose task is still running.
func Forget(ctx context.Context, socket, namespace, sessionID string) error {
	client, err := containerd.New(socket, containerd.WithDefaultNamespace(namespace))
	if err != nil {
		return fmt.Errorf("sandbox: connect %s: %w", socket, err)
	}
	defer client.Close()
	return clearStale(ctx, client, sessionID)
}
