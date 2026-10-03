package winjob

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Job is one process tree. A child joins it as it starts, but only once
// Assign has run: anything started before then is outside it.
type Job struct {
	mu     sync.Mutex
	handle windows.Handle
}

// Assign puts p in a new job that dies with its last handle, so the tree
// goes when detent does, however it exits.
func Assign(p *os.Process) (*Job, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("winjob: %w", err)
	}
	j := &Job{handle: h}
	if err := j.limit(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	// p's own handle keeps its pid from being reused, so this opens the same process.
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid)) //nolint:gosec // a pid fits
	if err != nil {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("winjob: %w", err)
	}
	defer windows.CloseHandle(proc) //nolint:errcheck // nothing to do about it
	if err := windows.AssignProcessToJobObject(h, proc); err != nil {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("winjob: %w", err)
	}
	return j, nil
}

// Kill ends every process still in the job, and does nothing once it is let go.
func (j *Job) Kill() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 {
		return nil
	}
	return windows.TerminateJobObject(j.handle, 1)
}

// Close lets the job go, which ends every process still in it.
func (j *Job) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(j.handle)
	j.handle = 0
	return err
}

// Release lets the job go and leaves what is still in it running, as a
// backgrounded child outlives sh.
func (j *Job) Release() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 {
		return nil
	}
	err := j.limit(0)
	_ = windows.CloseHandle(j.handle)
	j.handle = 0
	return err
}

func (j *Job) limit(flags uint32) error {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = flags
	_, err := windows.SetInformationJobObject(j.handle, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))) //nolint:gosec // the Win32 call takes a pointer and a size
	if err != nil {
		return fmt.Errorf("winjob: %w", err)
	}
	return nil
}
