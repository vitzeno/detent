package view

import (
	"errors"
	"fmt"
	"image/color"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// option is one choice of an ```options block, with what speaks for and against it.
type option struct {
	name  string
	pick  bool
	lines []string // each "+", "-" or " " and its text
}

// minOptionW is the narrowest an option's box may be before the boxes stack.
const minOptionW = 26

// badgeTone reads a badge's value as an outcome, so passing is green wherever it is.
var badgeTone = []struct {
	words *regexp.Regexp
	tone  tone
}{
	{regexp.MustCompile(`(?i)^(pass(ing|ed)?|ok|yes|green|✓|success|clean|up)$`), toneSafe},
	{regexp.MustCompile(`(?i)^(fail(ing|ed)?|no|red|✗|error|broken|down)$`), toneDanger},
	{regexp.MustCompile(`(?i)^(warn(ing)?|flaky|partial|amber|yellow|skip(ped)?)$`), toneCaution},
}

// drawOptions draws each choice as a box, side by side while they fit, the pick's
// border in the accent and its star landing last.
func drawOptions(body string, s styles, width int) (drawn, error) {
	opts, err := options(body)
	if err != nil {
		return nil, err
	}
	n := len(opts)
	boxW := (width - (n - 1)) / n
	across := boxW >= minOptionW
	if !across {
		boxW = width
	}
	boxes := make([][]string, n)
	tall := 0
	for i, o := range opts {
		boxes[i] = optionBody(o, s, boxW-4)
		tall = max(tall, len(boxes[i]))
	}
	return func(p float64) []string {
		var rows []string
		var row []string
		for i, o := range opts {
			box := optionBox(o, boxes[i], tall, boxW, s, p >= 1)
			if p < 1 && p < 0.6*float64(i)/float64(n) {
				for j := range box {
					box[j] = strings.Repeat(" ", boxW)
				}
			}
			if !across {
				rows = append(rows, box...)
				continue
			}
			if row == nil {
				row = box
				continue
			}
			for j := range row {
				row[j] += " " + box[j]
			}
		}
		return append(rows, row...)
	}, nil
}

// optionBody is an option's strengths and weaknesses wrapped to width, each under its sign.
func optionBody(o option, s styles, width int) []string {
	var out []string
	for _, line := range o.lines {
		sign, text := line[:1], line[1:]
		mark, style := "  ", s.faint
		switch sign {
		case "+":
			mark, style = s.safe.Render("+")+" ", s.text
		case "-":
			mark, style = s.danger.Render("−")+" ", s.text
		}
		for i, part := range wrap(text, width-2) {
			if i > 0 {
				mark = "  "
			}
			out = append(out, mark+style.Render(part))
		}
	}
	return out
}

// optionBox frames an option's lines, tall rows high, its name in the top border.
func optionBox(o option, body []string, tall, width int, s styles, landed bool) []string {
	border, name := s.faint, s.text.Bold(true)
	title := o.name
	if o.pick {
		border, name = s.accent, s.value
		star := "☆ "
		if landed {
			star = "★ "
		}
		title = star + o.name
	}
	title = ansi.Truncate(title, max(width-6, 1), "…")
	top := border.Render("╭ ") + name.Render(title) + border.Render(" "+strings.Repeat("─", max(0, width-4-ansi.StringWidth(title)))+"╮")
	box := []string{top}
	for i := range tall {
		line := ""
		if i < len(body) {
			line = body[i]
		}
		box = append(box, border.Render("│ ")+pad(ansi.Truncate(line, width-4, "…"), width-4)+border.Render(" │"))
	}
	return append(box, border.Render("╰"+strings.Repeat("─", max(0, width-2))+"╯"))
}

// options reads "pick: name" and "option: name" lines, each followed by "+ strength"
// and "- weakness" lines, and any other line as a note on the option above it.
func options(body string) ([]option, error) {
	var out []option
	n := 0
	for line := range strings.SplitSeq(strings.ReplaceAll(body, "`", ""), "\n") {
		n++
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		key, rest, _ := strings.Cut(text, ":")
		if k := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(key, "- "))); k == "pick" || k == "option" {
			out = append(out, option{name: strings.TrimSpace(rest), pick: k == "pick"})
			continue
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("line %d: start with pick or option and a colon", n)
		}
		last := &out[len(out)-1]
		switch {
		case strings.HasPrefix(text, "+"):
			last.lines = append(last.lines, "+"+strings.TrimSpace(text[1:]))
		case strings.HasPrefix(text, "-"), strings.HasPrefix(text, "−"):
			last.lines = append(last.lines, "-"+strings.TrimSpace(strings.TrimLeft(text, "-−")))
		default:
			last.lines = append(last.lines, " "+text)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("the block is empty")
	}
	return out, nil
}

