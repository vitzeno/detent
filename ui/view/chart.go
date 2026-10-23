package view

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"gopkg.in/yaml.v3"
)

// chart is a ```chart block. data is a map of label to number, a list of numbers
// for a line or sparkline, or for compare a map of label to one number per series.
type chart struct {
	Type   string    `yaml:"type"`
	Title  string    `yaml:"title"`
	Unit   string    `yaml:"unit"`
	Series []string  `yaml:"series"`
	Better string    `yaml:"better"`
	Data   yaml.Node `yaml:"data"`
}

type point struct {
	label string
	value float64
}

// group is one label of a compare chart, a value per series.
type group struct {
	label  string
	values []float64
}

// chartTypes are the types a chart may be.
var chartTypes = []string{"bar", "columns", "line", "sparkline", "gauge", "share", "compare"}

var sparkCells = []rune("▁▂▃▄▅▆▇█")

// ChartTypes names the types a chart may be, which the model must be told of.
func ChartTypes() []string { return slices.Clone(chartTypes) }

func drawChart(body string, s styles, width int) (drawn, error) {
	var c chart
	if err := decode(body, &c); err != nil {
		return nil, err
	}
	if c.Better != "" && c.Better != "lower" && c.Better != "higher" {
		return nil, fmt.Errorf("better is lower or higher, not %q", c.Better)
	}
	d, err := c.draw(s, width)
	if err != nil || c.Title == "" {
		return d, err
	}
	title := s.heading.Render(c.Title)
	return func(p float64) []string { return append([]string{title}, d(p)...) }, nil
}

func (c chart) draw(s styles, width int) (drawn, error) {
	if c.Type == "compare" {
		groups, err := series(c.Data)
		if err != nil {
			return nil, err
		}
		return compare(groups, c, s, width), nil
	}
	pts, err := points(c.Data)
	if err != nil {
		return nil, err
	}
	switch c.Type {
	case "bar":
		return bars(pts, c.Unit, s, width), nil
	case "columns":
		return columns(pts, c.Unit, s, width), nil
	case "line":
		return line(pts, c.Unit, s, width), nil
	case "sparkline":
		return sparkline(pts, c.Unit, s), nil
	case "gauge":
		return gauges(pts, s, width), nil
	case "share":
		return share(pts, c.Unit, s, width), nil
	}
	last := len(chartTypes) - 1
	return nil, fmt.Errorf("type %q is not %s or %s", c.Type, strings.Join(chartTypes[:last], ", "), chartTypes[last])
}

// bars scale to the largest value, so the longest bar is always the largest.
func bars(pts []point, unit string, s styles, width int) drawn {
	labelW, valueW, hi := 0, 0, 0.0
	for _, p := range pts {
		labelW = max(labelW, ansi.StringWidth(p.label))
		valueW = max(valueW, ansi.StringWidth(amount(p.value, unit)))
		hi = max(hi, p.value)
	}
	labelW = min(labelW, width/3)
	barW := width - labelW - valueW - 2
	return func(at float64) []string {
		lines := make([]string, 0, len(pts))
		for _, p := range pts {
			label := pad(ansi.Truncate(p.label, labelW, "…"), labelW)
			value := s.faint.Render(amountAt(p.value, at, unit))
			if barW < 4 {
				lines = append(lines, label+" "+value)
				continue
			}
			lines = append(lines, label+" "+pad(s.accent.Render(hbar(scaled(p.value, hi, barW)*at)), barW)+" "+value)
		}
		return lines
	}
}

