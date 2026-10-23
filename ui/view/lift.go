package view

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The markdown constructs a view lifts out of glamour's hands, at the top level only.
var (
	headingLine = regexp.MustCompile(`^#{1,6}(\s|$)`)
	listItem    = regexp.MustCompile(`^([-*+]|\d{1,3}[.)]) `)
	taskItem    = regexp.MustCompile(`^\[([ xX])\]\s+(.*)$`)
	tableRule   = regexp.MustCompile(`^\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$`)
)

var (
	// boldSpan is a bold run of prose, and numberToken a number in one.
	boldSpan    = regexp.MustCompile(`\*\*([^*\n]+)\*\*|__([^_\n]+)__`)
	numberToken = regexp.MustCompile(`\d[\d,]*(?:\.\d+)?`)
	// stamped is a version, a date or a time, whose parts are not amounts to count.
	stamped = regexp.MustCompile(`\d[.:/-]\d+[.:/-]\d|\d:\d`)
	// amountNote is all that follows an item's label when the item is a measure of it.
	amountNote = regexp.MustCompile(`^(?:[—–:-]\s*)?~?(\d[\d,]*(?:\.\d+)?)\s?([A-Za-z%]*)$`)
	// boldLabel opens an item named in bold, as "**TUI:** Bubble Tea" or "**TUI**: Bubble Tea".
	boldLabel = regexp.MustCompile(`^\*\*([^*:]{1,40}?):?\*\*\s*[:—–-]?\s*(.+)$`)
	// pathLike leads a path: a file with an extension or a directory, nothing spaced.
	pathLike = regexp.MustCompile(`^[\w.@~+-]*(/[\w.@~+-]*)*(\.[A-Za-z][\w]{0,7}|/)$`)
	// numericCell is a table cell holding one amount, unsigned, with an optional unit.
	numericCell = regexp.MustCompile(`^[~≈$€£]?\d[\d,]*(?:\.\d+)?\s?(?:%|[A-Za-zµ]{1,6})?$`)
	// unbarred names a column whose numbers label rows rather than measure them.
	unbarred = regexp.MustCompile(`(?i)^(#|id|no\.?|line|year|port|pid|version|rank|step|row|index)$`)
	// inlineMark is what a table cell's markdown may hold: code, bold or a link.
	inlineMark = regexp.MustCompile("`([^`]+)`|\\*\\*([^*]+)\\*\\*|\\[([^\\]]+)\\]\\([^)]*\\)")
)

// tableBar is how many cells a number's bar in a table may take.
const tableBar = 6

// lift cuts a markdown run into the pieces that arrive in their own way, a heading,
// a code block, a table, a task list or a list of paths, and the prose between them.
func lift(text string) []piece {
	lines := strings.SplitAfter(text, "\n")
	var out []piece
	var run strings.Builder
	flush := func() {
		if run.Len() > 0 {
			out = append(out, piece{kind: pieceProse, body: run.String()})
			run.Reset()
		}
	}
	take := func(p piece) {
		flush()
		out = append(out, p)
	}
	for i := 0; i < len(lines); {
		line := strings.TrimRight(lines[i], "\r\n")
		if m, _ := fence(line); m != "" {
			end := i + 1
			for end < len(lines) && !closes(strings.TrimRight(lines[end], "\r\n"), m) {
				end++
			}
			end = min(end+1, len(lines))
			take(piece{kind: pieceCode, body: strings.Join(lines[i:end], "")})
			i = end
			continue
		}
		if headingLine.MatchString(line) {
			take(piece{kind: pieceHeading, body: line + "\n"})
			i++
			continue
		}
		if strings.HasPrefix(line, "|") && i+1 < len(lines) && strings.Contains(lines[i+1], "|") &&
			tableRule.MatchString(strings.TrimSpace(lines[i+1])) {
			end := i + 2
			for end < len(lines) && strings.Contains(lines[end], "|") && strings.TrimSpace(lines[end]) != "" {
				end++
			}
			take(piece{kind: pieceTable, body: strings.Join(lines[i:end], "")})
			i = end
			continue
		}
		if listItem.MatchString(line) {
			end := i + 1
			for end < len(lines) && (listItem.MatchString(lines[end]) ||
				strings.HasPrefix(lines[end], " ") && strings.TrimSpace(lines[end]) != "") {
				end++
			}
			if p, ok := liftList(lines[i:end]); ok {
				take(p)
			} else {
				run.WriteString(strings.Join(lines[i:end], ""))
			}
			i = end
			continue
		}
		run.WriteString(lines[i])
		i++
	}
	flush()
	return out
}

