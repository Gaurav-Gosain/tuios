package app

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The text pieces the rail's small dialogs share: the rename micro-dialog, the
// file dialogs and the worktree dialog. They live here, apart from any one
// dialog, so a change to how one dialog draws cannot change another by
// accident.

// dialogBodyLine is one indented, truncated, canvas-filled body line.
func dialogBodyLine(text string, width int, ink, bg color.Color) string {
	return overlay.Fill(overlay.Style(bg).Render(" ")+
		overlay.Style(bg).Foreground(ink).Render(overlay.Truncate(text, max(width-1, 1))), width, bg)
}

// fieldTail windows a text field's buffer so the tail is always visible: what
// you are typing is at the end, so a value longer than the field scrolls left
// rather than truncating away the part you are looking at.
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
