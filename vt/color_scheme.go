package vt

import "image/color"

// Mode 2031: a program that turns it on asks to be told when the terminal's
// colours change. The terminal sends CSI ? 997 ; 1 n when the background is
// dark and CSI ? 997 ; 2 n when it is light. The contour spec that defines the
// mode sends the notice on any palette change, not only on a switch between
// dark and light, and programs act on that: Neovim asks for the background
// again, and opencode asks for the whole palette again. A program that reads
// the notice only as dark or light sees the same value twice.
//
// The notice follows the reported colours: the ones SetReportColors gives,
// which are what OSC 10, OSC 11 and OSC 4 answer when the guest has set no
// colour of its own. A SetReportColors call that changes any of them sends one
// notice, so one theme change is one notice. SetThemeColors sends none. It sets
// the colours the screen is drawn in, and a host may call it for a preview
// that it never reports, as tuios does while its theme picker is open. A host
// that changes the theme calls SetReportColors with the new colours to notify
// the guest. CSI ? 996 n asks for the report at any time.
//
// Spec: https://github.com/contour-terminal/contour/blob/master/docs/vt-extensions/color-palette-update-notifications.md

// colorSchemeNotice is the mode 2031 notice, and the answer to DSR 996, for a
// pane on background bg.
func colorSchemeNotice(bg color.Color) string {
	if bg != nil && IsLightBackground(bg) {
		return "\x1b[?997;2n"
	}
	return "\x1b[?997;1n"
}

// IsLightBackground reports whether a background reads as light: its
// perceived brightness (ITU-R BT.601 luma) is over half. It is the rule the
// mode 2031 notice and the DSR 996 answer use, so a host that tells other
// programs about light and dark can agree with what its panes are told.
func IsLightBackground(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return 299*r+587*g+114*b > 1000*0xffff/2
}

// sameReport reports whether two sets of report colours give the same
// answers.
func sameReport(fg0, bg0 color.Color, pal0 [16]color.Color, fg1, bg1 color.Color, pal1 [16]color.Color) bool {
	if !sameColor(fg0, fg1) || !sameColor(bg0, bg1) {
		return false
	}
	for i := range pal0 {
		if !sameColor(pal0[i], pal1[i]) {
			return false
		}
	}
	return true
}

// sameColor compares two colours by value. Both nil is the same; one nil is
// not.
func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}
