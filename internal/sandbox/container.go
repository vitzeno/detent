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
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"

	"github.com/vitzeno/detent/internal/capture"
)

// DefaultImage is Ubuntu (GNU coreutils, not BusyBox) with git and
// curl already in it, so goals have them under NetworkNone too.
// Fully qualified: containerd's client doesn't expand Hub shorthand.
const DefaultImage = "docker.io/library/buildpack-deps:24.04-scm"

// Network postures. Host means the daemon's host: the colima VM on
// macOS, this machine on Linux.
const (
	NetworkNone = "none" // loopback only, no DNS
	NetworkHost = "host" // no network namespace, inherit the shim's
)

// DefaultNetwork is what NewContainer uses when nothing says otherwise.
const DefaultNetwork = NetworkNone

// DefaultNamespace keeps this tool's containers separate from other
// containerd users (Docker, Kubernetes) on the same daemon.
const DefaultNamespace = "detent"

// defaultSnapshotter avoids containerd client's own OS-compiled
// fallback, wrong once the client and the daemon run on different OSes.
const defaultSnapshotter = "overlayfs"

// defaultRuntime is the standard runc shim; this daemon's NewContainer
// doesn't default it for us the way some client versions do.
const defaultRuntime = "io.containerd.runc.v2"

// Container is a session-scoped containerd Runner, satisfied
// structurally: it imports neither host nor engine. An isolated
// bridge network would need CNI, which go-cni cannot drive from
// macOS.
type Container struct {
	socket     string
	namespace  string
	image      string
	mountPoint string
	limit      int
	runtime    string
	network    string

	client    *containerd.Client
	lease     *leases.Lease // roots this session's checkpoints, see snapshot.go
	img       containerd.Image
	container containerd.Container
	workspace string // host directory bind-mounted in; always os.Getwd()
	fifoDir   string // client-side dir for cio's stdio FIFOs
	sessionID string

	// running serialises Run: one task and one spec per container, so
	// parallel commands overwrote each other's and returned exit 0
	// with no output.
	running sync.Mutex
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

	// Checkpoints need a root of their own, or the GC reclaims them as
	// soon as a rollback drops them out of the active branch. Close
	// releases this, which is also what cleans them up.
	if err := clearStale(ctx, client, sessionID); err != nil {
		return err
	}
	l, err := client.LeasesService().Create(ctx, leases.WithID(leaseID(sessionID)))
	if err != nil {
		return fmt.Errorf("sandbox: create lease: %w", err)
	}
	c.lease = &l

	img, err := resolveImage(ctx, client, c.image)
	if err != nil {
		return err
	}
	c.img = img

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
	if c.network == NetworkHost {
		// resolv.conf and hosts come too, or DNS resolves nothing
		// despite the interfaces being right there.
		specOpts = append(specOpts,
			oci.WithHostNamespace(specs.NetworkNamespace),
			oci.WithHostResolvconf,
			oci.WithHostHostsFile,
		)
	}
	containerOpts := []containerd.NewContainerOpts{
		containerd.WithSnapshotter(defaultSnapshotter),
		containerd.WithNewSnapshot(containerID(sessionID)+"-snap", img),
		containerd.WithNewSpec(specOpts...),
	}
	runtimeName := c.runtime
	if runtimeName == "" {
		runtimeName = defaultRuntime
	}
	containerOpts = append(containerOpts, containerd.WithRuntime(runtimeName, nil))

	cont, err := client.NewContainer(ctx, containerID(sessionID), containerOpts...)
	if err != nil {
		return fmt.Errorf("sandbox: create container: %w", err)
	}
	c.container = cont
	return nil
}

// sandboxOutputDir, relative to the workspace, holds each session's
// own subdirectory of stdout/stderr capture files (see Run).
const sandboxOutputDir = ".detent-sandbox"

// Run creates a task per command against the container's current
// snapshot, one at a time, so filesystem state carries over. Output
// is shell-redirected into the workspace mount rather than captured
// via cio, whose FIFOs need the shim and reader on one kernel.
func (c *Container) Run(ctx context.Context, command string, events chan<- capture.StreamEvent) (capture.Result, error) {
	if c.container == nil {
		return capture.Result{}, fmt.Errorf("sandbox: Start not called")
	}
	c.running.Lock()
	defer c.running.Unlock()

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
	// exec redirects then gets out of the way, so the command keeps
	// its own line structure. "( cmd ) >out" glued that tail onto the
	// last line and broke every heredoc.
	spec.Process.Args = []string{"sh", "-c",
		fmt.Sprintf("exec >%s 2>%s\n%s\n",
			shellQuote(containerOutPath), shellQuote(containerErrPath), command)}
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

// Close tears down the container, its current snapshot, and the
// client connection. Safe to call even if Start failed partway.
// clearStale removes what a killed process left behind. Only Close
// deletes a container and its lease, so a kill leaves both, and the
// ids are per session: resuming one would collide with its own corpse.
func clearStale(ctx context.Context, client *containerd.Client, sessionID string) error {
	cont, err := client.LoadContainer(ctx, containerID(sessionID))
	switch {
	case errdefs.IsNotFound(err):
	case err != nil:
		return fmt.Errorf("sandbox: look for a stale container: %w", err)
	default:
		if err := clearTask(ctx, cont, sessionID); err != nil {
			return err
		}
		if err := cont.Delete(ctx, containerd.WithSnapshotCleanup); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("sandbox: delete stale container: %w", err)
		}
	}
	// The checkpoints it rooted died with the container's snapshot, so
	// the lease goes too and the GC can reclaim them.
	err = client.LeasesService().Delete(ctx, leases.Lease{ID: leaseID(sessionID)})
	if err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("sandbox: delete stale lease: %w", err)
	}
	return nil
}

// clearTask refuses rather than killing a task that is still running:
// that is another detent holding this session, not a leftover.
func clearTask(ctx context.Context, cont containerd.Container, sessionID string) error {
	task, err := cont.Task(ctx, nil)
	if err != nil {
		return nil
	}
	if st, err := task.Status(ctx); err == nil && st.Status == containerd.Running {
		return fmt.Errorf("sandbox: session %s is already running somewhere", sessionID)
	}
	if _, err := task.Delete(ctx, containerd.WithProcessKill); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("sandbox: delete stale task: %w", err)
	}
	return nil
}

func (c *Container) Close(ctx context.Context) error {
	var errs []error
	if c.container != nil {
		if err := c.container.Delete(ctx, containerd.WithSnapshotCleanup); err != nil {
			errs = append(errs, err)
		}
	}
	// Dropping the lease is what lets the GC reclaim this session's
	// checkpoints; without it they would outlive the session forever.
	if c.lease != nil && c.client != nil {
		if err := c.client.LeasesService().Delete(ctx, *c.lease); err != nil {
			errs = append(errs, err)
		}
		c.lease = nil
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
		// Only this session's own subdirectory; Run's comment explains
		// why the shared sandboxOutputDir parent is never removed.
		_ = os.RemoveAll(filepath.Join(c.workspace, sandboxOutputDir, c.sessionID))
	}
	return errors.Join(errs...)
}

// containerID correlates the containerd container with the
// agent.Session that owns it, rather than inventing a second identity.
func containerID(sessionID string) string { return "detent-" + sessionID }

func leaseID(sessionID string) string { return containerID(sessionID) + "-checkpoints" }

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

// shellQuote single-quotes s for safe interpolation into a sh -c string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
