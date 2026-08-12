package app

import (
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// renameDialogWidth is the micro-dialog's preferred inner width: a name and a
// cursor, and nothing that would make it a panel.
const renameDialogWidth = 28

// renameHints are built per render because the key glyph follows ASCII mode.
func renameHints() []overlay.Hint {
	return []overlay.Hint{
		{Key: overlay.EnterKey(), Label: "save"},
		{Key: "esc", Label: "cancel"},
	}
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
//
// It is centred on the screen like every other modal. Anchoring it to the rail
// row it renamed put it in the top-left corner, far from where the eye had to
// go to read what it was typing, and the row it pointed at is right there in
// the rail anyway.
func (m *OS) renderRenameDialog() (string, overlay.Geometry, int, int, bool) {
	if !m.Renaming() {
		return "", overlay.Geometry{}, 0, 0, false
	}
	pal := theme.UI()
	renderW := m.GetRenderWidth()
	inner := overlay.DialogFitWidth(renameDialogWidth, renderW)

	// Sigil, a space, the field, and one cell of right pad, so the cursor never
	// sits against the frame.
	field := renameFieldText(printableTitle(m.RenameBuffer), max(inner-4, 1))
	body := overlay.Style(pal.Canvas).Render(" ") +
		overlay.Style(pal.Canvas).Foreground(pal.AccentBright).Bold(true).Render(overlay.Sigil()) +
		overlay.Style(pal.Canvas).Foreground(pal.Fg).Render(field) +
		overlay.Cursor(" ", pal.Canvas, pal.Fg)

	content, geo := overlay.Dialog{
		Title: m.RenameDialogTitle(),
		Width: inner,
		Body:  body,
		Hints: renameHints(),
	}.Render(pal)

	x, y := m.centerOrigin(geo.Width, geo.Height)
	return content, geo, x, y, true
}
