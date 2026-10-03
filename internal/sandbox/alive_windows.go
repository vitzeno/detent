package sandbox

// processAlive cannot ask on Windows, where the sandbox does not run, so
// only a running task keeps a container there.
func processAlive(int) bool { return false }
