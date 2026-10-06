package tool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
)

// maxEditBytes is the largest file write_file and edit_file read whole.
const maxEditBytes = 50 << 20

// failed is a native tool refusing or failing, said the way a command would.
func failed(code int, format string, a ...any) capture.Result {
	return capture.Result{ExitCode: code, Stderr: fmt.Sprintf(format, a...) + "\n"}
}

// stopped is a native tool cut short by its context, keeping what it printed.
func stopped(name event.ToolName, out string, err error) capture.Result {
	why := "stopped"
	if err != nil {
		why += ": " + err.Error()
	}
	return capture.Result{ExitCode: 1, Stdout: out, Stderr: string(name) + ": " + why + "\n"}
}

// openRegular opens p to read, refusing anything but a regular file before
// opening it, since opening a FIFO blocks until something writes to it.
func openRegular(p string) (*os.File, error) {
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if err := regular(p, info); err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	if info, err = f.Stat(); err == nil {
		err = regular(p, info)
	}
	if err != nil {
		_ = f.Close() // read only, so closing cannot lose anything
		return nil, err
	}
	return f, nil
}

// regular refuses a directory as a read of one fails, and a FIFO, socket or
// device outright, since reading one can block or never end.
func regular(p string, info fs.FileInfo) error {
	m := info.Mode()
	switch {
	case m.IsRegular():
		return nil
	case m.IsDir():
		return &fs.PathError{Op: "read", Path: p, Err: syscall.EISDIR}
	}
	kind := "a device"
	switch {
	case m&fs.ModeNamedPipe != 0:
		kind = "a named pipe"
	case m&fs.ModeSocket != 0:
		kind = "a socket"
	}
	return &fs.PathError{Op: "open", Path: p, Err: fmt.Errorf("is %s, not a regular file, so read it with a command if you must", kind)}
}

// readCapped reads a regular file whole, refusing one over limit bytes.
func readCapped(ctx context.Context, p string, limit int64) ([]byte, error) {
	f, err := openRegular(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read only, so closing cannot lose anything
	if info, err := f.Stat(); err == nil && info.Size() > limit {
		return nil, tooLarge(p, limit)
	}
	b, err := io.ReadAll(io.LimitReader(ctxReader{ctx, f}, limit+1))
	if err == nil && int64(len(b)) > limit {
		return nil, tooLarge(p, limit) // it grew since
	}
	return b, err
}

func tooLarge(p string, limit int64) error {
	return &fs.PathError{Op: "read", Path: p, Err: fmt.Errorf("larger than %d MB, so change it with a command", limit>>20)}
}

// ctxReader stops reading once ctx is done. It checks between reads, so one
// stuck on a hung mount still blocks.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// writeAsShell writes as a shell redirect does, truncating in place or creating with
// 0666 less the umask. Not atomic, since a rename would lose mode, owner and links.
func writeAsShell(p, s string) error {
	return os.WriteFile(p, []byte(s), 0o666) //nolint:gosec // the mode a command's own redirect would give it
}

// osReason is err without the operation Go puts in front of it, so a failure
// reads "nope: no such file or directory" as a command's does, on every OS.
func osReason(p string, err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return p + ": no such file or directory"
	case errors.Is(err, fs.ErrPermission):
		return p + ": permission denied"
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return p + ": " + pe.Err.Error()
	}
	return p + ": " + err.Error()
}
