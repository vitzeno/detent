package view

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"gopkg.in/yaml.v3"

	"github.com/vitzeno/detent/ui/theme"
)

// styles are built from the theme each call, so nothing here needs restyling.
type styles struct {
	heading, accent, value, faint, safe, caution, danger lipgloss.Style
}

func newStyles(t theme.Theme) styles {
	return styles{
		heading: lipgloss.NewStyle().Foreground(t.TextPrimary).Bold(true),
		accent:  lipgloss.NewStyle().Foreground(t.Accent),
		value:   lipgloss.NewStyle().Foreground(t.Accent).Bold(true),
		faint:   lipgloss.NewStyle().Foreground(t.TextFaint),
		safe:    lipgloss.NewStyle().Foreground(t.Safe),
		caution: lipgloss.NewStyle().Foreground(t.Caution),
		danger:  lipgloss.NewStyle().Foreground(t.Danger),
	}
}

// chart is a ```chart block. data is a map of label to number, or for a
// sparkline a list of numbers, kept in the order written.
type chart struct {
	Type  string    `yaml:"type"`
	Title string    `yaml:"title"`
	Unit  string    `yaml:"unit"`
	Data  yaml.Node `yaml:"data"`
}

type point struct {
	label string
	value float64
}

func drawChart(body string, s styles, width int) ([]string, error) {
	var c chart
	if err := decode(body, &c); err != nil {
		return nil, err
	}
	pts, err := points(c.Data)
	if err != nil {
		return nil, err
	}
	var lines []string
	if c.Title != "" {
		lines = append(lines, s.heading.Render(c.Title))
	}
	switch c.Type {
	case "bar":
		return append(lines, bars(pts, c.Unit, s, width)...), nil
	case "sparkline":
		return append(lines, sparkline(pts, c.Unit, s)), nil
	case "gauge":
		return append(lines, gauges(pts, s, width)...), nil
	}
	return nil, fmt.Errorf("type %q is not bar, sparkline or gauge", c.Type)
}

// bars scale to the largest value, so the longest bar is always the largest.
func bars(pts []point, unit string, s styles, width int) []string {
	labelW, valueW, hi := 0, 0, 0.0
	for _, p := range pts {
		labelW = max(labelW, ansi.StringWidth(p.label))
		valueW = max(valueW, ansi.StringWidth(amount(p.value, unit)))
		hi = max(hi, p.value)
	}
	labelW = min(labelW, width/3)
	barW := width - labelW - valueW - 2
	lines := make([]string, 0, len(pts))
	for _, p := range pts {
		label := pad(ansi.Truncate(p.label, labelW, "…"), labelW)
		if barW < 4 {
			lines = append(lines, label+" "+s.faint.Render(amount(p.value, unit)))
			continue
		}
		n := 0
		if hi > 0 && p.value > 0 {
			n = max(1, int(p.value/hi*float64(barW)))
		}
		lines = append(lines, label+" "+s.accent.Render(strings.Repeat("█", n))+
			strings.Repeat(" ", barW-n)+" "+s.faint.Render(amount(p.value, unit)))
	}
	return lines
}

var sparkCells = []rune("▁▂▃▄▅▆▇█")

func sparkline(pts []point, unit string, s styles) string {
	if len(pts) == 0 {
		return ""
	}
	lo, hi := pts[0].value, pts[0].value
	for _, p := range pts {
		lo, hi = min(lo, p.value), max(hi, p.value)
	}
	var strip strings.Builder
	for _, p := range pts {
		i := 0
		if hi > lo {
			i = int((p.value - lo) / (hi - lo) * float64(len(sparkCells)-1))
		}
		strip.WriteRune(sparkCells[i])
	}
	return s.accent.Render(strip.String()) + s.faint.Render("  "+amount(lo, unit)+"–"+amount(hi, unit))
}

