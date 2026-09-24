package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleCommandPaletteInput handles keyboard input when the command palette is open.
func handleCommandPaletteInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	keyStr := msg.String()

	if listKey(keyStr, false, o.PalettePageRows(), o.PaletteMove) {
		return o, nil
	}

	switch keyStr {
	case "esc":
		o.CloseCommandPalette()
		return o, nil

	case "enter":
		return o, o.ActivateCommandPalette()

	default:
		if changed, _ := editFilterQuery(msg, &o.CommandPaletteQuery, true); changed {
			o.CommandPaletteSelected = 0
			o.CommandPaletteScroll = 0
		}
		return o, nil
	}
}
