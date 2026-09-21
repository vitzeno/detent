package viewspec

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// gantt lays each row on a shared axis from where it starts to how
// long it ran, which is the shape of anything timed: a boot, a build,
// a test run. bar would draw the lengths and lose when they happened.
type ganttWidget struct{}

func (ganttWidget) Validate(b Block, fields []string) error {
	if len(b.Columns) != 3 {
		return fmt.Errorf("gantt needs a label column, a start column and a length column")
	}
	return checkColumns(b, fields)
}

func (ganttWidget) Draw(b Block, d Data, f Frame) ([]string, error) {
	label, startF, lenF := b.Columns[0].Field, b.Columns[1].Field, b.Columns[2].Field
	starts := make([]float64, len(d.Rows))
	lengths := make([]float64, len(d.Rows))
	notes := make([]string, len(d.Rows))
	labels := make([]string, len(d.Rows))
	lo, hi := 0.0, 0.0
	for i, r := range d.Rows {
		starts[i], _ = instant(r[startF])
		lengths[i], _ = instant(r[lenF])
		labels[i], notes[i] = r[label], r[lenF]
		if i == 0 {
			lo = starts[i]
		}
		lo = min(lo, starts[i])
		hi = max(hi, starts[i]+lengths[i])
	}
	lay, err := layOutBars(labels, notes, f)
	if err != nil {
		return nil, err
	}
	lines := titleLine(b, f)
	for i := range d.Rows {
		at := int(fraction(starts[i], lo, hi) * float64(lay.bar))
		width := 1
		if hi > lo {
			width = max(1, int(lengths[i]/(hi-lo)*float64(lay.bar)))
		}
		width = min(width, lay.bar-at)
		role := accentOr(b, d.Rows[i], RoleAccent)
		if f.Focused && i == f.Cursor {
			role = RoleAccent
		}
		lines = append(lines,
			f.Paint.Paint(RoleMuted, pad(f.Paint.Truncate(labels[i], lay.label), lay.label, f.Paint))+" "+
				strings.Repeat(" ", at)+f.Paint.Paint(role, strings.Repeat("█", width))+
				strings.Repeat(" ", lay.bar-at-width)+" "+
				f.Paint.Paint(RoleFaint, pad(notes[i], lay.note, f.Paint)))
	}
	return lines, nil
}

func (ganttWidget) CursorLine(b Block, d Data, f Frame) int { return chartCursor(b, d, f) }

// timeline places each row at the moment it happened on one shared
// axis. gantt is for the things that lasted; this is for the ones that
// only have a when.
type timelineWidget struct{}

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

// checkColumns is the validation every multi-column chart shares: rows
// exist, each named column was parsed, and the shared keys resolve.
func checkColumns(b Block, fields []string) error {
	if len(fields) == 0 {
		return ErrNoRows
	}
	for _, c := range b.Columns {
		if err := needField(c.Field, fields); err != nil {
			return err
		}
	}
	return checkShared(b, fields)
}

func (ganttWidget) Describe() Description {
	return Description{
		What:     "one bar per row placed where it started and drawn as long as it ran",
		Needs:    []string{"columns (exactly three: label, start, then length)"},
		NotFor:   "lengths with no start, which is bar",
		Examples: []string{"systemd-analyze blame", "per-package test time", "build phases"},
	}
}

func (timelineWidget) Describe() Description {
	return Description{
		What:     "one marker per row on a shared axis, for events that have a moment but no length",
		Needs:    []string{"columns (exactly two: label, then the time)"},
		NotFor:   "events that lasted, which is gantt",
		Examples: []string{"git log dates", "docker events", "journalctl timestamps"},
	}
}
