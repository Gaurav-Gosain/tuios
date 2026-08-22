package input

import (
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
			o.WorkspaceSwitcherQuery = o.WorkspaceSwitcherQuery[:len(o.WorkspaceSwitcherQuery)-1]
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
		if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
			o.WorkspaceSwitcherQuery += keyStr
			o.WorkspaceSwitcherSelected = 0
			o.WorkspaceSwitcherScroll = 0
		}
		return o, nil
	}
}
