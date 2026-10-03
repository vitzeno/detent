package viewspec

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// splitMinWidth is the narrowest frame a diff is drawn side by side in.
// Below it each half is too thin to read, so it is drawn inline.
const splitMinWidth = 100

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// hunk counts down the lines its @@ header promised, so a removed line
// reading "-- comment" is not taken for a file header.
type hunk struct{ old, new int }

func (h *hunk) open() bool { return h.old > 0 || h.new > 0 }

// start reads a hunk header, returning where each side's lines begin.
func (h *hunk) start(l string) (oldAt, newAt int, ok bool) {
	m := hunkHeader.FindStringSubmatch(l)
	if m == nil {
		return 0, 0, false
	}
	count := func(s string) int {
		if s == "" {
			return 1
		}
		n, _ := strconv.Atoi(s)
		return n
	}
	oldAt, _ = strconv.Atoi(m[1])
	newAt, _ = strconv.Atoi(m[3])
	h.old, h.new = count(m[2]), count(m[4])
	return oldAt, newAt, true
}

// take consumes one body line, reporting which sides it belongs to.
func (h *hunk) take(l string) (removed, added bool) {
	switch {
	case strings.HasPrefix(l, "-"):
		h.old--
		return true, false
	case strings.HasPrefix(l, "+"):
		h.new--
		return false, true
	case l == "" || strings.HasPrefix(l, " "):
		h.old--
		h.new--
	}
	return false, false
}

// splitDiff draws a unified diff as two columns, the old file on the
// left and the new on the right, removed and added lines side by side.
func splitDiff(lines []string, f Frame) []string {
	half := (f.Width - 3) / 2
	var out []string
	var removed, added []string
	oldNo, newNo := 0, 0
	var h hunk

	cell := func(no int, text string, r Role) string {
		num := "    "
		if no > 0 {
			num = fmt.Sprintf("%4d", no)
		}
		// A tab is one rune but several columns, which would push the right half out of line.
		body := f.Paint.Truncate(strings.ReplaceAll(text, "\t", "    "), half-5)
		pad := strings.Repeat(" ", max(half-5-f.Paint.Width(body), 0))
		return f.Paint.Paint(RoleFaint, num) + " " + f.Paint.Paint(r, body) + pad
	}
	row := func(left, right string) {
		out = append(out, left+f.Paint.Paint(RoleFaint, " │ ")+right)
	}
	// flush pairs the removed lines with the added ones that replaced them.
	flush := func() {
		for i := range max(len(removed), len(added)) {
			left, right := cell(0, "", RoleDefault), cell(0, "", RoleDefault)
			if i < len(removed) {
				left = cell(oldNo, removed[i], RoleDanger)
				oldNo++
			}
			if i < len(added) {
				right = cell(newNo, added[i], RoleSafe)
				newNo++
			}
			row(left, right)
		}
		removed, added = removed[:0], added[:0]
	}
	whole := func(r Role, l string) {
		flush()
		out = append(out, f.Paint.Paint(r, f.Paint.Truncate(l, f.Width)))
	}

	for _, l := range lines {
		if h.open() && !strings.HasPrefix(l, "\\") {
			switch removedLine, addedLine := h.take(l); {
			case removedLine:
				removed = append(removed, l[1:])
			case addedLine:
				added = append(added, l[1:])
			default:
				flush()
				body := strings.TrimPrefix(l, " ")
				row(cell(oldNo, body, RoleDefault), cell(newNo, body, RoleDefault))
				oldNo++
				newNo++
			}
			continue
		}
		switch {
		case strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---") ||
			strings.HasPrefix(l, "diff ") || strings.HasPrefix(l, "index "):
			whole(RoleMuted, l)
		case strings.HasPrefix(l, "@@"):
			whole(RoleFaint, l)
			if o, n, ok := h.start(l); ok {
				oldNo, newNo = o, n
			}
		case strings.HasPrefix(l, "-"):
			removed = append(removed, l[1:])
		case strings.HasPrefix(l, "+"):
			added = append(added, l[1:])
		case strings.HasPrefix(l, " ") && oldNo > 0:
			flush()
			row(cell(oldNo, l[1:], RoleDefault), cell(newNo, l[1:], RoleDefault))
			oldNo++
			newNo++
		default:
			whole(RoleDefault, l)
		}
	}
	flush()
	return out
}
