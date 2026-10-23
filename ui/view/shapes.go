package view

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// node is one path in a ```tree block, a directory when it has kids.
type node struct {
	name, note string
	kids       []*node
}

func (n *node) child(name string) *node {
	for _, k := range n.kids {
		if k.name == name {
			return k
		}
	}
	k := &node{name: name}
	n.kids = append(n.kids, k)
	return k
}

// noteGap is what parts a path from its note: two spaces or a tab, since a single
// space can sit inside a name.
var noteGap = regexp.MustCompile(`\t|\s{2,}`)

// diffCount is a word of a note that counts lines added or removed.
var diffCount = regexp.MustCompile(`^[+-]\d+$`)

// arrows are what a ```flow line may join its steps with.
var arrows = regexp.MustCompile(`\s*(->|→|=>)\s*`)

// alertKinds are GitHub's alerts, which a model already writes as a quote opening [!NOTE].
var alertKinds = map[string]struct {
	glyph, title string
	tone         tone
}{
	"note":      {"ℹ", "Note", toneAccent},
	"tip":       {"★", "Tip", toneSafe},
	"important": {"❖", "Important", toneVivid},
	"warning":   {"▲", "Warning", toneCaution},
	"caution":   {"✖", "Caution", toneDanger},
}

var alertOpen = regexp.MustCompile(`^\s{0,3}>\s*\[!([A-Za-z]+)\]\s*(.*)$`)

// drawTree draws paths as the directories they share, each with its note, a +12
// or -3 in it coloured as a diff's counts are.
func drawTree(body string, s styles, width int) (drawn, error) {
	root := &node{}
	for line := range strings.SplitSeq(strings.ReplaceAll(body, "`", ""), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if line == "" {
			continue
		}
		path, note := line, ""
		if at := noteGap.FindStringIndex(line); at != nil {
			path, note = line[:at[0]], strings.TrimSpace(line[at[1]:])
		}
		n := root
		for part := range strings.SplitSeq(strings.Trim(path, "/"), "/") {
			if part != "" {
				n = n.child(part)
			}
		}
		n.note = note
	}
	if len(root.kids) == 0 {
		return nil, fmt.Errorf("the block is empty")
	}
	type row struct{ rail, name, note string }
	var rows []row
	var walk func(n *node, rail string, top bool, last bool)
	walk = func(n *node, rail string, top, last bool) {
		name, kids := n.name, n.kids
		// A directory holding only one directory reads as one path, as git prints it.
		for len(kids) == 1 && len(kids[0].kids) > 0 && n.note == "" {
			name, kids = name+"/"+kids[0].name, kids[0].kids
		}
		branch, below := "", ""
		if !top {
			branch, below = "├── ", "│   "
			if last {
				branch, below = "└── ", "    "
			}
		}
		style := s.text
		if len(kids) > 0 {
			name, style = name+"/", s.accent
		}
		rows = append(rows, row{rail: s.faint.Render(rail + branch), name: style.Render(name), note: n.note})
		for i, k := range kids {
			walk(k, rail+below, false, i == len(kids)-1)
		}
	}
	for _, k := range root.kids {
		walk(k, "", true, false)
	}
	col := 0
	for _, r := range rows {
		col = max(col, ansi.StringWidth(r.rail+r.name))
	}
	col = min(col+2, width*2/3)
	// It draws down its branches as it arrives, each row's note landing just after it.
	return func(p float64) []string {
		lines := make([]string, len(rows))
		for i, r := range rows {
			at := arrival(i, len(rows))
			if p < at {
				continue
			}
			lines[i] = r.rail + r.name
			if r.note != "" && landed(p, at) {
				lines[i] = pad(ansi.Truncate(lines[i], max(col-2, 1), "…"), col) + diffNote(r.note, s)
			}
		}
		return lines
	}, nil
}

func diffNote(note string, s styles) string {
	words := strings.Fields(note)
	for i, w := range words {
		switch {
		case diffCount.MatchString(w) && w[0] == '+':
			words[i] = s.safe.Render(w)
		case diffCount.MatchString(w):
			words[i] = s.danger.Render(w)
		default:
			words[i] = s.faint.Render(w)
		}
	}
	return strings.Join(words, " ")
}

