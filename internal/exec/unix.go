package exec

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"os/user"
	"sort"
	"strings"
	"syscall"
)

// RegisterUnix wires up handlers for every action in capabilities/unix.yaml.
// Called once at startup (cmd/detent/main.go), mirroring how
// capabilities.LoadFile is called once to load the schema side.
func RegisterUnix(r *Registry) {
	r.Register("unix__list_files", listFiles)
	r.Register("unix__read_file", readFile)
	r.Register("unix__process_list", processList)
	r.Register("unix__kill_process", killProcess)
	r.Register("unix__port_listeners", portListeners)
	r.Register("unix__disk_usage", diskUsage)
	r.Register("unix__find_files", findFiles)
	r.Register("unix__tail_log", tailLog)
	r.Register("unix__git_status", gitStatus)
	r.Register("unix__git_log", gitLog)
}

// optionalPath reads an optional "path" arg, defaulting to ".", the same
// convention listFiles/diskUsage/findFiles all share.
func optionalPath(args Args, action string) (string, error) {
	v, ok := args["path"]
	if !ok {
		return ".", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: path must be a string, got %T", action, v)
	}
	return s, nil
}

// listFiles and readFile use Go's stdlib directly rather than shelling out
// to `ls`/`cat` — there is no subprocess here to assemble a string for, and
// §7 Layer 1's "no shell string is ever assembled" rule is trivially true
// when there's no shell involved at all. Capabilities that must shell out
// (process_list, git_status, later steps) are exactly where that rule earns
// its keep: exec.Command with a fixed binary and typed argv, never this.
func listFiles(_ context.Context, args Args) (Result, error) {
	path := "."
	if v, ok := args["path"]; ok {
		s, ok := v.(string)
		if !ok {
			return Result{}, fmt.Errorf("unix__list_files: path must be a string, got %T", v)
		}
		path = s
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return Result{}, fmt.Errorf("unix__list_files: %w", err)
	}

	names := make([]string, len(entries))
	for i, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names[i] = name
	}
	sort.Strings(names)

	return Result{Output: strings.Join(names, "\n")}, nil
}

func readFile(_ context.Context, args Args) (Result, error) {
	v, ok := args["path"]
	if !ok {
		return Result{}, fmt.Errorf("unix__read_file: path is required")
	}
	path, ok := v.(string)
	if !ok {
		return Result{}, fmt.Errorf("unix__read_file: path must be a string, got %T", v)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("unix__read_file: %w", err)
	}

	return Result{Output: string(data)}, nil
}

// processList is the first handler that actually shells out — §7 Layer
// 1's "fixed binary and typed argv elements, no shell string ever
// assembled" rule finally has something real to hold to: osexec.Command
// takes "ps" and three literal argv elements, never a string built from
// anything a goal or a Judge supplied.
//
// Scoped to the current user (-U), not every process on the machine
// (-ax): gate.ValidatePID (§7) rejects any pid not owned by the current
// user anyway, so listing other users' processes only adds noise a real
// run found blows the state-token budget on the very first step (a busy
// desktop easily has 700+ processes even scoped to one user — reduce.
// ProcessLines' cap is the second, defensive layer against that).
func processList(ctx context.Context, _ Args) (Result, error) {
	u, err := user.Current()
	if err != nil {
		return Result{}, fmt.Errorf("unix__process_list: %w", err)
	}
	cmd := osexec.CommandContext(ctx, "ps", "-U", u.Username, "-o", "pid=,user=,comm=")
	out, err := cmd.Output()
	if err != nil {
		return Result{}, fmt.Errorf("unix__process_list: %w", err)
	}
	return Result{Output: string(out)}, nil
}

