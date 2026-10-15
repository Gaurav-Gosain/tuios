package vt

import "image/color"

// Mode 2031: a program that turns it on asks to be told when the terminal
// goes from a dark background to a light one or back. The terminal sends
// CSI ? 997 ; 1 n for dark and CSI ? 997 ; 2 n for light.
//
// In tuios the background a pane is drawn on is the one SetReportColors
// gives it: the theme's, the painted one, or the host's own. So the report is
// sent when that colour changes between dark and light, which is when the
// theme switches to one of the other kind (the Omarchy theme among them) or
// the host terminal switches its scheme.

// colorSchemeChange is the report to send when the background goes from
// before to after, and empty when both are dark or both light, or one is not
// known.
func colorSchemeChange(before, after color.Color) string {
	if before == nil || after == nil {
		return ""
	}
	light := isLightBackground(after)
	if isLightBackground(before) == light {
		return ""
	}
	if light {
		return "\x1b[?997;2n"
	}
	return "\x1b[?997;1n"
}

// isLightBackground reports whether a background reads as light: its
// perceived brightness is over half.
func isLightBackground(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return 299*r+587*g+114*b > 1000*0xffff/2
}
