package view

import (
	"errors"
	"fmt"
	"image/color"
	"io"
	"math"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"gopkg.in/yaml.v3"

	"github.com/vitzeno/detent/ui/theme"
)

// styles are built from the theme each call, so nothing here needs restyling.
type styles struct {
	heading, text, accent, value, faint, safe, caution, danger, vivid lipgloss.Style
	// palette tells apart the parts of a share or the series of a compare.
	palette []lipgloss.Style
}

func newStyles(t theme.Theme) styles {
	fg := func(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }
	s := styles{
		heading: lipgloss.NewStyle().Foreground(t.TextPrimary).Bold(true),
		text:    fg(t.TextPrimary),
		accent:  fg(t.Accent),
		value:   lipgloss.NewStyle().Foreground(t.Accent).Bold(true),
		faint:   fg(t.TextFaint),
		safe:    fg(t.Safe),
		caution: fg(t.Caution),
		danger:  fg(t.Danger),
		vivid:   fg(t.Tools.Agent),
	}
	for _, c := range []color.Color{t.Accent, t.Tools.Read, t.Tools.Write, t.Tools.Shell, t.Tools.Web,
		t.Tools.Server, t.Tools.Review, t.Tools.Agent, t.Tools.Other} {
		s.palette = append(s.palette, fg(c))
	}
	return s
}

func (s styles) hue(i int) lipgloss.Style { return s.palette[i%len(s.palette)] }

func (s styles) of(t tone) lipgloss.Style {
	switch t {
	case toneAccent:
		return s.accent
	case toneSafe:
		return s.safe
	case toneCaution:
		return s.caution
	case toneDanger:
		return s.danger
	case toneVivid:
		return s.vivid
	default:
		return s.faint
	}
}

// tone names a style, so a package var can say which without baking one.
type tone int

const (
	toneFaint tone = iota
	toneAccent
	toneSafe
	toneCaution
	toneDanger
	toneVivid
)

// stat is one tile of a ```stats block. A delta starting + is drawn safe and
// one starting - is drawn as danger, and a trend is a sparkline under the label.
type stat struct {
	Label string    `yaml:"label"`
	Value scalar    `yaml:"value"`
	Delta scalar    `yaml:"delta"`
	Trend yaml.Node `yaml:"trend"`
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

// tile is a stat laid out: its width, and its trend drawn once.
type tile struct {
	stat
	width int
	trend string
}

// kind is one outcome a line block's entries may name, with its glyph and tone.
type kind struct {
	name, glyph string
	tone        tone
}

// statusKinds are a ```status block's outcomes, and stepKinds a ```steps block's.
var (
	statusKinds = []kind{{"pass", "✓", toneSafe}, {"fail", "✗", toneDanger}, {"warn", "!", toneCaution}, {"skip", "○", toneFaint}}
	stepKinds   = []kind{{"done", "●", toneSafe}, {"doing", "◉", toneAccent}, {"todo", "○", toneFaint}, {"failed", "✗", toneDanger}}
)

type entry struct {
	kind  kind
	items []string
}

// maxNumber is the largest magnitude a chart draws.
const maxNumber = 1e15

// drawStats lays tiles out in rows as wide as fit, each value counting up as it arrives.
func drawStats(body string, s styles, width int) (drawn, error) {
	var stats []stat
	if err := decode(body, &stats); err != nil {
		return nil, err
	}
	const gap = "    "
	var rows [][]tile
	used := 0
	for _, st := range stats {
		t := tile{stat: st}
		if !st.Trend.IsZero() {
			pts, err := points(st.Trend)
			if err != nil {
				return nil, err
			}
			t.trend = s.accent.Render(strip(pts))
		}
		t.width = max(ansi.StringWidth(statValue(st, s, 1)), ansi.StringWidth(st.Label), ansi.StringWidth(t.trend))
		if len(rows) == 0 || used > 0 && used+len(gap)+t.width > width {
			rows, used = append(rows, nil), 0
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], t)
		used += t.width + len(gap)
	}
	return func(p float64) []string {
		var lines []string
		for i, row := range rows {
			if i > 0 {
				lines = append(lines, "")
			}
			var top, bottom, under []string
			trended := false
			for _, t := range row {
				top = append(top, pad(statValue(t.stat, s, p), t.width))
				bottom = append(bottom, pad(s.faint.Render(t.Label), t.width))
				under = append(under, pad(t.trend, t.width))
				trended = trended || t.trend != ""
			}
			lines = append(lines, strings.Join(top, gap), strings.Join(bottom, gap))
			if trended {
				lines = append(lines, strings.Join(under, gap))
			}
		}
		return lines
	}, nil
}