// gauges read each value as a percentage, clamped to 0–100.
func gauges(pts []point, s styles, width int) []string {
	labelW := 0
	for _, p := range pts {
		labelW = max(labelW, ansi.StringWidth(p.label))
	}
	labelW = min(labelW, width/3)
	cells := width - labelW - 6
	lines := make([]string, 0, len(pts))
	for _, p := range pts {
		v := min(max(p.value, 0), 100)
		label := pad(ansi.Truncate(p.label, labelW, "…"), labelW)
		pct := s.faint.Render(fmt.Sprintf(" %3.0f%%", v))
		if cells < 4 {
			lines = append(lines, label+pct)
			continue
		}
		n := int(v * float64(cells) / 100)
		lines = append(lines, label+" "+s.accent.Render(strings.Repeat("█", n))+
			s.faint.Render(strings.Repeat("░", cells-n))+pct)
	}
	return lines
}

// stat is one tile of a ```stats block. A delta starting + is drawn safe and
// one starting - is drawn as danger.
type stat struct {
	Label string `yaml:"label"`
	Value scalar `yaml:"value"`
	Delta scalar `yaml:"delta"`
}

// scalar takes any YAML scalar as written, so value: 412 and value: 81% both read.
type scalar string

func (s *scalar) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: expected a single value", n.Line)
	}
	*s = scalar(n.Value)
	return nil
}

func drawStats(body string, s styles, width int) ([]string, error) {
	var tiles []stat
	if err := decode(body, &tiles); err != nil {
		return nil, err
	}
	const gap = "    "
	var lines []string
	var top, bottom []string
	used := 0
	emit := func() {
		if len(top) > 0 {
			lines = append(lines, strings.Join(top, gap), strings.Join(bottom, gap), "")
		}
		top, bottom, used = nil, nil, 0
	}
	for _, t := range tiles {
		value := s.value.Render(string(t.Value))
		if t.Delta != "" {
			value += " " + deltaStyle(string(t.Delta), s).Render(string(t.Delta))
		}
		w := max(ansi.StringWidth(value), ansi.StringWidth(t.Label))
		if used > 0 && used+len(gap)+w > width {
			emit()
		}
		top, bottom = append(top, pad(value, w)), append(bottom, pad(s.faint.Render(t.Label), w))
		used += w + len(gap)
	}
	emit()
	return trimBlank(lines), nil
}

func deltaStyle(d string, s styles) lipgloss.Style {
	switch {
	case strings.HasPrefix(d, "+"):
		return s.safe
	case strings.HasPrefix(d, "-"), strings.HasPrefix(d, "−"):
		return s.danger
	}
	return s.faint
}

// statusKinds are a ```status block's keys, each with its glyph and tone.
var statusKinds = map[string]struct {
	glyph string
	tone  tone
}{
	"pass": {"✓", toneSafe},
	"fail": {"✗", toneDanger},
	"warn": {"!", toneCaution},
	"skip": {"○", toneFaint},
}

// tone names a style, so a package var can say which without baking one.
type tone int

const (
	toneFaint tone = iota
	toneSafe
	toneCaution
	toneDanger
)

func (s styles) of(t tone) lipgloss.Style {
	switch t {
	case toneSafe:
		return s.safe
	case toneCaution:
		return s.caution
	case toneDanger:
		return s.danger
	default:
		return s.faint
	}
}

// drawStatus reads its own lines rather than YAML, since its entries are free text
// and YAML stops at the colon or the backtick a model puts in one.
func drawStatus(body string, s styles, width int) ([]string, error) {
	entries, err := statusEntries(body)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, e := range entries {
		kind := statusKinds[e.kind]
		head := s.of(kind.tone).Render(kind.glyph+" "+pad(e.kind, 4)) + "  "
		indent := strings.Repeat(" ", ansi.StringWidth(head))
		for i, part := range strings.Split(ansi.Wrap(strings.Join(e.items, ", "), max(width-len(indent), 1), ""), "\n") {
			if i == 0 {
				lines = append(lines, head+part)
			} else {
				lines = append(lines, indent+part)
			}
		}
	}
	return lines, nil
}

