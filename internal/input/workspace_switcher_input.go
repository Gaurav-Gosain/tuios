package input

import (
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleWorkspaceSwitcherInput handles keyboard input while the workspace
// switcher is open. It mirrors the session switcher's bindings, so the two
// overlays are driven the same way.
func handleWorkspaceSwitcherInput(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	keyStr := msg.String()
	filtered := app.FilterWorkspaceItems(o.WorkspaceSwitcherItems, o.WorkspaceSwitcherQuery)

	switch keyStr {
	case "esc":
		o.CloseWorkspaceSwitcher()
		return o, nil

	case "enter":
		// Same entry point as the mouse click, so the two cannot drift.
		o.WorkspaceSwitcherActivate(o.WorkspaceSwitcherSelected)
		return o, nil

	case "up", "ctrl+p":
		o.WorkspaceSwitcherMove(-1, len(filtered))
		return o, nil

	case "down", "ctrl+n":
		o.WorkspaceSwitcherMove(1, len(filtered))
		return o, nil

	case "ctrl+r":
		if target, ok := o.WorkspaceSwitcherTarget(o.WorkspaceSwitcherSelected); ok {
			o.BeginRenameWorkspace(target.Number)
		}
		return o, nil

	case "backspace":
		if len(o.WorkspaceSwitcherQuery) > 0 {
			_, size := utf8.DecodeLastRuneInString(o.WorkspaceSwitcherQuery)
			o.WorkspaceSwitcherQuery = o.WorkspaceSwitcherQuery[:len(o.WorkspaceSwitcherQuery)-size]
			o.WorkspaceSwitcherSelected = 0
			o.WorkspaceSwitcherScroll = 0
		}
		return o, nil

	case "ctrl+u":
		o.WorkspaceSwitcherQuery = ""
		o.WorkspaceSwitcherSelected = 0
		o.WorkspaceSwitcherScroll = 0
		return o, nil

	default:
		// A space and any typed rune, the way the session switcher takes
		// them. The gate here was printable ASCII only, so a name with a
		// space in it could not be searched for past its first word.
		text := msg.Text
		if keyStr == "space" {
			text = " "
		}
		if text != "" {
			o.WorkspaceSwitcherQuery += text
			o.WorkspaceSwitcherSelected = 0
			o.WorkspaceSwitcherScroll = 0
		}
		return o, nil
	}
}
