// Package sandbox runs commands inside a containerd-backed container,
// one persistent instance per session.
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	containerd "github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"

	"github.com/vitzeno/detent/internal/capture"
)

// DefaultImage has broad GNU/coreutils compatibility, unlike alpine's
// BusyBox utils. Fully qualified since containerd's client, unlike
// docker/nerdctl, doesn't expand Docker Hub shorthand itself.
const DefaultImage = "docker.io/library/ubuntu:24.04"

// DefaultNamespace keeps this tool's containers separate from other
// containerd users (Docker, Kubernetes) on the same daemon.
const DefaultNamespace = "detent"

// defaultSnapshotter avoids containerd client's own OS-compiled
// fallback, wrong once the client and the daemon run on different OSes.
const defaultSnapshotter = "overlayfs"

// defaultRuntime is the standard runc shim; this daemon's NewContainer
// doesn't default it for us the way some client versions do.
const defaultRuntime = "io.containerd.runc.v2"

// Container is a session-scoped containerd-backed agent.Runner. It
// never imports host or agent: Run returns capture's own aliased
// types, and Snapshot/Rollback use plain string IDs.
type Container struct {
	socket     string
	namespace  string
	image      string
	mountPoint string
	limit      int
	runtime    string
	network    bool

	client    *containerd.Client
	img       containerd.Image
	container containerd.Container
	workspace string // host directory bind-mounted in; always os.Getwd()
	fifoDir   string // client-side dir for cio's stdio FIFOs
	sessionID string
}

// Start connects to the daemon and creates the session's container,
// named from sessionID so it correlates with the owning agent.Session.
// Must be called once before Run.
func (c *Container) Start(ctx context.Context, sessionID string) error {
	workspace, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("sandbox: getwd: %w", err)
	}
	c.workspace = workspace
	c.sessionID = sessionID

	// The shim opening these FIFOs runs wherever the daemon does (e.g.
	// inside colima's VM), so the dir must be one it actually shares
	// with us (colima virtiofs-mounts $HOME), not the OS temp dir.
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("sandbox: home dir: %w", err)
	}
	fifoRoot := filepath.Join(home, ".detent", "sandbox-fifo")
	if err := os.MkdirAll(fifoRoot, 0o755); err != nil {
		return fmt.Errorf("sandbox: fifo dir: %w", err)
	}
	fifoDir, err := os.MkdirTemp(fifoRoot, sessionID+"-")
	if err != nil {
		return fmt.Errorf("sandbox: fifo dir: %w", err)
	}
	c.fifoDir = fifoDir

	client, err := containerd.New(c.socket, containerd.WithDefaultNamespace(c.namespace))
	if err != nil {
		return fmt.Errorf("sandbox: connect %s: %w", c.socket, err)
	}
	c.client = client

	img, err := resolveImage(ctx, client, c.image)
	if err != nil {
		return err
	}
	c.img = img

	id := "detent-" + sessionID
	specOpts := []oci.SpecOpts{
		// The daemon (and every container it runs) is always Linux,
		// regardless of the client's own OS; without this the spec
		// would default to the client's OS instead.
		oci.WithDefaultSpecForPlatform("linux/" + runtime.GOARCH),
		oci.WithImageConfig(img),
		oci.WithProcessArgs("sh", "-c", "true"),
		oci.WithProcessCwd(c.mountPoint),
		oci.WithMounts([]specs.Mount{{
			Type:        "bind",
			Source:      workspace,
			Destination: c.mountPoint,
			Options:     []string{"rbind", "rw"},
		}}),
	}
	containerOpts := []containerd.NewContainerOpts{
		containerd.WithSnapshotter(defaultSnapshotter),
		containerd.WithNewSnapshot(id+"-snap", img),
		containerd.WithNewSpec(specOpts...),
	}
	runtimeName := c.runtime
	if runtimeName == "" {
		runtimeName = defaultRuntime
	}
	containerOpts = append(containerOpts, containerd.WithRuntime(runtimeName, nil))

	cont, err := client.NewContainer(ctx, id, containerOpts...)
	if err != nil {
		return fmt.Errorf("sandbox: create container: %w", err)
	}
	c.container = cont
	return nil
}

// resolveImage returns the local image if present, pulling it
// (unpacked) otherwise.
func resolveImage(ctx context.Context, client *containerd.Client, ref string) (containerd.Image, error) {
	img, err := client.GetImage(ctx, ref)
	if err == nil {
		return img, nil
	}
	if !errdefs.IsNotFound(err) {
		return nil, fmt.Errorf("sandbox: get image %s: %w", ref, err)
	}
	img, err = client.Pull(ctx, ref, containerd.WithPullUnpack, containerd.WithPullSnapshotter(defaultSnapshotter))
	if err != nil {
		return nil, fmt.Errorf("sandbox: pull image %s: %w", ref, err)
	}
	return img, nil
}