// liftList reads a list of task boxes as steps, of measures in one unit as a bar chart,
// of values named in bold as facts, and of paths, one of them in a directory, as a tree.
func liftList(lines []string) (piece, bool) {
	source := strings.Join(lines, "")
	var items []string
	for _, line := range lines {
		marker := listItem.FindString(line)
		if marker == "" {
			return piece{}, false
		}
		items = append(items, strings.TrimSpace(line[len(marker):]))
	}
	if chart, ok := measures(items); ok {
		return piece{kind: pieceBlock, lang: "chart", body: chart, source: source}, true
	}
	if facts, ok := named(items); ok {
		return piece{kind: pieceBlock, lang: "facts", body: facts, source: source}, true
	}
	var steps, tree strings.Builder
	tasks, paths, nested := true, len(items) >= 2, false
	for _, item := range items {
		if m := taskItem.FindStringSubmatch(item); m != nil {
			state := "done"
			if m[1] == " " {
				state = "todo"
			}
			steps.WriteString("- " + state + ": " + m[2] + "\n")
		} else {
			tasks = false
		}
		path, note := leadingPath(item)
		if path == "" {
			paths = false
			continue
		}
		nested = nested || strings.Contains(strings.TrimSuffix(path, "/"), "/")
		tree.WriteString(path + "  " + strings.ReplaceAll(note, "**", "") + "\n")
	}
	switch {
	case tasks:
		return piece{kind: pieceBlock, lang: "steps", body: steps.String(), source: source}, true
	case paths && nested:
		return piece{kind: pieceBlock, lang: "tree", body: tree.String(), source: source}, true
	}
	return piece{}, false
}

// measures reads items such as "`view.go`: 167 lines" as one bar chart, when every
// item is a label and one amount, all in the same unit.
func measures(items []string) (string, bool) {
	if len(items) < 2 {
		return "", false
	}
	var data strings.Builder
	unit := ""
	for i, item := range items {
		label, rest := leadingLabel(item)
		m := amountNote.FindStringSubmatch(strings.TrimSpace(rest))
		if label == "" || m == nil || i > 0 && m[2] != unit {
			return "", false
		}
		unit = m[2]
		data.WriteString("  " + strconv.Quote(label) + ": " + strings.ReplaceAll(m[1], ",", "") + "\n")
	}
	return "type: bar\nunit: " + strconv.Quote(unit) + "\ndata:\n" + data.String(), true
}

// named reads items that each open with a name in bold as facts, two of them at least.
func named(items []string) (string, bool) {
	if len(items) < 2 {
		return "", false
	}
	var facts strings.Builder
	for _, item := range items {
		m := boldLabel.FindStringSubmatch(item)
		if m == nil {
			return "", false
		}
		facts.WriteString(strings.TrimSpace(m[1]) + ": " + strings.ReplaceAll(m[2], "**", "") + "\n")
	}
	return facts.String(), true
}

// leadingLabel splits an item into the label it opens with, in backticks, in bold or
// before a dash or colon, and the rest.
func leadingLabel(item string) (label, rest string) {
	switch {
	case strings.HasPrefix(item, "`"):
		label, rest, _ = strings.Cut(item[1:], "`")
	case strings.HasPrefix(item, "**"):
		label, rest, _ = strings.Cut(item[2:], "**")
	default:
		at := strings.IndexAny(item, "—–:")
		if dash := strings.Index(item, " - "); dash >= 0 && (at < 0 || dash < at) {
			at = dash + 1
		}
		if at <= 0 {
			return "", ""
		}
		label, rest = item[:at], item[at:]
	}
	return strings.TrimSpace(strings.TrimRight(label, ":")), rest
}

// leadingPath splits a list item into the path it opens with, in backticks, a link or
// bare, and the note after it. An item opening with anything else has no path.
func leadingPath(item string) (path, note string) {
	switch {
	case strings.HasPrefix(item, "`"):
		path, note, _ = strings.Cut(item[1:], "`")
	case strings.HasPrefix(item, "["):
		var rest string
		path, rest, _ = strings.Cut(item[1:], "]")
		if !strings.HasPrefix(rest, "(") {
			return "", ""
		}
		_, note, _ = strings.Cut(rest, ")")
	default:
		path, note, _ = strings.Cut(item, " ")
	}
	path = strings.TrimRight(path, ":,")
	if !pathLike.MatchString(path) {
		return "", ""
	}
	return path, strings.TrimSpace(strings.TrimLeft(note, " :—–-"))
}

