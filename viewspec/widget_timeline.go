package viewspec

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// timeline places each row at the moment it happened on one shared
// axis. gantt is for the things that lasted; this is for the ones that
// only have a when.
type timelineWidget struct{}

var (
	_ Validator = timelineWidget{}
	_ Selector  = timelineWidget{}
	_ Described = timelineWidget{}
)

func (timelineWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 2 {
		return fmt.Errorf("timeline needs a label column and a time column")
	}
	return checkColumns(b, fields)
}

func (timelineWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	label, at := b.Columns[0].Field, b.Columns[1].Field
	labels := make([]string, len(d.Rows))
	notes := make([]string, len(d.Rows))
	points := make([]float64, len(d.Rows))
	known := false
	for i, r := range d.Rows {
		labels[i], notes[i] = r[label], r[at]
		if v, ok := instant(r[at]); ok {
			points[i], known = v, true
		}
	}
	if !known {
		return nil, fmt.Errorf("no readable times in %q", at)
	}
	lo, hi := bounds(points)
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
		lines = append(lines,
			f.Paint.Paint(RoleMuted, pad(f.Paint.Truncate(labels[i], lay.label), lay.label, f.Paint))+" "+
				f.Paint.Paint(RoleFaint, strings.Repeat("·", x))+
				f.Paint.Paint(role, "●")+
				strings.Repeat(" ", lay.bar-x-1)+" "+
				f.Paint.Paint(RoleFaint, pad(notes[i], lay.note, f.Paint)))
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

// timeLayouts are the shapes a shell prints a moment in, tried in
// order. Anything a layout cannot read falls through to a duration and
// then to a plain number, so an axis works on epochs too.
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

// instant reads a field as a point on an axis, in seconds. It reports
// false rather than 0 for something it cannot read, since 0 is a real
// position and "unknown" is not.
func instant(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return float64(t.UnixNano()) / 1e9, true
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