// Close tears down the container, its current snapshot, and the
// client connection. Safe to call even if Start failed partway.
func (c *Container) Close(ctx context.Context) error {
	var errs []error
	if c.container != nil {
		if err := c.container.Delete(ctx, containerd.WithSnapshotCleanup); err != nil {
			errs = append(errs, err)
		}
	}
	if c.client != nil {
		if err := c.client.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if c.fifoDir != "" {
		if err := os.RemoveAll(c.fifoDir); err != nil {
			errs = append(errs, err)
		}
	}
	if c.workspace != "" {
		// Only this session's own subdirectory; see Run for why the
		// shared sandboxOutputDir parent itself is never removed.
		_ = os.RemoveAll(filepath.Join(c.workspace, sandboxOutputDir, c.sessionID))
	}
	return errors.Join(errs...)
}

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

// withSnapshotKey re-points a container at an existing snapshot,
// unlike containerd.WithNewSnapshot which creates one.
func withSnapshotKey(key string) containerd.UpdateContainerOpts {
	return func(_ context.Context, _ *containerd.Client, c *containers.Container) error {
		c.SnapshotKey = key
		return nil
	}
}

// sandboxOutputDir, relative to the workspace, holds each session's
// own subdirectory of stdout/stderr capture files (see Run).
const sandboxOutputDir = ".detent-sandbox"

// Run creates a fresh task per command against the container's
// current snapshot, so filesystem state carries over between them.
// Output is shell-redirected into the workspace mount, not captured
// via containerd's cio: its FIFOs need the shim and reader on the
// same kernel, not true once the daemon runs in a VM.
func (c *Container) Run(ctx context.Context, command string, events chan<- capture.StreamEvent) (capture.Result, error) {
	if c.container == nil {
		return capture.Result{}, fmt.Errorf("sandbox: Start not called")
	}

	// Session-scoped, not shared directly: recreating the same
	// directory path across back-to-back containers on a virtiofs
	// mount can serve the guest a stale, empty view of it.
	hostOutDir := filepath.Join(c.workspace, sandboxOutputDir, c.sessionID)
	if err := os.MkdirAll(hostOutDir, 0o755); err != nil {
		return capture.Result{}, fmt.Errorf("sandbox: create output dir: %w", err)
	}
	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	containerOutDir := path.Join(c.mountPoint, sandboxOutputDir, c.sessionID)
	containerOutPath := path.Join(containerOutDir, runID+".stdout")
	containerErrPath := path.Join(containerOutDir, runID+".stderr")
	hostOutPath := filepath.Join(hostOutDir, runID+".stdout")
	hostErrPath := filepath.Join(hostOutDir, runID+".stderr")
	defer os.Remove(hostOutPath)
	defer os.Remove(hostErrPath)

	spec, err := c.container.Spec(ctx)
	if err != nil {
		return capture.Result{}, fmt.Errorf("sandbox: read spec: %w", err)
	}
	spec.Process.Args = []string{"sh", "-c",
		fmt.Sprintf("( %s ) >%s 2>%s", command, shellQuote(containerOutPath), shellQuote(containerErrPath))}
	if err := c.container.Update(ctx, containerd.UpdateContainerOpts(containerd.WithSpec(spec))); err != nil {
		return capture.Result{}, fmt.Errorf("sandbox: update spec: %w", err)
	}

	task, err := c.container.NewTask(ctx, cio.NewCreator(cio.WithFIFODir(c.fifoDir)))
	if err != nil {
		return capture.Result{}, fmt.Errorf("sandbox: create task: %w", err)
	}

	exitCh, err := task.Wait(ctx)
	if err != nil {
		_, _ = task.Delete(context.Background())
		return capture.Result{}, fmt.Errorf("sandbox: wait task: %w", err)
	}

	if err := task.Start(ctx); err != nil {
		_, _ = task.Delete(context.Background())
		return capture.Result{}, fmt.Errorf("sandbox: start task: %w", err)
	}

	done := make(chan struct{})
	var stdout, stderr bytes.Buffer
	var scanWG sync.WaitGroup
	scanWG.Add(2)
	go func() { defer scanWG.Done(); tailFile(hostOutPath, false, &stdout, c.limit, events, done) }()
	go func() { defer scanWG.Done(); tailFile(hostErrPath, true, &stderr, c.limit, events, done) }()

	var exitCode uint32
	select {
	case status := <-exitCh:
		exitCode = status.ExitCode()
	case <-ctx.Done():
		bg := context.Background()
		_ = task.Kill(bg, syscall.SIGKILL)
		<-exitCh
	}

	close(done)
	scanWG.Wait()
	if events != nil {
		close(events)
	}

	_, _ = task.Delete(context.Background())

	res := capture.Result{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		ExitCode:  int(exitCode),
		Truncated: stdout.Len() >= c.limit || stderr.Len() >= c.limit,
	}
	if ctx.Err() != nil {
		return res, fmt.Errorf("sandbox: %w", ctx.Err())
	}
	return res, nil
}

// shellQuote single-quotes s for safe interpolation into a sh -c string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