// counting is prose whose bold numbers count up as it arrives, in place, so the line
// never moves while they do.
func counting(body, style string, width int) drawn {
	rest := prose(body, style, width)
	amounts := map[string]bool{}
	for _, m := range boldSpan.FindAllStringSubmatch(body, -1) {
		span := m[1] + m[2]
		if stamped.MatchString(span) {
			continue
		}
		for _, n := range numberToken.FindAllString(span, -1) {
			amounts[n] = true
		}
	}
	if len(amounts) == 0 {
		return still(rest)
	}
	return func(p float64) []string {
		if p >= 1 {
			return rest
		}
		out := make([]string, len(rest))
		for i, line := range rest {
			out[i] = outsideEscapes(line, func(text string) string {
				return numberToken.ReplaceAllStringFunc(text, func(n string) string {
					if !amounts[n] {
						return n
					}
					counted := countUp(n, p)
					return strings.Repeat(" ", max(0, len(n)-len(counted))) + counted
				})
			})
		}
		return out
	}
}

// shimmering draws a heading with a light passing across it as it arrives, and as
// glamour drew it once it rests.
func shimmering(rest []string, s styles) drawn {
	return func(p float64) []string {
		if p >= 1 {
			return rest
		}
		out := make([]string, len(rest))
		for i, line := range rest {
			runes := []rune(ansi.Strip(line))
			at := int(p*float64(len(runes)+6)) - 3
			lo, hi := min(max(at-1, 0), len(runes)), min(max(at+2, 0), len(runes))
			out[i] = s.heading.Render(string(runes[:lo])) + s.value.Render(string(runes[lo:hi])) +
				s.heading.Render(string(runes[hi:]))
		}
		return out
	}
}

// typing draws a code block from the left as it arrives, as though being typed out.
func typing(rest []string) drawn {
	widest := 0
	for _, line := range rest {
		widest = max(widest, ansi.StringWidth(line))
	}
	return func(p float64) []string {
		if p >= 1 {
			return rest
		}
		out := make([]string, len(rest))
		for i, line := range rest {
			out[i] = ansi.Truncate(line, int(p*float64(widest)), "")
		}
		return out
	}
}

// column is one column of a table: how it aligns, how wide it is, and for one whose
// cells are all amounts, the largest of them its bars scale to.
type column struct {
	align   byte
	width   int
	numeric bool
	hi      float64
}

// drawTable draws a table's rows arriving in turn, a column of amounts counting up with
// a bar in each cell, and gives up on one too wide for width, which glamour draws.
func drawTable(body string, s styles, width int) (drawn, bool) {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	header, rule := cellsOf(lines[0]), cellsOf(lines[1])
	if len(header) != len(rule) {
		return nil, false
	}
	cols := make([]column, len(rule))
	for j, r := range rule {
		cols[j].align = 'l'
		switch {
		case strings.HasPrefix(r, ":") && strings.HasSuffix(r, ":"):
			cols[j].align = 'c'
		case strings.HasSuffix(r, ":"):
			cols[j].align = 'r'
		}
	}
	var rows [][]string
	for _, line := range lines[2:] {
		row := cellsOf(line)
		rows = append(rows, append(row, make([]string, max(0, len(cols)-len(row)))...)[:len(cols)])
	}
	barW := tableBar
	for j := range cols {
		cols[j].numeric = len(rows) >= 2 && !unbarred.MatchString(plainCell(header[j]))
		for _, row := range rows {
			cell := plainCell(row[j])
			if !numericCell.MatchString(cell) {
				cols[j].numeric = false
				break
			}
			cols[j].hi = max(cols[j].hi, leadingNumber(cell))
		}
		if cols[j].numeric {
			cols[j].align = 'r'
		}
	}
	measure := func() int {
		total := 3 * (len(cols) - 1)
		for j := range cols {
			cols[j].width = ansi.StringWidth(plainCell(header[j]))
			for _, row := range rows {
				cols[j].width = max(cols[j].width, ansi.StringWidth(plainCell(row[j])))
			}
			if cols[j].numeric && barW > 0 {
				cols[j].width += 1 + barW
			}
			total += cols[j].width
		}
		return total
	}
	total := measure()
	if total > width && barW > 0 {
		barW = 0
		total = measure()
	}
	for total > width {
		widest := -1
		for j, c := range cols {
			if !c.numeric && c.width > 8 && (widest < 0 || c.width > cols[widest].width) {
				widest = j
			}
		}
		if widest < 0 {
			return nil, false
		}
		cols[widest].width--
		total--
	}
	bar := s.faint.Render(" │ ")
	draw := func(cells []string, style lipgloss.Style, p float64) string {
		parts := make([]string, len(cols))
		for j, c := range cols {
			text := plainCell(cells[j])
			if !c.numeric || style.GetBold() {
				parts[j] = aligned(ansi.Truncate(inline(cells[j], style, s), c.width, "…"), c.width, c.align)
				continue
			}
			value := countUp(text, p)
			if barW == 0 {
				parts[j] = aligned(style.Render(value), c.width, 'r')
				continue
			}
			fill := scaled(leadingNumber(text), c.hi, barW) * p
			parts[j] = aligned(style.Render(value), c.width-barW-1, 'r') + " " + pad(s.accent.Render(hbar(fill)), barW)
		}
		return strings.Join(parts, bar)
	}
	rules := make([]string, len(cols))
	for j, c := range cols {
		rules[j] = strings.Repeat("─", c.width)
	}
	ruled := s.faint.Render(strings.Join(rules, "─┼─"))
	return func(p float64) []string {
		lines := []string{draw(header, s.heading, 1), ruled}
		for i, row := range rows {
			if p < 1 && p < 0.5*float64(i)/float64(len(rows)) {
				lines = append(lines, "")
				continue
			}
			lines = append(lines, draw(row, s.text, p))
		}
		return lines
	}, true
}

