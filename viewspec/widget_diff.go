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

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// splitDiff draws a unified diff as two columns, the old file on the
// left and the new on the right, removed and added lines side by side.
func splitDiff(lines []string, f Frame) []string {
	half := (f.Width - 3) / 2
	var out []string
	var removed, added []string
	oldNo, newNo := 0, 0

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
		switch {
		case strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---") ||
			strings.HasPrefix(l, "diff ") || strings.HasPrefix(l, "index "):
			whole(RoleMuted, l)
		case strings.HasPrefix(l, "@@"):
			whole(RoleFaint, l)
			if m := hunkHeader.FindStringSubmatch(l); m != nil {
				oldNo, _ = strconv.Atoi(m[1])
				newNo, _ = strconv.Atoi(m[2])
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