// columns stand up from a baseline with their values above, or lie down as bars
// when there are too many to stand side by side.
func columns(pts []point, unit string, s styles, width int) drawn {
	const height = 8
	colW, hi := 0, 0.0
	for _, p := range pts {
		colW = max(colW, ansi.StringWidth(p.label), ansi.StringWidth(amount(p.value, unit)))
		hi = max(hi, p.value)
	}
	colW = min(colW, (width+1)/len(pts)-1)
	if colW < 2 {
		return bars(pts, unit, s, width)
	}
	barW := min(max(colW-2, 1), 4)
	return func(at float64) []string {
		lines := make([]string, height+2)
		for i, p := range pts {
			gap := " "
			if i == 0 {
				gap = ""
			}
			lines[0] += gap + centre(s.faint.Render(amountAt(p.value, at, unit)), colW)
			fill := scaled(p.value, hi, height) * at * 8
			for row := range height {
				cell := vbar(fill - float64((height-1-row)*8))
				lines[row+1] += gap + centre(s.accent.Render(strings.Repeat(cell, barW)), colW)
			}
			lines[height+1] += gap + centre(ansi.Truncate(p.label, colW, "…"), colW)
		}
		return lines
	}
}

// line plots in braille, two dots across and four down a cell, drawn from the
// left as it arrives. A map's first and last labels sit under its ends.
func line(pts []point, unit string, s styles, width int) drawn {
	const rows = 6
	lo, hi := extent(pts)
	top, bottom := amount(hi, unit), amount(lo, unit)
	axisW := max(ansi.StringWidth(top), ansi.StringWidth(bottom))
	cells := width - axisW - 2
	if cells < 8 || len(pts) < 2 {
		return sparkline(pts, unit, s)
	}
	dotsX, dotsY := cells*2, rows*4
	at := func(i int) (x, y int) {
		x = i * (dotsX - 1) / (len(pts) - 1)
		if hi > lo {
			y = int(math.Round((pts[i].value - lo) / (hi - lo) * float64(dotsY-1)))
		}
		return x, dotsY - 1 - y
	}
	return func(p float64) []string {
		grid := make([][]rune, rows)
		for r := range grid {
			grid[r] = []rune(strings.Repeat("⠀", cells))
		}
		reach := int(p * float64(dotsX-1))
		for i := 1; i < len(pts); i++ {
			x0, y0 := at(i - 1)
			x1, y1 := at(i)
			plot(grid, x0, y0, x1, y1, reach)
		}
		lines := make([]string, 0, rows+1)
		for r, cellsRow := range grid {
			label, tick := strings.Repeat(" ", axisW), " │"
			switch r {
			case 0:
				label, tick = pad(top, axisW), " ┤"
			case rows - 1:
				label, tick = pad(bottom, axisW), " ┤"
			}
			lines = append(lines, s.faint.Render(label+tick)+s.accent.Render(string(cellsRow)))
		}
		first, last := pts[0].label, pts[len(pts)-1].label
		under := strings.Repeat(" ", axisW+2) + first
		under += strings.Repeat(" ", max(1, width-ansi.StringWidth(under)-ansi.StringWidth(last))) + last
		return append(lines, s.faint.Render(under))
	}
}

// plot draws a line of braille dots from one point to the next, none past reach.
func plot(grid [][]rune, x0, y0, x1, y1, reach int) {
	steps := max(abs(x1-x0), abs(y1-y0), 1)
	for k := 0; k <= steps; k++ {
		x := x0 + (x1-x0)*k/steps
		y := y0 + (y1-y0)*k/steps
		if x > reach {
			return
		}
		grid[y/4][x/2] |= brailleDot(x%2, y%4)
	}
}

// brailleDot is the bit a dot sets in a braille cell, which numbers its dots
// down the left column, down the right, then the bottom row.
func brailleDot(col, row int) rune {
	if row == 3 {
		return [2]rune{0x40, 0x80}[col]
	}
	return rune(1<<row) << (3 * col)
}

// sparkline is drawn a cell at a time from the left.
func sparkline(pts []point, unit string, s styles) drawn {
	lo, hi := extent(pts)
	cells := []rune(strip(pts))
	rangeNote := s.faint.Render("  " + amount(lo, unit) + "–" + amount(hi, unit))
	return func(p float64) []string {
		shown := int(math.Ceil(p * float64(len(cells))))
		return []string{s.accent.Render(string(cells[:shown])) + strings.Repeat(" ", len(cells)-shown) + rangeNote}
	}
}

