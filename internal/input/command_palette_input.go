package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleCommandPaletteInput handles keyboard input when the command palette is open.
func handleCommandPaletteInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	keyStr := msg.String()

	switch keyStr {
	case "esc":
		o.CloseCommandPalette()
		return o, nil

	case "enter":
		return o, o.ActivateCommandPalette()

	case "up", "ctrl+p":
		o.PaletteMove(-1)
		return o, nil

	case "down", "ctrl+n":
		o.PaletteMove(1)
		return o, nil

	default:
		if changed, _ := editFilterQuery(msg, &o.CommandPaletteQuery, true); changed {
			o.CommandPaletteSelected = 0
			o.CommandPaletteScroll = 0
		}
		return o, nil
	}
}
