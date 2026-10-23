// Package markdown renders prose through glamour, in the theme's named style.
package markdown

import "charm.land/glamour/v2"

// Render renders prose in the named glamour style rather than one sniffed
// from the terminal. Under 20 wide it still wraps at 20, and the frame truncates.
func Render(body, style string, width int) (string, error) {
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(style),
		glamour.WithWordWrap(max(20, width)),
	)
	if err != nil {
		return "", err
	}
	return r.Render(body)
}
