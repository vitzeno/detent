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

// Snapshot commits the active snapshot as a checkpoint and re-points the
// container at a fresh one on top. Returns the checkpoint's key.
func (c *Container) Snapshot(ctx context.Context) (string, error) {
	if c.container == nil {
		return "", errNotStarted
	}
	release, err := c.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
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
		return errNotStarted
	}
	release, err := c.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
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
	// Nothing can reach the abandoned branch again, and the lease would keep it till Close.
	_ = sn.Remove(ctx, info.SnapshotKey)
	return nil
}

// leased roots every snapshot made under ctx in the session's lease, so
// the GC spares checkpoints and a snapshot not yet pointed at.
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