// strip is pts as one cell each, its lowest a floor and its highest a full block.
func strip(pts []point) string {
	lo, hi := extent(pts)
	var out []rune
	for _, p := range pts {
		i := 0
		if hi > lo {
			i = int((p.value - lo) / (hi - lo) * float64(len(sparkCells)-1))
		}
		out = append(out, sparkCells[i])
	}
	return string(out)
}

// gauges read each value as a percentage, clamped to 0–100.
func gauges(pts []point, s styles, width int) drawn {
	labelW := 0
	for _, p := range pts {
		labelW = max(labelW, ansi.StringWidth(p.label))
	}
	labelW = min(labelW, width/3)
	cells := width - labelW - 6
	return func(at float64) []string {
		lines := make([]string, 0, len(pts))
		for _, p := range pts {
			v := min(max(p.value, 0), 100) * at
			label := pad(ansi.Truncate(p.label, labelW, "…"), labelW)
			pct := s.faint.Render(fmt.Sprintf(" %3.0f%%", v))
			if cells < 4 {
				lines = append(lines, label+pct)
				continue
			}
			fill := v * float64(cells) / 100
			n := ansi.StringWidth(hbar(fill))
			lines = append(lines, label+" "+s.accent.Render(hbar(fill))+
				s.faint.Render(strings.Repeat("░", cells-n))+pct)
		}
		return lines
	}
}

// share is one strip split between the parts of a whole, filled from the left,
// with a legend saying what each colour is and how much of the whole it takes.
func share(pts []point, unit string, s styles, width int) drawn {
	total, labelW, valueW := 0.0, 0, 0
	for _, p := range pts {
		total += max(p.value, 0)
		labelW = max(labelW, ansi.StringWidth(p.label))
		valueW = max(valueW, ansi.StringWidth(amount(p.value, unit)))
	}
	labelW = min(labelW, width/2)
	cuts := apportion(pts, total, width)
	legend := make([]string, 0, len(pts))
	for i, p := range pts {
		pct := 0.0
		if total > 0 {
			pct = max(p.value, 0) / total * 100
		}
		legend = append(legend, s.hue(i).Render("■")+" "+pad(ansi.Truncate(p.label, labelW, "…"), labelW)+"  "+
			s.faint.Render(pad(amount(p.value, unit), valueW)+fmt.Sprintf("  %3.0f%%", pct)))
	}
	return func(at float64) []string {
		reach := int(at * float64(width))
		var strip strings.Builder
		cell := 0
		for i, n := range cuts {
			k := max(0, min(n, reach-cell))
			strip.WriteString(s.hue(i).Render(strings.Repeat("█", k)))
			cell += n
		}
		strip.WriteString(strings.Repeat(" ", max(0, width-min(reach, cell))))
		return append([]string{strip.String()}, legend...)
	}
}

// apportion splits width cells by value, largest remainder first, so they add up
// exactly and any part with a value gets at least a cell where there is room.
func apportion(pts []point, total float64, width int) []int {
	cuts := make([]int, len(pts))
	if total <= 0 {
		return cuts
	}
	used := 0
	rest := make([]float64, len(pts))
	for i, p := range pts {
		exact := max(p.value, 0) / total * float64(width)
		cuts[i] = int(exact)
		if p.value > 0 && cuts[i] == 0 && len(pts) <= width {
			cuts[i] = 1
		}
		rest[i] = exact - float64(cuts[i])
		used += cuts[i]
	}
	for used < width {
		best := 0
		for i := range rest {
			if rest[i] > rest[best] {
				best = i
			}
		}
		cuts[best]++
		rest[best] = -1
		used++
	}
	for i := len(cuts) - 1; used > width && i >= 0; i-- {
		if cuts[i] > 1 {
			cuts[i]--
			used--
		}
	}
	return cuts
}