// killProcess is v1's one destructive handler. It sends SIGTERM (a
// graceful request to exit), not SIGKILL — the gate (§7 Layer 2,
// gate.ValidatePID) and the confirm dialog (§4.3) are what make this safe
// to call at all; this function trusts pid completely, exactly as every
// other handler trusts its args, because validation is the gate's job,
// never the handler's (§7: "Layer 1 — allowlist ... Layer 2 — argument
// validation").
func killProcess(_ context.Context, args Args) (Result, error) {
	v, ok := args["pid"]
	if !ok {
		return Result{}, fmt.Errorf("unix__kill_process: pid is required")
	}
	pid, ok := v.(int)
	if !ok {
		return Result{}, fmt.Errorf("unix__kill_process: pid must be an int, got %T", v)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return Result{}, fmt.Errorf("unix__kill_process: %w", err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return Result{}, fmt.Errorf("unix__kill_process: %w", err)
	}
	return Result{Output: fmt.Sprintf("sent SIGTERM to pid %d", pid)}, nil
}

// portListeners lists every TCP socket in LISTEN state, with owner —
// -P -n keep ports and hosts numeric (no name/DNS resolution), matching
// processList's "typed argv, no shell string" discipline.
func portListeners(ctx context.Context, _ Args) (Result, error) {
	cmd := osexec.CommandContext(ctx, "lsof", "-iTCP", "-sTCP:LISTEN", "-P", "-n")
	out, err := cmd.Output()
	if err != nil {
		// lsof exits non-zero when nothing matches at all — not a real
		// error for this capability, just an empty result. A genuine
		// failure (lsof missing, permissions) surfaces as a different
		// error type (*exec.Error), not *exec.ExitError.
		if _, ok := err.(*osexec.ExitError); ok {
			return Result{Output: ""}, nil
		}
		return Result{}, fmt.Errorf("unix__port_listeners: %w", err)
	}
	return Result{Output: string(out)}, nil
}

// diskUsage reports one level of subdirectory/file sizes under path —
// -d 1 caps recursion depth so this stays a quick overview, not a full
// tree walk.
func diskUsage(ctx context.Context, args Args) (Result, error) {
	path, err := optionalPath(args, "unix__disk_usage")
	if err != nil {
		return Result{}, err
	}
	cmd := osexec.CommandContext(ctx, "du", "-h", "-d", "1", path)
	out, err := cmd.Output()
	if err != nil {
		return Result{}, fmt.Errorf("unix__disk_usage: %w", err)
	}
	return Result{Output: string(out)}, nil
}

// findFiles recursively lists files under path — the "search a tree"
// counterpart to listFiles' single-directory listing. Excludes .git the
// same way a person would when eyeballing a repo's real files; no
// name/age/size pattern matching yet (that needs a literal search-term
// argument, which extraction doesn't support for any capability today —
// a real, separate gap, not silently pretended away).
func findFiles(ctx context.Context, args Args) (Result, error) {
	path, err := optionalPath(args, "unix__find_files")
	if err != nil {
		return Result{}, err
	}
	cmd := osexec.CommandContext(ctx, "find", path, "-type", "f", "-not", "-path", "*/.git/*")
	out, err := cmd.Output()
	if err != nil {
		return Result{}, fmt.Errorf("unix__find_files: %w", err)
	}
	return Result{Output: string(out)}, nil
}

func tailLog(ctx context.Context, args Args) (Result, error) {
	v, ok := args["path"]
	if !ok {
		return Result{}, fmt.Errorf("unix__tail_log: path is required")
	}
	path, ok := v.(string)
	if !ok {
		return Result{}, fmt.Errorf("unix__tail_log: path must be a string, got %T", v)
	}
	cmd := osexec.CommandContext(ctx, "tail", "-n", "50", path)
	out, err := cmd.Output()
	if err != nil {
		return Result{}, fmt.Errorf("unix__tail_log: %w", err)
	}
	return Result{Output: string(out)}, nil
}

func gitStatus(ctx context.Context, _ Args) (Result, error) {
	cmd := osexec.CommandContext(ctx, "git", "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return Result{}, fmt.Errorf("unix__git_status: %w", err)
	}
	return Result{Output: string(out)}, nil
}

func gitLog(ctx context.Context, _ Args) (Result, error) {
	cmd := osexec.CommandContext(ctx, "git", "log", "--oneline", "-n", "20")
	out, err := cmd.Output()
	if err != nil {
		return Result{}, fmt.Errorf("unix__git_log: %w", err)
	}
	return Result{Output: string(out)}, nil
}
