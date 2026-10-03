// Package sandbox runs commands inside a containerd-backed container,
// one persistent instance per session. The last command run stays in the
// container's stored spec until the session closes.
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
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

// DefaultImage is Ubuntu (GNU coreutils) with git and curl, so they work
// under NetworkNone too. Fully qualified, since containerd won't expand it.
const DefaultImage = "docker.io/library/buildpack-deps:24.04-scm"

// DefaultMountPoint is where the working directory appears in the container.
const DefaultMountPoint = "/workspace"

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

// defaultRuntime is the runc shim, which NewContainer does not default.
const defaultRuntime = "io.containerd.runc.v2"

// sandboxOutputDir, relative to the workspace, holds each session's
// own subdirectory of stdout/stderr capture files (see Run).
const sandboxOutputDir = ".detent-sandbox"

// maxOutputFile stops a command whose capture file passes it, since the
// file lives in the human's directory and only its head is ever read.
const maxOutputFile = 64 << 20

// Bounds on the calls that clean up after a command, so a hung daemon
// cannot hold the container forever.
const (
	killWait    = 5 * time.Second
	cleanupWait = 10 * time.Second
)

// containerPrefix marks a container as detent's own, which is how Prune
// tells ours from anything else in the namespace.
const containerPrefix = "detent-"

const leaseSuffix = "-checkpoints"

// holderLabel names the process holding a session's container, as pid@host.
const holderLabel = "detent/holder"

// ErrSessionLive marks a container another detent still holds, so
// neither starting over it nor pruning it may touch it.
var ErrSessionLive = errors.New("already running somewhere")

var errNotStarted = errors.New("sandbox: Start not called")

// Container is a session-scoped containerd Runner, satisfied structurally:
// it imports neither host nor engine.
type Container struct {
	socket     string
	namespace  string
	image      string
	mountPoint string
	limit      int
	runtime    string
	network    string
	// readOnly maps host directories to where they appear, unwritable.
	readOnly map[string]string

	client    *containerd.Client
	lease     *leases.Lease // roots this session's checkpoints, see snapshot.go
	img       containerd.Image
	container containerd.Container
	workspace string // host directory bind-mounted in, always os.Getwd()
	fifoDir   string // client-side dir for cio's stdio FIFOs
	sessionID string

	// slot serialises Run, Snapshot and Rollback: one task and one spec
	// per container, and a whole-record Update would undo another's.
	slot chan struct{}
}

