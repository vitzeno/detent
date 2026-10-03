package sandbox

import (
	"bytes"
	"io"
	"os"
	"time"

	"github.com/vitzeno/detent/internal/capture"
)

const tailPollInterval = 20 * time.Millisecond

// tailFile scans path's growing contents until done closes and it is drained,
// reporting whether anything was cut. A file that never appears reads as empty.
func tailFile(path string, isStderr bool, buf *bytes.Buffer, limit int, events chan<- capture.StreamEvent, done <-chan struct{}) bool {
	f, err := openWithRetry(path, done)
	if err != nil {
		return false
	}
	defer f.Close()
	truncated, _ := capture.ScanCapped(&pollingReader{f: f, done: done}, isStderr, buf, limit, events)
	return truncated
}

// openWithRetry waits for the file as long as the command runs, since a
// cold start or a stale virtiofs view can be slow to show it.
func openWithRetry(path string, done <-chan struct{}) (*os.File, error) {
	for {
		f, err := os.Open(path)
		if err == nil {
			return f, nil
		}
		select {
		case <-done:
			return os.Open(path)
		case <-time.After(tailPollInterval):
		}
	}
}

// pollingReader turns a file still being appended to by another
// process into a blocking io.Reader, polling until done closes.
type pollingReader struct {
	f        *os.File
	done     <-chan struct{}
	sawClose bool
}

func (r *pollingReader) Read(p []byte) (int, error) {
	for {
		n, err := r.f.Read(p)
		if n > 0 {
			return n, nil
		}
		if err != nil && err != io.EOF {
			return 0, err
		}
		if r.sawClose {
			return 0, io.EOF
		}
		select {
		case <-r.done:
			// One more read straight away catches what landed before done.
			r.sawClose = true
		case <-time.After(tailPollInterval):
		}
	}
}
