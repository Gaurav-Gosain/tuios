package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const renameDialogWidth = 31

const noteDialogWidth = 60

const renameHintClear = 1

func renameHints() []overlay.Hint {
	return []overlay.Hint{
		{Key: overlay.EnterKey(), Label: "save"},
		{Key: "ctrl+u", Label: "clear"},
		{Key: "esc", Label: "cancel"},
	}
}

func (m *OS) renderRenameDialog() (string, overlay.Geometry, int, int, bool) {
	if !m.Renaming() {
		return "", overlay.Geometry{}, 0, 0, false
	}
	pal := m.overlayUI()
	renderW := m.GetRenderWidth()
	want := renameDialogWidth
	if m.RenameKind == RenameNote {
		want = noteDialogWidth
	}
	inner := overlay.DialogFitWidth(want, renderW)

	body := fieldRow(&m.rename, inner, pal.Canvas, pal)

	content, geo := overlay.Dialog{
		Title: m.RenameDialogTitle(),
		Width: inner,
		Body:  body,
		Hints: renameHints(),
	}.Render(pal)

	x, y := m.centerOrigin(geo.Width, geo.Height)
	return content, geo, x, y, true
}
