package viewspec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// timeline places each row at the moment it happened on one shared
// axis, for events with no length.
type timelineWidget struct{}

var (
	_ Validator = timelineWidget{}
	_ Selector  = timelineWidget{}
	_ Described = timelineWidget{}
)

func (timelineWidget) Describe() Description {
	return Description{
		What: "one marker per row on a shared axis, for events that have a moment but no length",
		Needs: []Slot{
			{Name: "label", What: "names each event"},
			{Name: "time", What: "when it happened"},
		},
		NotFor:   "events that lasted, which is gantt",
		Examples: []string{"git log dates", "docker events", "journalctl timestamps"},
	}
}

func (timelineWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 2 {
		return errors.New("timeline needs a label column and a time column")
	}
	return checkColumns(b, fields)
}

func (timelineWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	if len(d.Rows) == 0 {
		return titleLine(b, f), nil
	}
	label, at := b.Columns[0].Field, b.Columns[1].Field
	labels := make([]string, len(d.Rows))
	notes := make([]string, len(d.Rows))
	points := make([]float64, len(d.Rows))
	known := make([]bool, len(d.Rows))
	var read []float64
	for i, r := range d.Rows {
		labels[i], notes[i] = r[label], r[at]
		if points[i], known[i] = instant(r[at]); known[i] {
			read = append(read, points[i])
		}
	}
	if len(read) == 0 {
		return nil, fmt.Errorf("no readable times in %q", at)
	}
	// Only readable times span the axis, so one bad row cannot pin it to zero.
	lo, hi := bounds(read)
	lay, err := layOutBars(labels, notes, f)
	if err != nil {
		return nil, err
	}
	lines := append(titleLine(b, f),
		strings.Repeat(" ", lay.label+1)+
			f.Paint.Paint(RoleFaint, "├"+strings.Repeat("─", max(lay.bar-2, 0))+"┤"))
	for i := range d.Rows {
		x := int(fraction(points[i], lo, hi) * float64(lay.bar-1))
		role := accentOr(b, d.Rows[i], RoleAccent)
		if f.Focused && i == f.Cursor {
			role = RoleAccent
		}
		mark := f.Paint.Paint(RoleFaint, repeat("·", x)) + f.Paint.Paint(role, "●") + repeat(" ", lay.bar-x-1)
		if !known[i] {
			mark = repeat(" ", lay.bar)
		}
		lines = append(lines,
			labelCell(labels[i], lay.label, f)+" "+
				mark+" "+f.Paint.Paint(RoleFaint, pad(notes[i], lay.note, f.Paint)))
	}
	return lines, nil
}

// CursorLine clears the title and the axis above the first event.
func (timelineWidget) CursorLine(b Block, d Data, f Frame) int {
	at := chartCursor(b, d, f)
	if at < 0 {
		return at
	}
	return at + 1
}

// timeLayouts are the shapes a shell prints a moment in, tried in order
// before a duration and then a plain number.
var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006/01/02 15:04:05",
	"2006-01-02",
	"Jan _2 15:04:05",
	"Jan _2 15:04",
	"15:04:05",
	"15:04",
}

// instant reads a field as a point on an axis, in seconds. Unreadable
// is false rather than 0, since 0 is a real position.
func instant(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			// Not UnixNano, which is undefined for the year 0 a clock-only layout gives.
			return float64(t.Unix()) + float64(t.Nanosecond())/1e9, true
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d.Seconds(), true
	}
	if _, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64); err == nil {
		return number(s), true
	}
	return 0, false
}
