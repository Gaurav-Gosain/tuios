package app

import (
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func (m *OS) renderQuitConfirmDialog() (string, int, int) {
	borderColor := theme.HelpBorder()
	selectedColor := theme.HelpTabActive()
	unselectedColor := theme.HelpGray()

	title := lipgloss.NewStyle().
		Foreground(selectedColor).
		Bold(true).
		Render("Quit TUIOS?")

	yesButtonContent := "yes"
	noButtonContent := "no"

	var yesButton, noButton string

	if m.QuitConfirmSelection == 0 {
		yesButton = lipgloss.NewStyle().
			Foreground(selectedColor).
			Bold(true).
			Border(lipgloss.NormalBorder()).
			BorderForeground(selectedColor).
			Padding(0, 1).
			Render(yesButtonContent)

		noButton = lipgloss.NewStyle().
			Foreground(unselectedColor).
			Border(lipgloss.NormalBorder()).
			BorderForeground(unselectedColor).
			Padding(0, 1).
			Render(noButtonContent)
	} else {
		yesButton = lipgloss.NewStyle().
			Foreground(unselectedColor).
			Border(lipgloss.NormalBorder()).
			BorderForeground(unselectedColor).
			Padding(0, 1).
			Render(yesButtonContent)

		noButton = lipgloss.NewStyle().
			Foreground(selectedColor).
			Bold(true).
			Border(lipgloss.NormalBorder()).
			BorderForeground(selectedColor).
			Padding(0, 1).
			Render(noButtonContent)
	}

	buttonRow := lipgloss.JoinHorizontal(lipgloss.Center, yesButton, "   ", noButton)

	dialogContent := lipgloss.JoinVertical(
		lipgloss.Center,
		title,
		"",
		buttonRow,
	)

	dialogBox := lipgloss.NewStyle().
		Border(getBorder()).
		BorderForeground(borderColor).
		Padding(1, 3).
		Render(dialogContent)

	width := lipgloss.Width(dialogBox)
	height := lipgloss.Height(dialogBox)

	return dialogBox, width, height
}