// drawFlow draws each line's chain as boxes joined by arrows, across when it fits
// and down when it does not, each box arriving after the one before it.
func drawFlow(body string, s styles, width int) (drawn, error) {
	var chains [][]string
	n, open := 0, false
	for line := range strings.SplitSeq(strings.ReplaceAll(body, "`", ""), "\n") {
		n++
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if line == "" {
			continue
		}
		// A chain written a step a line, each after an arrow, is one chain.
		lead := arrows.FindStringIndex(line)
		continues := len(chains) > 0 && (open || lead != nil && lead[0] == 0)
		if lead != nil && lead[0] == 0 {
			line = line[lead[1]:]
		}
		trail := arrows.FindAllStringIndex(line, -1)
		open = len(trail) > 0 && trail[len(trail)-1][1] == len(line)
		if open {
			line = line[:trail[len(trail)-1][0]]
		}
		steps := arrows.Split(line, -1)
		if slices.Contains(steps, "") {
			return nil, fmt.Errorf("line %d: a step between arrows is empty", n)
		}
		if continues {
			chains[len(chains)-1] = append(chains[len(chains)-1], steps...)
			continue
		}
		chains = append(chains, steps)
	}
	if len(chains) == 0 {
		return nil, fmt.Errorf("the block is empty")
	}
	return func(p float64) []string {
		var lines []string
		for i, chain := range chains {
			if i > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, flowLines(chain, s, width, p)...)
		}
		return lines
	}, nil
}

// flowLines draws one chain at p of its arrival: box i is there once the chain has got
// that far along, and the arrow into it is drawn out cell by cell on the way.
func flowLines(chain []string, s styles, width int, p float64) []string {
	along := p * float64(len(chain)-1)
	if p >= 1 {
		along = float64(len(chain))
	}
	reached := func(i int) bool { return along >= float64(i) }
	// arrowCells is how many of an arrow's three cells into box i are out.
	arrowCells := func(i int) int { return min(3, max(0, int(math.Ceil((along-float64(i-1))*3)))) }
	labels := make([]string, len(chain))
	across := 0
	for i, step := range chain {
		labels[i] = ansi.Truncate(step, max(width-4, 1), "…")
		across += ansi.StringWidth(labels[i]) + 4
	}
	across += 3 * (len(chain) - 1)
	if across <= width {
		rows := make([]string, 3)
		for i, label := range labels {
			box := flowBox(label, s)
			arrow := [3]string{"   ", s.accent.Render(pad([]string{"", "─", "──", "──▶"}[arrowCells(i)], 3)), "   "}
			if !reached(i) {
				w := ansi.StringWidth(box[0])
				box = [3]string{strings.Repeat(" ", w), strings.Repeat(" ", w), strings.Repeat(" ", w)}
			}
			for r := range rows {
				if i > 0 {
					rows[r] += arrow[r]
				}
				rows[r] += box[r]
			}
		}
		return rows
	}
	var rows []string
	for i, label := range labels {
		if i > 0 {
			down := []string{"", ""}
			if arrowCells(i) > 0 {
				down[0] = s.accent.Render("  │")
			}
			if arrowCells(i) == 3 {
				down[1] = s.accent.Render("  ▼")
			}
			rows = append(rows, down...)
		}
		box := flowBox(label, s)
		if !reached(i) {
			box = [3]string{}
		}
		rows = append(rows, box[:]...)
	}
	return rows
}

func flowBox(label string, s styles) [3]string {
	edge := strings.Repeat("─", ansi.StringWidth(label)+2)
	return [3]string{
		s.faint.Render("╭" + edge + "╮"),
		s.faint.Render("│ ") + s.heading.Render(label) + s.faint.Render(" │"),
		s.faint.Render("╰" + edge + "╯"),
	}
}

// alertOpens reads a quote opening a callout, with any text after its marker.
func alertOpens(line string) (kind, rest string) {
	m := alertOpen.FindStringSubmatch(line)
	if m == nil {
		return "", ""
	}
	if _, ok := alertKinds[strings.ToLower(m[1])]; !ok {
		return "", ""
	}
	return strings.ToLower(m[1]), m[2]
}

// quoted is a quote line's text, without its marker.
func quoted(line string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimLeft(line, " "), ">")
	return strings.TrimPrefix(rest, " "), ok
}

// callout draws an alert as a coloured rail beside its title and its markdown, the
// rail sliding down as it arrives and carrying the text with it.
func callout(kind, body string, s styles, style string, width int) drawn {
	a := alertKinds[kind]
	tone := s.of(a.tone)
	rail := margin + tone.Render("▌") + " "
	lines := []string{rail + tone.Bold(true).Render(a.glyph+" "+a.title)}
	for _, line := range prose(body, style, max(width-ansi.StringWidth(rail)+len(margin), 1)) {
		lines = append(lines, rail+strings.TrimPrefix(line, margin))
	}
	return func(p float64) []string {
		out := make([]string, len(lines))
		for i, line := range lines {
			if p >= arrival(i, len(lines)) {
				out[i] = line
			}
		}
		return out
	}
}
