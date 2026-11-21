package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

const worktreeDialogWidth = 54

const worktreeFieldLabelW = 9

func (m *OS) renderWorktreePrompt() (string, overlay.Geometry, []overlayRowHit) {
	pal := m.overlayUI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(worktreeDialogWidth, m.modalRoomWidth())

	var body []string
	body = append(body, dialogBodyLine(m.worktreePromptContext(width), width, pal.FgDim, bg))
	body = append(body,
		m.worktreePromptField(worktreeFieldBranch, "Branch", width, pal, bg),
		m.worktreePromptField(worktreeFieldName, "Session", width, pal, bg))

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

func (m *OS) worktreePromptField(field worktreeField, label string, width int, pal overlay.Palette, bg color.Color) string {
	active := m.worktreePrompt.Field == field
	value := m.worktreeFieldText(field)

	quiet := false
	if value == "" && field == worktreeFieldName {
		if branch := m.WorktreePromptBranch(); branch != "" {
			value = worktree.SessionName(m.worktreePromptRepo(), branch)
			quiet = true
		}
	}

	marker, ink := strings.Repeat(" ", lipgloss.Width(overlay.Sigil())), pal.FgDim
	if active {
		marker, ink = overlay.Sigil(), pal.Fg
	}
	if quiet {
		ink = pal.FgMute
	}

	room := max(width-4-worktreeFieldLabelW, 1)
	text := fieldTail(printableRunes(value), room)
	row := overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(marker) +
		overlay.Style(bg).Foreground(pal.FgMute).Render(labelPad(label))
	if active && quiet {
		row += overlay.Cursor(" ", bg, pal.Fg)
	}
	if active && !quiet {
		before, at, after := m.worktreeFieldBuffer(field).Parts()
		row += overlay.FieldLine(before, at, after, room, bg, pal.Fg)
		return overlay.Fill(row, width, bg)
	}
	row += overlay.Style(bg).Foreground(ink).Render(text)
	return overlay.Fill(row, width, bg)
}

func labelPad(label string) string {
	return label + strings.Repeat(" ", max(worktreeFieldLabelW-len(label)-1, 1))
}

func (m *OS) worktreePromptContext(width int) string {
	prefix := "Worktree of " + printableTitle(m.worktreePromptRepo()) + " ("
	root := truncPathLeft(shortenHome(m.worktreePrompt.RepoRoot), max(width-lipgloss.Width(prefix)-2, 1))
	return prefix + root + ")"
}

func (m *OS) worktreePromptNote() string {
	if m.worktreePrompt.Field == worktreeFieldName {
		return "Type a session name. Leave it empty to use the name shown."
	}
	return "The branch the worktree checks out."
}
