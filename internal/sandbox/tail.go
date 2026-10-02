package sandbox

import (
	"bytes"
	"io"
	"os"
	"time"

	"github.com/vitzeno/detent/internal/capture"
)

const tailPollInterval = 20 * time.Millisecond

// tailFile feeds path's growing contents to capture.ScanCapped until done
// closes and the file is drained. A file that never appears reads as empty.
func tailFile(path string, isStderr bool, buf *bytes.Buffer, limit int, events chan<- capture.StreamEvent, done <-chan struct{}) {
	f, err := openWithRetry(path, done)
	if err != nil {
		return
	}
	defer f.Close()
	capture.ScanCapped(&pollingReader{f: f, done: done}, isStderr, buf, limit, events)
}

func openWithRetry(path string, done <-chan struct{}) (*os.File, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		f, err := os.Open(path)
		if err == nil {
			return f, nil
		}
		select {
		case <-done:
			return os.Open(path)
		default:
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(tailPollInterval)
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
		select {
		case <-r.done:
			if r.sawClose {
				return 0, io.EOF
			}
			r.sawClose = true
		default:
		}
		time.Sleep(tailPollInterval)
	}
}
