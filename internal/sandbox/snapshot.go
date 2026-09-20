package sandbox

import (
	"context"
	"fmt"
	"strconv"
	"time"

	containerd "github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/leases"
)

// Snapshot commits the container's active snapshot as a read-only
// checkpoint, then re-points it at a fresh active snapshot layered on
// top so Run keeps working. Returns the checkpoint's key.
func (c *Container) Snapshot(ctx context.Context) (string, error) {
	if c.container == nil {
		return "", fmt.Errorf("sandbox: Start not called")
	}
	ctx = c.leased(ctx)
	info, err := c.container.Info(ctx)
	if err != nil {
		return "", fmt.Errorf("sandbox: container info: %w", err)
	}
	sn := c.client.SnapshotService(info.Snapshotter)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	checkpoint := info.SnapshotKey + "-checkpoint-" + suffix
	if err := sn.Commit(ctx, checkpoint, info.SnapshotKey); err != nil {
		return "", fmt.Errorf("sandbox: commit snapshot: %w", err)
	}
	active := info.SnapshotKey + "-active-" + suffix
	if _, err := sn.Prepare(ctx, active, checkpoint); err != nil {
		return "", fmt.Errorf("sandbox: prepare snapshot: %w", err)
	}
	if err := c.container.Update(ctx, withSnapshotKey(active)); err != nil {
		return "", fmt.Errorf("sandbox: repoint snapshot: %w", err)
	}
	return checkpoint, nil
}

// Rollback restores a checkpoint by preparing a fresh active snapshot
// as its child and re-pointing the container at it.
func (c *Container) Rollback(ctx context.Context, id string) error {
	if c.container == nil {
		return fmt.Errorf("sandbox: Start not called")
	}
	ctx = c.leased(ctx)
	info, err := c.container.Info(ctx)
	if err != nil {
		return fmt.Errorf("sandbox: container info: %w", err)
	}
	sn := c.client.SnapshotService(info.Snapshotter)
	active := id + "-active-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := sn.Prepare(ctx, active, id); err != nil {
		return fmt.Errorf("sandbox: prepare snapshot: %w", err)
	}
	if err := c.container.Update(ctx, withSnapshotKey(active)); err != nil {
		return fmt.Errorf("sandbox: repoint snapshot: %w", err)
	}
	return nil
}

// leased puts the session's lease on ctx, so every snapshot made under
// it is rooted from birth. containerd's GC keeps a snapshot only while
// a container or lease references it, and both the checkpoints and the
// gap between preparing an active snapshot and pointing the container
// at it are otherwise unreferenced — a pass landing there took them.
func (c *Container) leased(ctx context.Context) context.Context {
	if c.lease == nil {
		return ctx
	}
	return leases.WithLease(ctx, c.lease.ID)
}

// withSnapshotKey re-points a container at an existing snapshot,
// unlike containerd.WithNewSnapshot which creates one.
func withSnapshotKey(key string) containerd.UpdateContainerOpts {
	return func(_ context.Context, _ *containerd.Client, c *containers.Container) error {
		c.SnapshotKey = key
		return nil
	}
}
