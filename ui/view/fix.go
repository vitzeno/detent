package view

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/vitzeno/detent/ui/syntax"
)

// diffRow is one line of a ```diff block: a file's header, a hunk's, or a line of code
// the change kept (' '), added ('+') or removed ('-').
type diffRow struct {
	op         byte
	text, path string
}

// causeKeys are a ```cause block's lines, in the order they are drawn.
var causeKeys = []string{"error", "at", "why", "fix"}

// diffNoise is what git prints about a file that is not its change.
var diffNoise = []string{"diff --git ", "index ", "new file mode", "deleted file mode", "similarity ", "rename "}

// drawDiff draws a unified diff as /review does. Each line is read by its marker, not
// by the hunk's counts, since a model writing a diff miscounts them.
func drawDiff(body string, s styles, width int) (drawn, error) {
	rows := diffRows(body)
	changed := false
	for _, r := range rows {
		changed = changed || r.op == '+' || r.op == '-'
	}
	if !changed {
		return nil, errors.New("a diff adds or removes at least one line")
	}
	code := diffCode(rows, s)
	return func(p float64) []string {
		lines := make([]string, len(rows))
		for i, r := range rows {
			switch r.op {
			case 'f':
				lines[i] = s.accent.Bold(true).Render(ansi.Truncate(r.path, width, "…"))
			case 'h':
				lines[i] = s.faint.Render(ansi.Truncate(r.text, width, "…"))
			default:
				lines[i] = syntax.Tinted(r.op, code[i], width, s.theme)
			}
			// What the change added sweeps in, as though being written.
			if r.op == '+' && p < 1 {
				lines[i] = ansi.Truncate(lines[i], max(1, int(p*float64(width))), "") + "\x1b[49m"
			}
		}
		return lines
	}, nil
}

// diffRows reads a diff's lines. A --- or +++ line before any code or hunk names a file,
// as a model often writes only one, and a line with no marker is code the change kept.
func diffRows(body string) []diffRow {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	var rows []diffRow
	path, header := "", true
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		switch {
		case slices.ContainsFunc(diffNoise, func(p string) bool { return strings.HasPrefix(line, p) }):
			header = true
			continue
		case header && (strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ")):
			path = diffPath(line[4:])
			if strings.HasPrefix(line, "--- ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ ") {
				if after := diffPath(lines[i+1][4:]); after != "/dev/null" {
					path = after
				}
				i++
			}
			rows = append(rows, diffRow{op: 'f', path: path})
		case strings.HasPrefix(line, "@@"):
			header = false
			rows = append(rows, diffRow{op: 'h', text: line})
		case strings.HasPrefix(line, "+"), strings.HasPrefix(line, "-"), strings.HasPrefix(line, " "):
			header = false
			rows = append(rows, diffRow{op: line[0], text: line[1:], path: path})
		case strings.HasPrefix(line, `\`):
			rows = append(rows, diffRow{op: 'h', text: line})
		default:
			header = false
			rows = append(rows, diffRow{op: ' ', text: line, path: path})
		}
	}
	return rows
}

// diffCode colours each file's lines of code as one run, so a comment or string
// spanning lines is coloured right, and leaves a file no lexer knows plain.
func diffCode(rows []diffRow, s styles) []string {
	code := make([]string, len(rows))
	for start := 0; start < len(rows); {
		end := start
		var texts []string
		for end < len(rows) && rows[end].op != 'f' {
			// A hunk's header is no code, and would throw the lexer off what follows.
			text := rows[end].text
			if rows[end].op == 'h' {
				text = ""
			}
			texts = append(texts, text)
			end++
		}
		coloured := syntax.Hunk(rows[min(start, len(rows)-1)].path, texts, s.theme.Syntax)
		for i := start; i < end; i++ {
			code[i] = rows[i].text
			if coloured != nil {
				code[i] = coloured[i-start]
			}
		}
		start = end + 1
	}
	return code
}

func diffPath(name string) string {
	name, _, _ = strings.Cut(strings.TrimSpace(name), "\t")
	if strings.HasPrefix(name, "a/") || strings.HasPrefix(name, "b/") {
		return name[2:]
	}
	return name
}

// drawCause draws why something failed as a card: the error, where it happened, why,
// and the fix, which lands last as the card arrives.
func drawCause(body string, s styles, width int) (drawn, error) {
	parts, err := causeParts(body)
	if err != nil {
		return nil, err
	}
	rail := s.danger.Render("▌") + " "
	inner := max(width-2, 1)
	type row struct {
		line  string
		entry int
	}
	var rows []row
	entry := 0
	for _, key := range causeKeys {
		text := parts[key]
		if len(text) == 0 {
			continue
		}
		if key == "why" && len(rows) > 0 {
			rows = append(rows, row{entry: entry})
		}
		for i, line := range text {
			var lines []string
			switch {
			case key == "error" && i == 0:
				lines = wrap(line, inner-2)
				lines[0] = s.danger.Bold(true).Render("✗ " + lines[0])
				for j := 1; j < len(lines); j++ {
					lines[j] = "  " + s.danger.Bold(true).Render(lines[j])
				}
			case key == "error":
				lines = []string{"  " + s.faint.Render(ansi.Truncate(line, inner-2, "…"))}
			case key == "at":
				lines = []string{"  " + s.faint.Render("at ") + s.accent.Render(ansi.Truncate(line, inner-5, "…"))}
			default:
				label, style := s.faint.Render(key+"  "), s.text
				if key == "fix" {
					label, style = s.safe.Bold(true).Render(key+"  "), s.safe
				}
				if i > 0 {
					label = "     "
				}
				lines = wrap(line, inner-5)
				for j := range lines {
					lines[j] = label + style.Render(lines[j])
					label = "     "
				}
			}
			for _, l := range lines {
				rows = append(rows, row{line: l, entry: entry})
			}
		}
		entry++
	}
	return func(p float64) []string {
		lines := make([]string, len(rows))
		for i, r := range rows {
			lines[i] = rail + r.line
			if p < 1 && p < float64(r.entry)/float64(entry) {
				lines[i] = rail
			}
		}
		return lines
	}, nil
}

// causeParts reads "error: …", "at: …", "why: …" and "fix: …" lines, a line under
// one continuing it, as a stack under an error does.
func causeParts(body string) (map[string][]string, error) {
	parts := map[string][]string{}
	last, n := "", 0
	for line := range strings.SplitSeq(strings.ReplaceAll(body, "`", ""), "\n") {
		n++
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if text == "" {
			continue
		}
		key, rest, _ := strings.Cut(text, ":")
		if slices.Contains(causeKeys, key) {
			last = key
			parts[key] = append(parts[key], strings.TrimSpace(rest))
			continue
		}
		if last == "" {
			return nil, fmt.Errorf("line %d: start each line with error, at, why or fix and a colon", n)
		}
		parts[last] = append(parts[last], text)
	}
	if len(parts["error"]) == 0 && len(parts["why"]) == 0 {
		return nil, errors.New("a cause says what failed in its error line or why in its why line")
	}
	return parts, nil
}
