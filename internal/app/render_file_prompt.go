package app

import (
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const fileDialogWidth = 54

func (m *OS) filePromptTitle() string {
	switch m.filePrompt.Kind {
	case filePromptCreate:
		return "new file or folder"
	case filePromptRename:
		return "rename"
	case filePromptConfirm:
		if m.filePrompt.Trash {
			return "move to trash"
		}
		return "delete permanently"
	}
	return ""
}

func (m *OS) renderFileDialog() (string, overlay.Geometry, []overlayRowHit) {
	if m.filePrompt.Kind == filePromptConfirm {
		return m.renderFileConfirm()
	}
	return m.renderFileNamePrompt()
}

func (m *OS) renderFileNamePrompt() (string, overlay.Geometry, []overlayRowHit) {
	pal := m.overlayUI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(fileDialogWidth, m.modalRoomWidth())

	var body []string
	body = append(body, dialogBodyLine(m.filePromptContext(width), width, pal.FgDim, bg))
	if m.filePrompt.Kind == filePromptRename {
		body = append(body, dialogBodyLine(m.filePromptTargetPath(width), width, pal.FgMute, bg))
	}

	body = append(body, overlay.Fill(fieldRow(&m.filePrompt.Input, width, bg, pal), width, bg))

	note, ink := m.filePromptNote(), pal.FgMute
	if m.filePrompt.Err != "" {
		note, ink = m.filePrompt.Err, pal.Warn
	}
	body = append(body, dialogBodyLine(note, width, ink, bg))

	content, geo := overlay.Dialog{
		Title: m.filePromptTitle(),
		Width: width,
		Body:  strings.Join(body, "\n"),
		Hints: []overlay.Hint{
			{Key: overlay.EnterKey(), Label: "save"},
			{Key: "esc", Label: "cancel"},
		},
	}.Render(pal)
	return content, geo, nil
}

func (m *OS) filePromptContext(width int) string {
	if m.filePrompt.Kind == filePromptRename {
		return "Rename " + printableTitle(m.filePrompt.Target)
	}
	return "In " + truncPathLeft(shortenHome(m.filePrompt.Dir), max(width-4, 1))
}

func (m *OS) filePromptTargetPath(width int) string {
	full := filepath.Join(m.filePrompt.Dir, m.filePrompt.Target)
	return truncPathLeft(shortenHome(full), max(width-4, 1))
}

func (m *OS) filePromptNote() string {
	if m.filePrompt.Kind == filePromptCreate {
		return "Put / at the end to make a folder."
	}
	return "The new name goes in this folder."
}

func (m *OS) renderFileConfirm() (string, overlay.Geometry, []overlayRowHit) {
	pal := m.overlayUI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(fileDialogWidth, m.modalRoomWidth())
	m.filePrompt.List.Cursor = clampInt(m.filePrompt.List.Cursor, 0, fileConfirmRowCount-1)

	path := ""
	if len(m.filePrompt.Paths) > 0 {
		path = truncPathLeft(shortenHome(filepath.Dir(m.filePrompt.Paths[0])), max(width-4, 1))
		if len(m.filePrompt.Paths) == 1 {
			path = truncPathLeft(shortenHome(m.filePrompt.Paths[0]), max(width-4, 1))
		}
	}

	outcomeInk := pal.Warn
	if m.filePrompt.Trash {
		outcomeInk = pal.FgDim
	}

	body := []string{
		overlay.Fill(overlay.Style(bg).Render(" ")+
			overlay.Style(bg).Foreground(pal.Fg).Bold(true).
				Render(overlay.Truncate(m.fileConfirmQuestion(), max(width-1, 1))), width, bg),
		dialogBodyLine(path, width, pal.FgMute, bg),
		dialogBodyLine(m.fileConfirmOutcome(), width, outcomeInk, bg),
		overlay.Fill(overlay.Style(bg).Render(" ")+overlay.DashRule(max(width-2, 0), bg, pal), width, bg),
		m.fileConfirmRow(fileConfirmRowCancel, width, pal),
		m.fileConfirmRow(fileConfirmRowGo, width, pal),
	}

	content, geo := overlay.Dialog{
		Title: m.filePromptTitle(),
		Width: width,
		Body:  strings.Join(body, "\n"),
		Hints: []overlay.Hint{
			{Key: overlay.EnterKey(), Label: "run"},
			{Key: "esc", Label: "cancel"},
		},
	}.Render(pal)

	hits := make([]overlayRowHit, 0, fileConfirmRowCount)
	for i := range fileConfirmRowCount {
		y := geo.BodyY + 4 + i
		hits = append(hits, overlayRowHit{
			Rect: overlay.Rect{X0: 0, Y0: y, X1: geo.Width, Y1: y + 1},
			Idx:  i,
		})
	}
	return content, geo, hits
}

func (m *OS) fileConfirmRow(idx, width int, pal overlay.Palette) string {
	bg := pal.Canvas
	cursor := m.filePrompt.List.Cursor == idx
	if cursor {
		bg = pal.Surface
	}
	marker := " "
	if cursor {
		marker = overlay.SigilMark()
	}

	label, ink := "Cancel", pal.FgDim
	if idx == fileConfirmRowGo {
		label, ink = "Delete permanently", pal.FgMute
		if m.filePrompt.Trash {
			label = "Move to trash"
		}
		if cursor {
			ink = pal.Warn
		}
	} else if cursor {
		ink = pal.Fg
	}

	row := overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(marker) +
		overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(ink).Bold(cursor).Render(label)
	return overlay.Fill(row, width, bg)
}
