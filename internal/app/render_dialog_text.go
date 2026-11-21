package app

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

func dialogBodyLine(text string, width int, ink, bg color.Color) string {
	return overlay.Fill(overlay.Style(bg).Render(" ")+
		overlay.Style(bg).Foreground(ink).Render(overlay.Truncate(text, max(width-1, 1))), width, bg)
}

func fieldTail(buf string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(buf)
	for len(r) > 0 && lipgloss.Width(string(r)) > width {
		r = r[1:]
	}
	return string(r)
}
