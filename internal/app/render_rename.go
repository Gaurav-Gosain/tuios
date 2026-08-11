package app

import (
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// renameDialogWidth is the micro-dialog's preferred inner width: a name and a
// cursor, and nothing that would make it a panel.
const renameDialogWidth = 28

var renameHints = []overlay.Hint{
	{Key: "↵", Label: "save"},
	{Key: "esc", Label: "cancel"},
}

// renameFieldText windows the buffer so the tail is always visible: what you
// are typing is at the end, so a name longer than the field scrolls left rather
// than truncating away the part you are looking at.
func renameFieldText(buf string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(buf)
	for len(r) > 0 && lipgloss.Width(string(r)) > width {
		r = r[1:]
	}
	return string(r)
}

// renderRenameDialog draws the rename micro-dialog and its absolute origin, or
// ok false when no rename is in flight.
//
// There is one rename surface. The rail row and the title bar keep drawing the
// old name underneath, which is the old-vs-new comparison in situ: old on the
// row, new in the dialog. Editing in both places at once made the rail cache
// special-case renames and clipped the buffer to the rail's twenty columns.
func (m *OS) renderRenameDialog() (string, overlay.Geometry, int, int, bool) {
	if !m.RenamingWindow {
		return "", overlay.Geometry{}, 0, 0, false
	}
	pal := theme.UI()
	renderW := m.GetRenderWidth()
	inner := overlay.DialogFitWidth(renameDialogWidth, renderW)

	// Sigil, a space, the field, and one cell of right pad, so the cursor never
	// sits against the frame.
	field := renameFieldText(printableTitle(m.RenameBuffer), max(inner-4, 1))
	body := overlay.Style(pal.Canvas).Render(" ") +
		overlay.Style(pal.Canvas).Foreground(pal.AccentBright).Bold(true).Render("› ") +
		overlay.Style(pal.Canvas).Foreground(pal.Fg).Render(field) +
		overlay.Cursor(" ", pal.Canvas, pal.Fg)

	content, geo := overlay.Dialog{
		Title: "rename",
		Width: inner,
		Body:  body,
		Hints: renameHints,
	}.Render(pal)

	x, y := m.renameDialogOrigin(geo)
	return content, geo, x, y, true
}

// renameDialogOrigin anchors the dialog to the thing being renamed: the rail
// row it was started from, else the pane's title bar. The field row (row 2 of
// 3) is what lines up with the target, since that is the row the eye is on.
// Everything is clamped inside the screen and off the dock.
func (m *OS) renameDialogOrigin(geo overlay.Geometry) (int, int) {
	renderW := m.GetRenderWidth()
	top, bottom := m.GetTopMargin(), m.GetTopMargin()+m.GetUsableHeight()

	x, y, anchored := -1, -1, false
	if m.RenameFromRail {
		if row, ok := m.sidebarRowFor(m.RenameTargetID); ok {
			// Beside the rail rather than over it, mirrored for a right-hand
			// rail so the dialog never covers the row it is naming.
			if config.SidebarPosition == "right" {
				x = row.X0 - geo.Width
			} else {
				x = row.X1
			}
			y, anchored = row.Y0-1, true
		}
	}
	if !anchored {
		if w := m.RenameTarget(); w != nil {
			x, y, anchored = w.X, w.Y+1, true
		}
	}
	if !anchored || x < 0 || x+geo.Width > renderW {
		// Too narrow or nothing to anchor to: centre horizontally, top third
		// vertically, which is where a prompt with no home belongs.
		x = max((renderW-geo.Width)/2, 0)
		if !anchored {
			y = top + max(m.GetUsableHeight()/3, 0)
		}
	}
	x = max(min(x, renderW-geo.Width), 0)
	y = max(min(y, bottom-geo.Height), top)
	return x, y
}

// sidebarRowFor is the rail row drawn for a window this frame, if the rail drew
// one. The hits are the geometry the rail actually drew, so an anchor taken
// from them lands on the row the user is looking at.
//
// A pane running an agent is drawn twice, once in the agents section and once
// in the tree, so the row the keyboard cursor is on wins: that is the one the
// rename was started from. Failing that the tree row does, since it is where a
// pane lives whether or not it is running anything.
func (m *OS) sidebarRowFor(windowID string) (sidebarRowHit, bool) {
	want := sidebarRowWindow
	if m.SidebarCursor >= 0 && m.SidebarCursor < len(m.SidebarNav) {
		if n := m.SidebarNav[m.SidebarCursor]; n.WindowID == windowID {
			want = n.Kind
		}
	}
	best, ok := sidebarRowHit{}, false
	for _, h := range m.SidebarHits {
		if h.WindowID != windowID || (h.Kind != sidebarRowWindow && h.Kind != sidebarRowAgent) {
			continue
		}
		if h.Kind == want {
			return h, true
		}
		if !ok {
			best, ok = h, true
		}
	}
	return best, ok
}