// Start connects to the daemon and creates the session's container,
// named from sessionID. Must be called once before Run.
func (c *Container) Start(ctx context.Context, sessionID string) error {
	if c.client != nil {
		return errors.New("sandbox: already started")
	}
	if c.network != NetworkNone && c.network != NetworkHost {
		return fmt.Errorf("sandbox: unknown network %q, want %q or %q", c.network, NetworkNone, NetworkHost)
	}
	if c.limit <= 0 {
		return fmt.Errorf("sandbox: output limit must be positive, got %d", c.limit)
	}
	if c.slot == nil {
		c.slot = make(chan struct{}, 1)
	}
	workspace, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("sandbox: getwd: %w", err)
	}
	c.workspace = workspace
	c.sessionID = sessionID
	if err := writeIgnore(filepath.Join(workspace, sandboxOutputDir)); err != nil {
		return err
	}

	// The shim opening these FIFOs runs where the daemon does, so the dir
	// must be shared with it (colima mounts $HOME), not the OS temp dir.
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("sandbox: home dir: %w", err)
	}
	fifoRoot := filepath.Join(home, ".detent", "sandbox-fifo")
	if err := os.MkdirAll(fifoRoot, 0o755); err != nil { //nolint:gosec // the shim reads it from inside the VM
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

	// Checkpoints need a lease of their own, or the GC reclaims them once a
	// rollback leaves their branch. Close releases it.
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
		// The daemon is always Linux, whatever the client's OS is.
		oci.WithDefaultSpecForPlatform("linux/" + runtime.GOARCH),
		oci.WithImageConfig(img),
		oci.WithProcessArgs("sh", "-c", "true"),
		oci.WithProcessCwd(c.mountPoint),
		oci.WithMounts(c.mounts(workspace)),
	}
	if c.network == NetworkHost {
		// resolv.conf and hosts come too, or DNS resolves nothing.
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
		containerd.WithContainerLabels(map[string]string{holderLabel: holder()}),
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

// Run runs one command at a time as a task on the current snapshot, so
// state carries over. Output goes to files, since cio's FIFOs need one
// kernel. Run sends nothing after it returns, and the caller closes events.
func (c *Container) Run(ctx context.Context, command string, events chan<- capture.StreamEvent) (capture.Result, error) {
	if c.container == nil {
		return capture.Result{}, errNotStarted
	}
	release, err := c.acquire(ctx)
	if err != nil {
		return capture.Result{}, err
	}
	defer release()

	// Session-scoped: recreating one path across containers on a virtiofs
	// mount can serve the guest a stale, empty view of it.
	hostOutDir := filepath.Join(c.workspace, sandboxOutputDir, c.sessionID)
	if err := os.MkdirAll(hostOutDir, 0o755); err != nil { //nolint:gosec // the container writes here as its own user
		return capture.Result{}, fmt.Errorf("sandbox: create output dir: %w", err)
	}
	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	containerOutDir := path.Join(c.mountPoint, sandboxOutputDir, c.sessionID)
	containerOutPath := path.Join(containerOutDir, runID+".stdout")
	containerErrPath := path.Join(containerOutDir, runID+".stderr")
	hostOutPath := filepath.Join(hostOutDir, runID+".stdout")
	hostErrPath := filepath.Join(hostOutDir, runID+".stderr")
	defer os.Remove(hostOutPath) //nolint:errcheck // a leftover is swept with the directory
	defer os.Remove(hostErrPath) //nolint:errcheck // a leftover is swept with the directory

	spec, err := c.container.Spec(ctx)
	if err != nil {
		return capture.Result{}, fmt.Errorf("sandbox: read spec: %w", err)
	}
	// exec redirects then gets out of the way, so the command keeps its
	// own lines. A "( cmd ) >out" wrapper would break every heredoc.
	spec.Process.Args = []string{"sh", "-c",
		fmt.Sprintf("exec >%s 2>%s\n%s\n",
			shellQuote(containerOutPath), shellQuote(containerErrPath), command)}
	if err := c.container.Update(ctx, containerd.UpdateContainerOpts(containerd.WithSpec(spec))); err != nil {
		return capture.Result{}, fmt.Errorf("sandbox: update spec: %w", err)
	}

	// Once a task exists it must be gone before the next Run, so only the
	// command itself is cut short by ctx.
	bg := context.WithoutCancel(ctx)
	tctx, cancel := context.WithTimeout(bg, cleanupWait)
	task, err := c.container.NewTask(tctx, cio.NewCreator(cio.WithFIFODir(c.fifoDir)))
	cancel()
	if err != nil {
		return capture.Result{}, fmt.Errorf("sandbox: create task: %w", err)
	}
	exitCh, err := task.Wait(bg)
	if err != nil {
		return capture.Result{}, errors.Join(fmt.Errorf("sandbox: wait task: %w", err), deleteTask(bg, task))
	}
	if err := task.Start(bg); err != nil {
		return capture.Result{}, errors.Join(fmt.Errorf("sandbox: start task: %w", err), deleteTask(bg, task))
	}

	done := make(chan struct{})
	var stdout, stderr bytes.Buffer
	var outCut, errCut bool
	var scanWG sync.WaitGroup
	scanWG.Add(2)
	go func() { defer scanWG.Done(); outCut = tailFile(hostOutPath, false, &stdout, c.limit, events, done) }()
	go func() { defer scanWG.Done(); errCut = tailFile(hostErrPath, true, &stderr, c.limit, events, done) }()

	exitCode, overflow, waitErr := await(ctx, bg, task, exitCh, hostOutPath, hostErrPath)

	close(done)
	scanWG.Wait()
	delErr := deleteTask(bg, task)

	res := capture.Result{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		ExitCode:  int(exitCode),
		Truncated: outCut || errCut || overflow,
	}
	switch {
	case ctx.Err() != nil:
		return res, errors.Join(fmt.Errorf("sandbox: %w", ctx.Err()), delErr)
	case overflow:
		return res, errors.Join(fmt.Errorf("sandbox: output passed %d MiB, so the command was stopped", maxOutputFile>>20), delErr)
	case waitErr != nil:
		return res, errors.Join(fmt.Errorf("sandbox: wait: %w", waitErr), delErr)
	}
	return res, delErr
}

// await waits on bg, not ctx, for the task to exit, since a cancelled wait
// returns at once and leaves the task running. A cancel or a flood kills it.
func await(ctx, bg context.Context, task containerd.Task, exitCh <-chan containerd.ExitStatus, files ...string) (code uint32, overflow bool, err error) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case st := <-exitCh:
			code, _, err = st.Result()
			return code, false, err
		case <-ctx.Done():
			kill(bg, task, exitCh)
			return 0, false, nil
		case <-tick.C:
			if tooBig(files) {
				kill(bg, task, exitCh)
				return 0, true, nil
			}
		}
	}
}

