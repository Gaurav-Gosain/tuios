package input

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// handleFocusSidebar enters the rail's keyboard scope. Bound to "s" in window
// mode and reached from anywhere via the ctrl+b o prefix.
func handleFocusSidebar(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.EnterSidebarFocus()
	return o, nil
}

// HandleSidebarKey routes a keypress while the rail owns the keyboard. It looks
// the key up in the [keybindings] sidebar section and mutates the OS through the
// same methods the mouse handlers call, so keyboard and mouse can never diverge.
// Keys with no rail binding are swallowed, not passed to the pane underneath:
// the whole point of the scope is that pane bindings do not fire here.
func HandleSidebarKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	key := msg.String()
	// esc always leaves the rail, whatever the config says. The scope swallows
	// unbound keys, so a config that resolves no rail action (one written before
	// this section existed, or a rebound exit) would otherwise trap the keyboard
	// here with no way back to the panes.
	if key == "esc" {
		o.ExitSidebarFocus()
		return o, nil
	}
	if o.KeybindRegistry == nil {
		o.ExitSidebarFocus()
		return o, nil
	}
	action := o.KeybindRegistry.GetSidebarAction(key)
	if action == "" {
		return o, nil // consumed: the rail owns the keyboard
	}

	// Workspace/session jumps share a numeric suffix.
	if strings.HasPrefix(action, sidebarActJumpPrefix) {
		if n, err := strconv.Atoi(strings.TrimPrefix(action, sidebarActJumpPrefix)); err == nil {
			o.SidebarJumpToSession(n)
		}
		return o, nil
	}

	switch action {
	case sidebarActCursorDown:
		o.SidebarCursorMove(1)
	case sidebarActCursorUp:
		o.SidebarCursorMove(-1)
	case sidebarActFirst:
		o.SidebarCursorFirst()
	case sidebarActLast:
		o.SidebarCursorLast()
	case sidebarActExpand:
		o.SidebarCursorExpand()
	case sidebarActCollapse:
		o.SidebarCursorCollapse()
	case sidebarActActivate:
		if o.SidebarActivateCursor() {
			o.ExitSidebarFocus() // activating a window is a request for that pane
		}
	case sidebarActReorderDown:
		o.SidebarReorderCursor(1)
	case sidebarActReorderUp:
		o.SidebarReorderCursor(-1)
	case sidebarActSection:
		o.SidebarCycleSection()
	case sidebarActKill:
		o.SidebarOpenCursorMenu(true) // session menu, Kill among its rows
	case sidebarActMenu:
		o.SidebarOpenCursorMenu(false)
	case sidebarActRename:
		o.SidebarRenameCursor()
	case sidebarActAccent:
		o.SidebarAccentCursor()
	case sidebarActNewSession:
		o.SidebarNewSession()
	case sidebarActNewWindow:
		// Registered now, real behavior arrives in M6. A hint keeps the key from
		// feeling dead without pretending it did something.
		o.ShowNotification("Not available yet", "info", config.NotificationDuration)
	case sidebarActExit:
		o.ExitSidebarFocus()
	}
	return o, nil
}