// statValue is a tile's value p of the way up, its delta once it has landed.
func statValue(st stat, s styles, p float64) string {
	value := s.value.Render(countUp(string(st.Value), p))
	if st.Delta != "" && p >= 1 {
		value += " " + deltaStyle(string(st.Delta), s).Render(string(st.Delta))
	}
	return value
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

// countUp is a value's number p of the way from zero, keeping whatever surrounds
// it and how it was written, so $1,024 counts as $512 and 81% as 40%.
func countUp(value string, p float64) string {
	start := strings.IndexAny(value, "0123456789")
	if p >= 1 || start < 0 {
		return value
	}
	end := start
	for end < len(value) && strings.IndexByte("0123456789,.", value[end]) >= 0 {
		end++
	}
	number := strings.TrimRight(value[start:end], ",.")
	end = start + len(number)
	v, err := strconv.ParseFloat(strings.ReplaceAll(number, ",", ""), 64)
	if err != nil {
		return value
	}
	decimals := 0
	if _, frac, ok := strings.Cut(number, "."); ok {
		decimals = len(frac)
	}
	out := strconv.FormatFloat(v*p, 'f', decimals, 64)
	if strings.Contains(number, ",") {
		out = grouped(out)
	}
	return value[:start] + out + value[end:]
}

// grouped puts thousands commas into a formatted number.
func grouped(n string) string {
	whole, frac, dot := strings.Cut(n, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if dot {
		return b.String() + "." + frac
	}
	return b.String()
}

// drawStatus reads its own lines rather than YAML, since its entries are free text
// and YAML stops at the colon or the backtick a model puts in one.
func drawStatus(body string, s styles, width int) (drawn, error) {
	entries, err := lineEntries(body, statusKinds)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, e := range entries {
		head := s.of(e.kind.tone).Render(e.kind.glyph+" "+pad(e.kind.name, 4)) + "  "
		indent := strings.Repeat(" ", ansi.StringWidth(head))
		for i, part := range wrap(strings.Join(e.items, ", "), width-len(indent)) {
			if i == 0 {
				lines = append(lines, head+part)
			} else {
				lines = append(lines, indent+part)
			}
		}
	}
	return still(lines), nil
}

// drawSteps draws work in order as a timeline, what is still to do faint.
func drawSteps(body string, s styles, width int) (drawn, error) {
	entries, err := lineEntries(body, stepKinds)
	if err != nil {
		return nil, err
	}
	var lines []string
	for i, e := range entries {
		text := s.text
		if e.kind.name == "todo" {
			text = s.faint
		}
		rail := s.faint.Render("│") + " "
		if i == len(entries)-1 {
			rail = "  "
		}
		for j, part := range wrap(strings.Join(e.items, ", "), width-2) {
			if j == 0 {
				lines = append(lines, s.of(e.kind.tone).Render(e.kind.glyph)+" "+text.Render(part))
			} else {
				lines = append(lines, rail+text.Render(part))
			}
		}
		if i < len(entries)-1 {
			lines = append(lines, s.faint.Render("│"))
		}
	}
	return still(lines), nil
}

// lineEntries reads "- pass: text", "- pass: [a, b]", or "- pass:" over
// indented "- item" lines, one entry per kind named, in the order written.
func lineEntries(body string, kinds []kind) ([]entry, error) {
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = k.name
	}
	oneOf := strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
	var out []entry
	n := 0
	for line := range strings.SplitSeq(strings.ReplaceAll(body, "`", ""), "\n") {
		n++
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		item, ok := strings.CutPrefix(text, "- ")
		if !ok {
			return nil, fmt.Errorf("line %d: start each entry with - and one of %s", n, oneOf)
		}
		name, rest, named := strings.Cut(item, ":")
		k, known := kindNamed(kinds, strings.TrimSpace(name))
		if named && known && line == strings.TrimLeft(line, " ") {
			out = append(out, entry{kind: k, items: listed(strings.TrimSpace(rest))})
			continue
		}
		if len(out) == 0 || line == strings.TrimLeft(line, " ") {
			return nil, fmt.Errorf("line %d: %q is not %s", n, strings.TrimSpace(name), oneOf)
		}
		last := &out[len(out)-1]
		last.items = append(last.items, item)
	}
	if len(out) == 0 {
		return nil, errors.New("the block is empty")
	}
	return out, nil
}

func kindNamed(kinds []kind, name string) (kind, bool) {
	for _, k := range kinds {
		if k.name == name {
			return k, true
		}
	}
	return kind{}, false
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
	// Bounded, so a difference of two can never overflow into a NaN cell count.
	if math.Abs(v) > maxNumber {
		return 0, fmt.Errorf("line %d: %q is too large to draw", n.Line, n.Value)
	}
	return v, nil
}

// amount spaces a word unit, 10 lines, and keeps a symbol on its number, 5ms.
func amount(v float64, unit string) string {
	return amountAt(v, 1, unit)
}

// amountAt is v's amount at of the way up from zero, with the decimals it has at rest.
func amountAt(v, at float64, unit string) string {
	if len(unit) > 2 {
		unit = " " + unit
	}
	exact := strconv.FormatFloat(v, 'f', -1, 64)
	if at >= 1 {
		return exact + unit
	}
	decimals := 0
	if _, frac, ok := strings.Cut(exact, "."); ok {
		decimals = len(frac)
	}
	return strconv.FormatFloat(v*at, 'f', decimals, 64) + unit
}

// wrap breaks text to width at spaces, and mid-word only for a word wider than it.
func wrap(text string, width int) []string {
	return strings.Split(ansi.Wrap(text, max(width, 1), ""), "\n")
}

func pad(s string, w int) string {
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}
