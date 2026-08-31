package app

import (
	"image/color"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The file action dialogs.
//
// They are centred overlays and not rows in the rail, and that is the whole of
// the layout decision. The rail is twenty-six columns on a default config and
// about twenty-two of them are left after the gutter and the glyph column. A
// delete confirmation has to carry a question, the path it is about, whether
// the file comes back, and two answers. Four of those five things do not fit on
// one twenty-two column row, and a confirmation whose answers are off the
// bottom of the visible listing is not a confirmation.
//
// The rest of this codebase already answers focused questions this way: the
// rename editor is a centred overlay.Dialog and so is the close-session
// confirmation. These are the same object, so a user who has renamed a pane has
// already used the create prompt.
//
// The rail keeps drawing underneath, with the row the dialog is about still in
// it. That is the old-and-new comparison in place: the name on the row, the new
// one in the field.

// fileDialogWidth is the preferred inner width. Wide enough for a path and a
// sentence about whether the file comes back, which is the longest line either
// dialog draws.
const fileDialogWidth = 54

// filePromptTitle is the word in the dialog's top border.
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

// renderFileDialog draws whichever file dialog is open, with its geometry and
// its clickable rows. The rows are recorded here, as the dialog draws them, so
// no handler ever works out where a row landed.
func (m *OS) renderFileDialog() (string, overlay.Geometry, []overlayRowHit) {
	if m.filePrompt.Kind == filePromptConfirm {
		return m.renderFileConfirm()
	}
	return m.renderFileNamePrompt()
}

// renderFileNamePrompt draws the create and rename prompts. One dialog for
// both: they ask the same question about a different starting point, and the
// rule for what the answer may say is the same rule.
func (m *OS) renderFileNamePrompt() (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(fileDialogWidth, m.GetRenderWidth())

	var body []string
	body = append(body, fileDialogLine(m.filePromptContext(width), width, pal.FgDim, bg))
	// A rename is the second thing here that changes a file that already
	// exists, and the folder it acts in comes from the pane rather than from
	// the user. So it names the whole path, the way the delete confirmation
	// does and for the same reason: "Rename notes.txt" is true of a file in
	// every folder on the disk. A create says its folder on the line above
	// already, because that is the whole of what it has to say.
	if m.filePrompt.Kind == filePromptRename {
		body = append(body, fileDialogLine(m.filePromptTargetPath(width), width, pal.FgMute, bg))
	}

	// The field windows to its tail, so a name longer than the dialog scrolls
	// left and what you are typing stays under the cursor.
	field := renameFieldText(printableRunes(m.filePrompt.Input), max(width-4, 1))
	body = append(body, overlay.Fill(
		overlay.Style(bg).Render(" ")+
			overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(overlay.Sigil())+
			overlay.Style(bg).Foreground(pal.Fg).Render(field)+
			overlay.Cursor(" ", bg, pal.Fg), width, bg))

	// One line under the field, and it is the refusal when there is one. A
	// prompt that both refused a name and went on explaining the trailing slash
	// would bury the answer to the question the user just asked.
	note, ink := m.filePromptNote(), pal.FgMute
	if m.filePrompt.Err != "" {
		note, ink = m.filePrompt.Err, pal.Warn
	}
	body = append(body, fileDialogLine(note, width, ink, bg))

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

// filePromptContext is the line above the field: where a create lands, and
// which entry a rename is about. Without it the field is a name with no folder,
// and the rail behind the dialog is the only thing saying where "here" is.
func (m *OS) filePromptContext(width int) string {
	if m.filePrompt.Kind == filePromptRename {
		return "Rename " + printableTitle(m.filePrompt.Target)
	}
	return "In " + truncPathLeft(shortenHome(m.filePrompt.Dir), max(width-4, 1))
}

// filePromptTargetPath is the full path of the entry a rename acts on, cut from
// the front like every other path in these dialogs so the tail survives.
func (m *OS) filePromptTargetPath(width int) string {
	full := filepath.Join(m.filePrompt.Dir, m.filePrompt.Target)
	return truncPathLeft(shortenHome(full), max(width-4, 1))
}

// filePromptNote is the standing hint under the field.
func (m *OS) filePromptNote() string {
	if m.filePrompt.Kind == filePromptCreate {
		return "Put / at the end to make a folder."
	}
	return "The new name goes in this folder."
}

// renderFileConfirm draws the delete confirmation.
//
// Four things, in the order a person needs them: what goes, where it is, what
// happens to it, and the two answers. Cancel is drawn first and is where the
// dialog opens, so enter on a dialog nobody has touched removes nothing.
func (m *OS) renderFileConfirm() (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(fileDialogWidth, m.GetRenderWidth())
	m.filePrompt.Selected = clampInt(m.filePrompt.Selected, 0, fileConfirmRowCount-1)

	// The path is cut from the front, like the rail's own header: the tail is
	// the file being deleted and the head is the disk it is on. A confirmation
	// that truncated to "/home/gaurav/dev/tui…" would have cut off the only
	// part that says which file.
	path := ""
	if len(m.filePrompt.Paths) > 0 {
		path = truncPathLeft(shortenHome(filepath.Dir(m.filePrompt.Paths[0])), max(width-4, 1))
		if len(m.filePrompt.Paths) == 1 {
			path = truncPathLeft(shortenHome(m.filePrompt.Paths[0]), max(width-4, 1))
		}
	}

	// The outcome line is the one loud thing in the dialog, and a permanent
	// delete is the case worth the warning colour. A trash delete says where the
	// file goes in the quiet ink, because it is the recoverable one.
	outcomeInk := pal.Warn
	if m.filePrompt.Trash {
		outcomeInk = pal.FgDim
	}

	body := []string{
		overlay.Fill(overlay.Style(bg).Render(" ")+
			overlay.Style(bg).Foreground(pal.Fg).Bold(true).
				Render(overlay.Truncate(m.fileConfirmQuestion(), max(width-1, 1))), width, bg),
		fileDialogLine(path, width, pal.FgMute, bg),
		fileDialogLine(m.fileConfirmOutcome(), width, outcomeInk, bg),
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

	// One rectangle per drawn answer, in drawn order, recorded as it is drawn.
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

// fileConfirmRow draws one answer. The destructive one takes the warn colour
// under the cursor and stays muted off it, which is the weight split the
// close-session dialog already uses.
func (m *OS) fileConfirmRow(idx, width int, pal overlay.Palette) string {
	bg := pal.Canvas
	cursor := m.filePrompt.Selected == idx
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

// fileDialogLine is one indented, truncated, canvas-filled body line.
func fileDialogLine(text string, width int, ink, bg color.Color) string {
	return overlay.Fill(overlay.Style(bg).Render(" ")+
		overlay.Style(bg).Foreground(ink).Render(overlay.Truncate(text, max(width-1, 1))), width, bg)
}
