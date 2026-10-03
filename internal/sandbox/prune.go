package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	containerd "github.com/containerd/containerd"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/leases"
)

// Pruned is what a prune removed, by session id, and what it left
// alone because something was still running in it.
type Pruned struct {
	Containers []string
	Leases     []string
	Kept       []string
}

// Empty reports whether nothing was removed.
func (p Pruned) Empty() bool { return len(p.Containers) == 0 && len(p.Leases) == 0 }

// Prune removes what abandoned sessions left in containerd. Anything no
// live process holds goes, since starting a session never adopts one.
func Prune(ctx context.Context, socket, namespace string) (Pruned, error) {
	client, err := containerd.New(socket, containerd.WithDefaultNamespace(namespace))
	if err != nil {
		return Pruned{}, fmt.Errorf("sandbox: connect %s: %w", socket, err)
	}
	defer client.Close() //nolint:errcheck // the work is done or already failed by now

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

// Forget removes one session's container, snapshot and lease.
// Refuses one a live process still holds.
func Forget(ctx context.Context, socket, namespace, sessionID string) error {
	client, err := containerd.New(socket, containerd.WithDefaultNamespace(namespace))
	if err != nil {
		return fmt.Errorf("sandbox: connect %s: %w", socket, err)
	}
	defer client.Close() //nolint:errcheck // the work is done or already failed by now
	return clearStale(ctx, client, sessionID)
}

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

// clearStale removes what a killed process left behind, since its ids
// are per session and resuming it would otherwise collide with them.
func clearStale(ctx context.Context, client *containerd.Client, sessionID string) error {
	cont, err := client.LoadContainer(ctx, containerID(sessionID))
	switch {
	case errdefs.IsNotFound(err):
	case err != nil:
		return fmt.Errorf("sandbox: look for a stale container: %w", err)
	default:
		if err := dropContainer(ctx, cont, sessionID); err != nil {
			return err
		}
	}
	// The checkpoints it rooted died with the container's snapshot, so
	// the lease goes too and the GC can reclaim them.
	return dropLease(ctx, client, sessionID)
}

// dropContainer deletes a container and its snapshot, refusing one a live
// session holds: between tool calls only its holder label says so. Doubt refuses.
func dropContainer(ctx context.Context, cont containerd.Container, sessionID string) error {
	labels, err := cont.Labels(ctx)
	if err != nil {
		return fmt.Errorf("sandbox: labels of %s: %w", sessionID, err)
	}
	if heldElsewhere(labels[holderLabel]) {
		return fmt.Errorf("sandbox: session %s is %w", sessionID, ErrSessionLive)
	}
	task, err := cont.Task(ctx, nil)
	switch {
	case errdefs.IsNotFound(err):
	case err != nil:
		return fmt.Errorf("sandbox: task of %s: %w", sessionID, err)
	default:
		st, err := task.Status(ctx)
		if err != nil {
			return fmt.Errorf("sandbox: status of %s: %w", sessionID, err)
		}
		if st.Status == containerd.Running {
			return fmt.Errorf("sandbox: session %s is %w", sessionID, ErrSessionLive)
		}
		if _, err := task.Delete(ctx, containerd.WithProcessKill); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("sandbox: delete stale task: %w", err)
		}
	}
	if err := cont.Delete(ctx, containerd.WithSnapshotCleanup); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("sandbox: delete stale container: %w", err)
	}
	return nil
}

func dropLease(ctx context.Context, client *containerd.Client, sessionID string) error {
	err := client.LeasesService().Delete(ctx, leases.Lease{ID: leaseID(sessionID)})
	if err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("sandbox: delete stale lease: %w", err)
	}
	return nil
}

func holder() string {
	host, _ := os.Hostname()
	return strconv.Itoa(os.Getpid()) + "@" + host
}

// heldElsewhere reports whether another live process on this machine holds
// a container. One elsewhere cannot be asked, so only a running task keeps it.
func heldElsewhere(label string) bool {
	pidText, host, ok := strings.Cut(label, "@")
	pid, err := strconv.Atoi(pidText)
	if !ok || err != nil || pid <= 0 {
		return false
	}
	if self, _ := os.Hostname(); host != self || pid == os.Getpid() {
		return false
	}
	return processAlive(pid)
}

// containerID names a container after the session that owns it.
func containerID(sessionID string) string { return containerPrefix + sessionID }

func leaseID(sessionID string) string { return containerID(sessionID) + leaseSuffix }