func kill(bg context.Context, task containerd.Task, exitCh <-chan containerd.ExitStatus) {
	_ = task.Kill(bg, syscall.SIGKILL)
	select {
	case <-exitCh:
	case <-time.After(killWait):
	}
}

func tooBig(files []string) bool {
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && st.Size() > maxOutputFile {
			return true
		}
	}
	return false
}

// deleteTask removes a finished or stuck task, killing it if it still
// runs: one left behind fails every later NewTask with "already exists".
func deleteTask(bg context.Context, task containerd.Task) error {
	ctx, cancel := context.WithTimeout(bg, cleanupWait)
	defer cancel()
	if _, err := task.Delete(ctx, containerd.WithProcessKill); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("sandbox: delete task: %w", err)
	}
	return nil
}

// acquire takes the container, giving up when ctx does: a Call queued
// behind a long human command must still honour its own bound.
func (c *Container) acquire(ctx context.Context) (func(), error) {
	select {
	case c.slot <- struct{}{}:
		return func() { <-c.slot }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("sandbox: waiting for the container: %w", ctx.Err())
	}
}

// Close tears down the container, its current snapshot, and the
// client connection. Safe to call even if Start failed partway, and twice.
func (c *Container) Close(ctx context.Context) error {
	if c.slot != nil {
		release, err := c.closeSlot(ctx)
		if err != nil {
			return err
		}
		defer release()
	}
	var errs []error
	containerGone := true
	if c.container != nil {
		if task, err := c.container.Task(ctx, nil); err == nil {
			if err := deleteTask(ctx, task); err != nil {
				errs = append(errs, err)
			}
		}
		if err := c.container.Delete(ctx, containerd.WithSnapshotCleanup); err != nil && !errdefs.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("sandbox: delete container: %w", err))
			containerGone = false
		}
		c.container = nil
	}
	// Dropping the lease is what lets the GC reclaim this session's
	// checkpoints, so it stays while their container does.
	if c.lease != nil && c.client != nil && containerGone {
		if err := c.client.LeasesService().Delete(ctx, *c.lease); err != nil {
			errs = append(errs, fmt.Errorf("sandbox: delete lease: %w", err))
		}
		c.lease = nil
	}
	if c.client != nil {
		if err := c.client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("sandbox: close client: %w", err))
		}
		c.client = nil
	}
	if c.fifoDir != "" {
		if err := os.RemoveAll(c.fifoDir); err != nil {
			errs = append(errs, fmt.Errorf("sandbox: fifo dir: %w", err))
		}
		c.fifoDir = ""
	}
	if c.workspace != "" {
		// This session's subdirectory, then the parent once nobody else uses it.
		_ = os.RemoveAll(filepath.Join(c.workspace, sandboxOutputDir, c.sessionID))
		removeOutputDir(filepath.Join(c.workspace, sandboxOutputDir))
	}
	return errors.Join(errs...)
}

// closeSlot takes the container from whatever still runs in it, killing
// that rather than waiting out a long command.
func (c *Container) closeSlot(ctx context.Context) (func(), error) {
	quick, cancel := context.WithTimeout(ctx, killWait)
	release, err := c.acquire(quick)
	cancel()
	if err == nil {
		return release, nil
	}
	if c.container != nil {
		if task, err := c.container.Task(ctx, nil); err == nil {
			_ = deleteTask(ctx, task)
		}
	}
	return c.acquire(ctx)
}

// writeIgnore gives the output directory its own .gitignore, so a commit
// made inside the sandbox never takes the file its output is going to.
func writeIgnore(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // the container writes here as its own user
		return fmt.Errorf("sandbox: create output dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*\n"), 0o644); err != nil { //nolint:gosec // an ordinary file in the human's repo
		return fmt.Errorf("sandbox: ignore output dir: %w", err)
	}
	return nil
}

// removeOutputDir removes the shared directory once only its .gitignore is left.
func removeOutputDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != ".gitignore" {
		return
	}
	_ = os.Remove(filepath.Join(dir, ".gitignore"))
	_ = os.Remove(dir)
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
// session holds: between Calls only its holder label says so. Doubt refuses.
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

// mounts is the workspace, writable, then each read-only directory in a
// stable order so the spec is the same from run to run.
func (c *Container) mounts(workspace string) []specs.Mount {
	out := []specs.Mount{{Type: "bind", Source: workspace, Destination: c.mountPoint, Options: []string{"rbind", "rw"}}}
	for _, src := range slices.Sorted(maps.Keys(c.readOnly)) {
		out = append(out, specs.Mount{Type: "bind", Source: src, Destination: c.readOnly[src], Options: []string{"rbind", "ro"}})
	}
	return out
}
