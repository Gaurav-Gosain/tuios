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

	case "backspace":
		if len(o.CommandPaletteQuery) > 0 {
			o.CommandPaletteQuery = o.CommandPaletteQuery[:len(o.CommandPaletteQuery)-1]
			o.CommandPaletteSelected = 0
			o.CommandPaletteScroll = 0
		}
		return o, nil

	case "ctrl+u":
		o.CommandPaletteQuery = ""
		o.CommandPaletteSelected = 0
		o.CommandPaletteScroll = 0
		return o, nil

	default:
		// Accept printable characters
		if keyStr == "space" {
			o.CommandPaletteQuery += " "
			o.CommandPaletteSelected = 0
			o.CommandPaletteScroll = 0
		} else if msg.Text != "" {
			// Use msg.Text for actual typed text (handles all printable chars)
			o.CommandPaletteQuery += msg.Text
			o.CommandPaletteSelected = 0
			o.CommandPaletteScroll = 0
		} else if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
			o.CommandPaletteQuery += keyStr
			o.CommandPaletteSelected = 0
			o.CommandPaletteScroll = 0
		}
		return o, nil
	}
}
