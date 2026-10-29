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

	if listKey(o, keyStr, false, listPage, func(d int) { o.WorkspaceSwitcherMove(d, len(filtered)) }) {
		return o, nil
	}

	switch keyStr {
	case "esc":
		o.CloseWorkspaceSwitcher()
		return o, nil

	case "enter":
		// Same entry point as the mouse click, so the two cannot drift.
		o.WorkspaceSwitcherActivate(o.WorkspaceSwitcherSelected)
		return o, nil

	case "ctrl+r":
		if target, ok := o.WorkspaceSwitcherTarget(o.WorkspaceSwitcherSelected); ok {
			o.BeginRenameWorkspace(target.Number)
		}
		return o, nil

	default:
		// A space and any typed rune, the way the session switcher takes
		// them. The gate here was printable ASCII only, so a name with a
		// space in it could not be searched for past its first word.
		if changed, _ := editFilterQuery(msg, &o.WorkspaceSwitcherQuery, true); changed {
			o.WorkspaceSwitcherSelected = 0
			o.WorkspaceSwitcherScroll = 0
		}
		return o, nil
	}
}
