package reduce

import (
	"strconv"
	"strings"
)

// PortListenerLines reduces unix__port_listeners' `lsof -iTCP -sTCP:LISTEN
// -P -n` output — a header row, then "COMMAND PID USER FD TYPE DEVICE
// SIZE/OFF NODE NAME" columns where NAME is "*:PORT" or "host:PORT",
// always followed by a literal "(LISTEN)" — into structured facts and
// typed pid Values.
//
// Like ProcessLines, each pid Value carries owner (lsof's USER column),
// not a bare int — gate.ValidatePID (§7) needs it, and this is one of
// only two reducers with that information at reduction time.
func PortListenerLines(output string) Result {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")

	var rows []map[string]any
	var values []Value
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "COMMAND") {
			continue // header row
		}
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[len(fields)-1] != "(LISTEN)" {
			continue // malformed or not actually a LISTEN row — skip rather than guess
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		cmd := fields[0]
		owner := fields[2]
		name := fields[len(fields)-2]
		port := name
		if idx := strings.LastIndex(name, ":"); idx >= 0 {
			port = name[idx+1:]
		}

		rows = append(rows, map[string]any{"port": port, "pid": pid, "owner": owner, "cmd": cmd})
		values = append(values, Value{Type: "pid", Value: map[string]any{"pid": pid, "owner": owner, "cmd": cmd}})
	}

	if len(rows) == 0 {
		return Result{Empty: true, Facts: map[string]any{"count": 0}}
	}
	return Result{Facts: map[string]any{"count": len(rows), "listeners": rows}, Values: values}
}

// DiskUsageLines reduces unix__disk_usage's `du -h -d 1` output — one
// "SIZE\tPATH" row per line.
func DiskUsageLines(output string) Result {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")

	var rows []map[string]any
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue // malformed row — skip rather than guess
		}
		rows = append(rows, map[string]any{"size": strings.TrimSpace(parts[0]), "path": parts[1]})
	}

	if len(rows) == 0 {
		return Result{Empty: true, Facts: map[string]any{"count": 0}}
	}
	return Result{Facts: map[string]any{"count": len(rows), "usage": rows}}
}

// GitStatusLines reduces unix__git_status' `git status --porcelain`
// output — porcelain v1's fixed "XY PATH" shape (two status characters,
// a space, then the path). Values carry the path type so a later step
// (e.g. read_file on the one changed file a goal means) can resolve
// against real, currently-changed files instead of a goal-text guess.
func GitStatusLines(output string) Result {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")

	var rows []map[string]any
	var values []Value
	for _, line := range lines {
		if len(line) < 4 {
			continue // shorter than the shortest real porcelain row
		}
		status := strings.TrimSpace(line[:2])
		path := strings.TrimSpace(line[3:])
		if path == "" {
			continue
		}
		rows = append(rows, map[string]any{"status": status, "path": path})
		values = append(values, Value{Type: "path", Value: path})
	}

	if len(rows) == 0 {
		return Result{Empty: true, Facts: map[string]any{"count": 0}}
	}
	return Result{Facts: map[string]any{"count": len(rows), "changes": rows}, Values: values}
}

// GitLogLines reduces unix__git_log's `git log --oneline` output —
// "hash subject" per line.
func GitLogLines(output string) Result {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")

	var rows []map[string]any
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		hash, subject, found := strings.Cut(line, " ")
		if !found {
			continue // malformed row — skip rather than guess
		}
		rows = append(rows, map[string]any{"hash": hash, "subject": subject})
	}

	if len(rows) == 0 {
		return Result{Empty: true, Facts: map[string]any{"count": 0}}
	}
	return Result{Facts: map[string]any{"count": len(rows), "commits": rows}}
}
