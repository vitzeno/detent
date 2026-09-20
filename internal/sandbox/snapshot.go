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
	if err := c.pin(ctx, info.Snapshotter, checkpoint); err != nil {
		return "", err
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

// pin holds a checkpoint against containerd's garbage collector for the
// session's lifetime.
//
// The GC keeps a snapshot only while something roots it: a container's
// current SnapshotKey, or a lease. Checkpoints were rooted by nothing —
// they survived incidentally, as ancestors of whatever the container
// pointed at. Rolling back to an earlier one orphans every checkpoint
// after it, and the next GC pass deletes them, so the second rollback
// in a session failed with "parent snapshot does not exist". Leasing
// each checkpoint as it's taken is what makes them outlive the branch
// they were on, which is the whole point of being able to roll back
// more than once.
func (c *Container) pin(ctx context.Context, snapshotter, key string) error {
	if c.lease == nil {
		return fmt.Errorf("sandbox: no lease (Start not called)")
	}
	err := c.client.LeasesService().AddResource(ctx, *c.lease, leases.Resource{
		ID:   key,
		Type: "snapshots/" + snapshotter,
	})
	if err != nil {
		return fmt.Errorf("sandbox: pin checkpoint: %w", err)
	}
	return nil
}

// withSnapshotKey re-points a container at an existing snapshot,
// unlike containerd.WithNewSnapshot which creates one.
func withSnapshotKey(key string) containerd.UpdateContainerOpts {
	return func(_ context.Context, _ *containerd.Client, c *containers.Container) error {
		c.SnapshotKey = key
		return nil
	}
}
