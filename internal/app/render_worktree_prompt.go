package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// The worktree dialog's drawing.
//
// It is the file create prompt's shape with a different question in it: a
// centred overlay.Dialog, a line above the fields saying what repository the
// branch comes off, and a quiet hint line under them for the field in use.
// The reasoning is the file prompt's and is not repeated here; see
// render_file_prompt.go for why this is an overlay and not a rail row.
//
// Two fields, because a worktree names two things: the branch it checks out,
// and the session it is addressed by. The session field starts empty and its
// empty state shows the name the daemon will derive, so the default is what
// the field reads as rather than something the user has to imagine.

// worktreeDialogWidth is the preferred inner width. Wide enough for the
// repository line, the longest the dialog draws, whose path is cut from the
// front like every other path in these dialogs so the tail survives.
const worktreeDialogWidth = 54

// worktreeFieldLabelW is the label column both field rows share.
const worktreeFieldLabelW = 9

// renderWorktreePrompt draws the worktree dialog.
func (m *OS) renderWorktreePrompt() (string, overlay.Geometry, []overlayRowHit) {
	pal := theme.UI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(worktreeDialogWidth, m.GetRenderWidth())

	var body []string
	body = append(body, dialogBodyLine(m.worktreePromptContext(width), width, pal.FgDim, bg))
	body = append(body,
		m.worktreePromptField(worktreeFieldBranch, "Branch", width, pal, bg),
		m.worktreePromptField(worktreeFieldName, "Session", width, pal, bg))

	// Under the fields: the hint for the field the keyboard is in, or the
	// refusal when there is one, standing where the hint stood — a second try
	// looks at the same place for what went wrong. No folder line: where the
	// worktree lands is the daemon's to say, and it says so on create.
	note, ink := m.worktreePromptNote(), pal.FgMute
	if m.worktreePrompt.Err != "" {
		note, ink = m.worktreePrompt.Err, pal.Warn
	}
	body = append(body, dialogBodyLine(note, width, ink, bg))

	content, geo := overlay.Dialog{
		Title: "new worktree",
		Width: width,
		Body:  strings.Join(body, "\n"),
		Hints: []overlay.Hint{
			{Key: overlay.EnterKey(), Label: "create"},
			{Key: "tab", Label: "next field"},
			{Key: "esc", Label: "cancel"},
		},
	}.Render(pal)
	return content, geo, nil
}

// worktreePromptField draws one field row: its label, the sigil on the field
// the keyboard is in, the text windows to its tail so what you are typing
// stays under the cursor, and the cursor on the active field only. A session
// field left empty shows the name the daemon will derive, in the quiet ink a
// default reads as.
func (m *OS) worktreePromptField(field worktreeField, label string, width int, pal overlay.Palette, bg color.Color) string {
	active := m.worktreePrompt.Field == field
	value := m.worktreeFieldText(field)

	// The empty session field reads as the derived name it will fall back to.
	quiet := false
	if value == "" && field == worktreeFieldName {
		if branch := m.WorktreePromptBranch(); branch != "" {
			value = worktree.SessionName(m.worktreePromptRepo(), branch)
			quiet = true
		}
	}

	// The inactive marker is padded to the sigil's width so the labels sit in
	// one column whether or not the field has the keyboard; the sigil is two
	// cells, a bare space would leave the inactive label one cell to the left.
	marker, ink := strings.Repeat(" ", lipgloss.Width(overlay.Sigil())), pal.FgDim
	if active {
		marker, ink = overlay.Sigil(), pal.Fg
	}
	if quiet {
		ink = pal.FgMute
	}

	text := fieldTail(printableRunes(value), max(width-4-worktreeFieldLabelW, 1))
	row := overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(marker) +
		overlay.Style(bg).Foreground(pal.FgMute).Render(labelPad(label))
	// The cursor rides where the next rune will land: after typed text, but
	// before a placeholder, which is not the user's and backspace cannot
	// reach — a cursor drawn past it reads as if the name were typed.
	if active && quiet {
		row += overlay.Cursor(" ", bg, pal.Fg)
	}
	row += overlay.Style(bg).Foreground(ink).Render(text)
	if active && !quiet {
		row += overlay.Cursor(" ", bg, pal.Fg)
	}
	return overlay.Fill(row, width, bg)
}

// labelPad pads a field label to the shared label column.
func labelPad(label string) string {
	return label + strings.Repeat(" ", max(worktreeFieldLabelW-len(label)-1, 1))
}

// worktreePromptContext is the line above the fields: which repository the
// worktree branches from. The main checkout's path is cut from the front like
// every other path in these dialogs, so the tail — the name the rail's group
// header shows — is the part that survives.
func (m *OS) worktreePromptContext(width int) string {
	// The budget is what the path may take of the line, so the tail — the
	// part that names the checkout — survives the truncation and the line
	// still closes its paren. dialogBodyLine spends one cell on its leading
	// space and truncates to width-1, so the composed line is one short of
	// the width it is handed.
	prefix := "Worktree of " + printableTitle(m.worktreePromptRepo()) + " ("
	root := truncPathLeft(shortenHome(m.worktreePrompt.RepoRoot), max(width-lipgloss.Width(prefix)-2, 1))
	return prefix + root + ")"
}

// worktreePromptNote is the standing hint under the fields, for whichever one
// the keyboard is in.
func (m *OS) worktreePromptNote() string {
	if m.worktreePrompt.Field == worktreeFieldName {
		return "Type a session name. Leave it empty to use the name shown."
	}
	return "The branch the worktree checks out."
}