// drawFacts draws named values as an aligned card, each value typing in as it arrives.
func drawFacts(body string, s styles, width int) (drawn, error) {
	var keys []string
	values := map[string][]string{}
	n := 0
	for line := range strings.SplitSeq(strings.ReplaceAll(body, "`", ""), "\n") {
		n++
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if text == "" {
			continue
		}
		key, value, named := strings.Cut(text, ":")
		if !named || strings.HasPrefix(line, " ") && len(keys) > 0 {
			if len(keys) == 0 {
				return nil, fmt.Errorf("line %d: each line is a name, a colon and its value", n)
			}
			last := keys[len(keys)-1]
			values[last] = append(values[last], text)
			continue
		}
		key = strings.TrimSpace(key)
		if _, seen := values[key]; !seen {
			keys = append(keys, key)
		}
		values[key] = append(values[key], strings.TrimSpace(value))
	}
	if len(keys) == 0 {
		return nil, errors.New("the block is empty")
	}
	keyW := 0
	for _, k := range keys {
		keyW = max(keyW, ansi.StringWidth(k))
	}
	keyW = min(keyW, width/3)
	type row struct{ key, value string }
	var rows []row
	for _, k := range keys {
		label := pad(ansi.Truncate(k, keyW, "…"), keyW)
		for _, v := range values[k] {
			for _, part := range wrap(v, width-keyW-2) {
				rows = append(rows, row{label, part})
				label = strings.Repeat(" ", keyW)
			}
		}
	}
	return func(p float64) []string {
		lines := make([]string, len(rows))
		for i, r := range rows {
			value := r.value
			if p < 1 {
				value = ansi.Truncate(value, int(p*float64(ansi.StringWidth(value))), "")
			}
			lines[i] = s.faint.Render(r.key) + "  " + s.text.Render(value)
		}
		return lines
	}, nil
}

// drawBadges draws short tags as pills in a row, wrapping as they must, each coloured
// by what its value says and popping in from the left as they arrive.
func drawBadges(body string, s styles, width int) (drawn, error) {
	var pills []string
	for line := range strings.SplitSeq(strings.ReplaceAll(body, "`", ""), "\n") {
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if text == "" {
			continue
		}
		label, value, valued := strings.Cut(text, ":")
		label, value = strings.TrimSpace(label), strings.TrimSpace(value)
		ink := s.theme.Background
		pill := pillStyle(ink, s.theme.TextMuted).Render(" " + label + " ")
		if valued && value != "" {
			pill += pillStyle(ink, s.of(badgeToneOf(value)).GetForeground()).Render(" " + value + " ")
		}
		pills = append(pills, ansi.Truncate(pill, width, "…"))
	}
	if len(pills) == 0 {
		return nil, errors.New("the block is empty")
	}
	return func(p float64) []string {
		var lines []string
		line, used := "", 0
		for i, pill := range pills {
			w := ansi.StringWidth(pill)
			if used > 0 && used+1+w > width {
				lines, line, used = append(lines, line), "", 0
			}
			if used > 0 {
				line += " "
				used++
			}
			if p < 1 && p < float64(i)/float64(len(pills)) {
				pill = strings.Repeat(" ", w)
			}
			line += pill
			used += w
		}
		return append(lines, line)
	}, nil
}

func badgeToneOf(value string) tone {
	for _, b := range badgeTone {
		if b.words.MatchString(value) {
			return b.tone
		}
	}
	return toneAccent
}

func pillStyle(ink, paper color.Color) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(ink).Background(paper).Bold(true)
}
