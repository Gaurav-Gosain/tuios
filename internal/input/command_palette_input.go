package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleCommandPaletteInput handles keyboard input when the command palette is open.
func handleCommandPaletteInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	keyStr := msg.String()

	items := app.GetCommandPaletteItems()
	filtered := app.FilterCommandPalette(items, o.CommandPaletteQuery)

	switch keyStr {
	case "esc":
		o.ShowCommandPalette = false
		o.CommandPaletteQuery = ""
		o.CommandPaletteSelected = 0
		o.CommandPaletteScroll = 0
		return o, nil

	case "enter":
		if len(filtered) > 0 && o.CommandPaletteSelected < len(filtered) {
			action := filtered[o.CommandPaletteSelected].Action
			o.ShowCommandPalette = false
			o.CommandPaletteQuery = ""
			o.CommandPaletteSelected = 0
			o.CommandPaletteScroll = 0
			if action != nil {
				return action(o)
			}
		}
		return o, nil

	case "up", "ctrl+p":
		if o.CommandPaletteSelected > 0 {
			o.CommandPaletteSelected--
			// Scroll up if selection is above visible area
			if o.CommandPaletteSelected < o.CommandPaletteScroll {
				o.CommandPaletteScroll = o.CommandPaletteSelected
			}
		}
		return o, nil

	case "down", "ctrl+n":
		if o.CommandPaletteSelected < len(filtered)-1 {
			o.CommandPaletteSelected++
			// Scroll down if selection goes below visible area (max 10 visible items)
			maxVisible := 10
			if o.CommandPaletteSelected >= o.CommandPaletteScroll+maxVisible {
				o.CommandPaletteScroll = o.CommandPaletteSelected - maxVisible + 1
			}
		}
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
