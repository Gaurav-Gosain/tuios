package vt

import "image/color"

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

func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}