// cellsOf splits a table row at its pipes, an escaped one staying in its cell.
func cellsOf(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
	var cells []string
	var cell strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			cell.WriteByte('|')
			i++
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(line[i])
		}
	}
	return append(cells, strings.TrimSpace(cell.String()))
}

// inline draws a cell's markdown: code in the accent, bold as bold, a link as its text.
func inline(text string, base lipgloss.Style, s styles) string {
	var b strings.Builder
	last := 0
	for _, m := range inlineMark.FindAllStringSubmatchIndex(text, -1) {
		b.WriteString(base.Render(text[last:m[0]]))
		switch {
		case m[2] >= 0:
			b.WriteString(s.accent.Render(text[m[2]:m[3]]))
		case m[4] >= 0:
			b.WriteString(base.Bold(true).Render(text[m[4]:m[5]]))
		default:
			b.WriteString(base.Render(text[m[6]:m[7]]))
		}
		last = m[1]
	}
	b.WriteString(base.Render(text[last:]))
	return b.String()
}

// plainCell is a cell's text as it reads, its markdown gone.
func plainCell(text string) string {
	return inlineMark.ReplaceAllString(text, "$1$2$3")
}

// leadingNumber is the amount a cell leads with, past any currency sign.
func leadingNumber(cell string) float64 {
	v, err := strconv.ParseFloat(strings.ReplaceAll(numberToken.FindString(cell), ",", ""), 64)
	if err != nil || math.Abs(v) > maxNumber {
		return 0
	}
	return v
}

func aligned(s string, w int, align byte) string {
	switch align {
	case 'r':
		return strings.Repeat(" ", max(0, w-ansi.StringWidth(s))) + s
	case 'c':
		return centre(s, w)
	}
	return pad(s, w)
}

// outsideEscapes applies f to a line's text and never to its escape sequences, whose
// parameters are digits too.
func outsideEscapes(line string, f func(string) string) string {
	var b strings.Builder
	for line != "" {
		at := strings.IndexByte(line, 0x1b)
		if at != 0 {
			if at < 0 {
				at = len(line)
			}
			b.WriteString(f(line[:at]))
			line = line[at:]
			continue
		}
		n := escapeLen(line)
		b.WriteString(line[:n])
		line = line[n:]
	}
	return b.String()
}

// escapeLen is how long the escape sequence opening s is: a CSI to its final byte,
// an OSC to its terminator, anything else its two bytes.
func escapeLen(s string) int {
	if len(s) < 2 {
		return len(s)
	}
	switch s[1] {
	case '[':
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']':
		if i := strings.IndexAny(s, "\x07\x1b\\"); i > 1 {
			if s[i] == 0x07 || s[i] == '\\' {
				return i + 1
			}
			return min(i+2, len(s))
		}
		return len(s)
	}
	return 2
}