// compare draws each label's series as bars on one scale, the last against the
// first as a change, coloured when the block says which way is better.
func compare(groups []group, c chart, s styles, width int) drawn {
	labelW, valueW, hi, n := 0, 0, 0.0, 0
	for _, g := range groups {
		labelW = max(labelW, ansi.StringWidth(g.label))
		for _, v := range g.values {
			valueW = max(valueW, ansi.StringWidth(amount(v, c.Unit)))
			hi = max(hi, v)
		}
		n = max(n, len(g.values))
	}
	labelW = min(labelW, width/3)
	const changeW = 7
	barW := width - labelW - valueW - changeW - 3
	tone := func(i int) lipgloss.Style {
		switch {
		case n == 2 && i == 0:
			return s.faint
		case n == 2:
			return s.accent
		}
		return s.hue(i)
	}
	var legend []string
	if len(c.Series) > 0 {
		var keys []string
		for i, name := range c.Series {
			keys = append(keys, tone(i).Render("■")+" "+s.faint.Render(name))
		}
		legend = []string{strings.Repeat(" ", labelW+1) + strings.Join(keys, "  ")}
	}
	return func(at float64) []string {
		lines := slices.Clone(legend)
		for _, g := range groups {
			for i, v := range g.values {
				label := strings.Repeat(" ", labelW)
				if i == 0 {
					label = pad(ansi.Truncate(g.label, labelW, "…"), labelW)
				}
				value := s.faint.Render(pad(amountAt(v, at, c.Unit), valueW))
				change := ""
				if i == len(g.values)-1 && i > 0 && at >= 1 {
					change = c.change(g.values[0], v, s)
				}
				if barW < 4 {
					lines = append(lines, label+" "+value+" "+change)
					continue
				}
				lines = append(lines, label+" "+pad(tone(i).Render(hbar(scaled(v, hi, barW)*at)), barW)+" "+value+" "+change)
			}
		}
		return lines
	}
}

// change is the last series against the first, safe or danger by which way is better.
func (c chart) change(from, to float64, s styles) string {
	if from == 0 {
		return ""
	}
	d := (to - from) / math.Abs(from) * 100
	text := fmt.Sprintf("%+.0f%%", d)
	switch {
	case d == 0 || c.Better == "":
		return s.faint.Render(text)
	case (d < 0) == (c.Better == "lower"):
		return s.safe.Render(text)
	}
	return s.danger.Render(text)
}

func series(n yaml.Node) ([]group, error) {
	if n.Kind != yaml.MappingNode || len(n.Content) == 0 {
		return nil, errors.New("data is a map of label to a list of numbers, one per series")
	}
	var groups []group
	for i := 0; i+1 < len(n.Content); i += 2 {
		list := n.Content[i+1]
		if list.Kind != yaml.SequenceNode || len(list.Content) == 0 {
			return nil, fmt.Errorf("line %d: %s is a list of numbers, one per series", list.Line, n.Content[i].Value)
		}
		g := group{label: n.Content[i].Value}
		for _, c := range list.Content {
			v, err := number(c)
			if err != nil {
				return nil, err
			}
			g.values = append(g.values, v)
		}
		groups = append(groups, g)
	}
	return groups, nil
}

// hbar is cells wide in eighths, so a bar grows smoothly rather than a cell at a time.
func hbar(cells float64) string {
	if cells <= 0 {
		return ""
	}
	full := int(cells)
	part := int((cells - float64(full)) * 8)
	out := strings.Repeat("█", full)
	if part > 0 {
		out += string([]rune("▏▎▍▌▋▊▉")[part-1])
	}
	return out
}

// vbar is one cell of a column filled eighths high, empty below none and full from eight.
func vbar(eighths float64) string {
	switch {
	case eighths >= 8:
		return "█"
	case eighths < 1:
		return " "
	}
	return string(sparkCells[int(eighths)-1])
}

// scaled is v as a share of cells against the largest, and never less than an
// eighth for a positive value, so a small one still shows.
func scaled(v, hi float64, cells int) float64 {
	if hi <= 0 || v <= 0 {
		return 0
	}
	return max(v/hi*float64(cells), 0.125)
}

func extent(pts []point) (lo, hi float64) {
	lo, hi = pts[0].value, pts[0].value
	for _, p := range pts {
		lo, hi = min(lo, p.value), max(hi, p.value)
	}
	return lo, hi
}

func centre(s string, w int) string {
	left := max(0, (w-ansi.StringWidth(s))/2)
	return pad(strings.Repeat(" ", left)+s, w)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
