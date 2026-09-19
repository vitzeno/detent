package reduce

import (
	"strconv"
	"strings"
)

// RegisterUnix wires up the reducers capabilities/unix.yaml's actions
// name. Mirrors exec.RegisterUnix — same registration discipline, one
// package per concern (schema vs. execution vs. reduction).
func RegisterUnix(r *Registry) {
	r.Register("path_lines", PathLines)
	r.Register("text_summary", TextSummary)
	r.Register("process_lines", ProcessLines)
	r.Register("port_listener_lines", PortListenerLines)
	r.Register("disk_usage_lines", DiskUsageLines)
	r.Register("git_status_lines", GitStatusLines)
	r.Register("git_log_lines", GitLogLines)
}

// PathLines reduces output that is one path per line — exactly what
// unix__list_files produces, and later find_files too. A trailing "/"
// marks a directory (the same convention exec's listFiles handler uses),
// kept in Facts for display but stripped from the typed Value, since a
// "path" arg type expects the real path, not a display suffix.
func PathLines(output string) Result {
	if strings.TrimSpace(output) == "" {
		return Result{Empty: true, Facts: map[string]any{"count": 0}}
	}

	lines := strings.Split(output, "\n")
	paths := make([]string, 0, len(lines))
	values := make([]Value, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		paths = append(paths, line)
		values = append(values, Value{Type: "path", Value: strings.TrimSuffix(line, "/")})
	}

	if len(paths) == 0 {
		return Result{Empty: true, Facts: map[string]any{"count": 0}}
	}
	return Result{
		Facts:  map[string]any{"count": len(paths), "paths": paths},
		Values: values,
	}
}

// previewLines caps how much of a text capability's output rides along in
// Facts — a preview for the human render to point at, not a substitute for
// the full content the viewport always shows (§4.4: "reduce for state,
// never for the human").
const previewLines = 3

// TextSummary reduces free-text output (unix__read_file) to structural
// facts, without hoarding the full content in state. This is Layer 1's
// honest ceiling for unstructured text: actual relevance-to-the-goal
// filtering is Layer 2's job (a semantic reducer, which needs a Jev call
// and isn't built until a capability needs it).
func TextSummary(output string) Result {
	if output == "" {
		return Result{Empty: true, Facts: map[string]any{"bytes": 0, "lines": 0}}
	}

	lines := strings.Split(output, "\n")
	preview := lines
	if len(preview) > previewLines {
		preview = preview[:previewLines]
	}

	return Result{
		Facts: map[string]any{
			"bytes":   len(output),
			"lines":   len(lines),
			"preview": strings.Join(preview, "\n"),
		},
	}
}

// maxProcessRows caps how many rows ProcessLines carries into state.
// Found necessary by running this live: even scoped to one user (exec's
// processList handler uses `ps -U`, not `-ax`), a normal desktop can have
// 700+ processes — unlike PathLines, whose input naturally scopes to one
// directory's size, `ps` output scales with the whole machine, and
// without this cap a single finding blew straight through the 8k
// state-token budget (§4.5) on the very first step. Goal-relevant
// narrowing among whatever survives this cap is extract's job (it has the
// goal text; this reducer doesn't), not this reducer's.
const maxProcessRows = 50

// ProcessLines reduces unix__process_list's output — one process per line,
// `pid= user= comm=` columns (matching exec's processList handler's `ps`
// invocation), into structured facts and typed pid Values.
//
// Unlike PathLines' scalar Value, each pid Value here carries a
// map[string]any (pid, owner, cmd) rather than a bare int — gate.ValidatePID
// (§7) needs the owner to check "not owned by the current user," and this
// is the only reducer that has that information at reduction time. The
// loop derives gate.ProcessInfo directly from these map-shaped values
// rather than tracking a second, parallel "known processes" cache.
func ProcessLines(output string) Result {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")

	var rows []map[string]any
	var values []Value
	total := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue // malformed row — skip rather than guess
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		total++
		if total > maxProcessRows {
			continue // keep counting for "truncated"/"total_found" below, but stop collecting
		}
		owner := fields[1]
		cmd := strings.Join(fields[2:], " ")

		rows = append(rows, map[string]any{"pid": pid, "owner": owner, "cmd": cmd})
		values = append(values, Value{Type: "pid", Value: map[string]any{"pid": pid, "owner": owner, "cmd": cmd}})
	}

	if total == 0 {
		return Result{Empty: true, Facts: map[string]any{"count": 0}}
	}
	facts := map[string]any{"count": len(rows), "processes": rows}
	if total > maxProcessRows {
		facts["truncated"] = true
		facts["total_found"] = total
	}
	return Result{Facts: facts, Values: values}
}
