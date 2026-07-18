package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const quitDialogInnerWidth = 34

// centerOnSurface centers s within width by padding with surface-background
// spaces on both sides so the fill stays solid.
func centerOnSurface(s string, width int, bg color.Color) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	left := (width - w) / 2
	right := width - w - left
	pad := overlay.Style(bg)
	return pad.Render(strings.Repeat(" ", left)) + s + pad.Render(strings.Repeat(" ", right))
}

// pillButton renders a confirm/cancel button. The selected button is a solid
// accent pill; the rest sit on a muted card.
func pillButton(label string, selected bool, accent color.Color, pal overlay.Palette) string {
	if selected {
		return lipgloss.NewStyle().
			Background(accent).
			Foreground(pal.PillFg).
			Bold(true).
			Padding(0, 2).
			Render(label)
	}
	return lipgloss.NewStyle().
		Background(pal.Card).
		Foreground(pal.FgDim).
		Padding(0, 2).
		Render(label)
}

func (m *OS) renderQuitConfirmDialog() (string, int, int) {
	pal := theme.UI()
	bg := pal.Surface

	question := overlay.Style(bg).Foreground(pal.Fg).Render("Close all windows and quit?")

	yesSelected := m.QuitConfirmSelection == 0
	// "Yes" is the destructive action, so it takes the warn color when selected.
	yes := pillButton("Yes", yesSelected, pal.Warn, pal)
	no := pillButton("No", !yesSelected, pal.Accent, pal)
	buttons := yes + overlay.Style(bg).Render("   ") + no

	body := strings.Join([]string{
		centerOnSurface(question, quitDialogInnerWidth, bg),
		overlay.Style(bg).Render(" "),
		centerOnSurface(buttons, quitDialogInnerWidth, bg),
	}, "\n")

	panel := overlay.Panel{
		Glyph: "", // warning
		Title: "Quit TUIOS",
		Width: quitDialogInnerWidth,
		Body:  body,
		Hints: []overlay.Hint{
			{Key: "←→", Label: "select"},
			{Key: "⏎", Label: "confirm"},
			{Key: "esc", Label: "cancel"},
		},
	}

	dialog, _ := panel.Render(pal)
	return dialog, lipgloss.Width(dialog), lipgloss.Height(dialog)
}