type statusEntry struct {
	kind  string
	items []string
}

// statusEntries reads "- pass: text", "- pass: [a, b]", or "- pass:" over
// indented "- item" lines, one entry per outcome named.
func statusEntries(body string) ([]statusEntry, error) {
	var out []statusEntry
	n := 0
	for line := range strings.SplitSeq(strings.ReplaceAll(body, "`", ""), "\n") {
		n++
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		item, ok := strings.CutPrefix(text, "- ")
		if !ok {
			return nil, fmt.Errorf("line %d: start each entry with - and one of pass, fail, warn or skip", n)
		}
		kind, rest, isKind := strings.Cut(item, ":")
		if _, known := statusKinds[strings.TrimSpace(kind)]; isKind && known && line == strings.TrimLeft(line, " ") {
			out = append(out, statusEntry{kind: strings.TrimSpace(kind), items: listed(strings.TrimSpace(rest))})
			continue
		}
		if len(out) == 0 || line == strings.TrimLeft(line, " ") {
			return nil, fmt.Errorf("line %d: %q is not pass, fail, warn or skip", n, strings.TrimSpace(kind))
		}
		last := &out[len(out)-1]
		last.items = append(last.items, item)
	}
	if len(out) == 0 {
		return nil, errors.New("the block is empty")
	}
	return out, nil
}

// listed reads "[a, b]" as two items and anything else as one, nothing as none.
func listed(text string) []string {
	if inner, ok := strings.CutPrefix(text, "["); ok && strings.HasSuffix(inner, "]") {
		var items []string
		for item := range strings.SplitSeq(strings.TrimSuffix(inner, "]"), ",") {
			if item = strings.TrimSpace(item); item != "" {
				items = append(items, item)
			}
		}
		return items
	}
	if text == "" {
		return nil
	}
	return []string{text}
}

// decode reads body strictly, so a misspelt key says so instead of drawing nothing.
// Backticks go first: a model quotes code in a label as in prose, and YAML refuses it.
func decode(body string, into any) error {
	dec := yaml.NewDecoder(strings.NewReader(strings.ReplaceAll(body, "`", "")))
	dec.KnownFields(true)
	if err := dec.Decode(into); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("the block is empty")
		}
		return err
	}
	return nil
}

func points(n yaml.Node) ([]point, error) {
	var pts []point
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			v, err := number(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			pts = append(pts, point{label: n.Content[i].Value, value: v})
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			v, err := number(c)
			if err != nil {
				return nil, err
			}
			pts = append(pts, point{label: strconv.Itoa(i + 1), value: v})
		}
	default:
		return nil, errors.New("data is a map of label to number, or a list of numbers")
	}
	if len(pts) == 0 {
		return nil, errors.New("data is empty")
	}
	return pts, nil
}

// number reads a scalar's leading number, so 4.2s, 81% and 1,024 all read.
func number(n *yaml.Node) (float64, error) {
	if n.Kind != yaml.ScalarNode {
		return 0, fmt.Errorf("line %d: %q is not a number", n.Line, n.Value)
	}
	s := strings.ReplaceAll(strings.TrimSpace(n.Value), ",", "")
	end := 0
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || s[end] == '.' || end == 0 && (s[end] == '-' || s[end] == '+')) {
		end++
	}
	v, err := strconv.ParseFloat(s[:end], 64)
	if err != nil {
		return 0, fmt.Errorf("line %d: %q is not a number", n.Line, n.Value)
	}
	return v, nil
}

// amount spaces a word unit, 10 lines, and keeps a symbol on its number, 5ms.
func amount(v float64, unit string) string {
	if len(unit) > 2 {
		unit = " " + unit
	}
	return strconv.FormatFloat(v, 'f', -1, 64) + unit
}

func pad(s string, w int) string {
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

func trimBlank(lines []string) []string {
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
