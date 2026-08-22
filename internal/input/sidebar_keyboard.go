package input

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleFocusSidebar enters the rail's keyboard scope. Bound to "s" in window
// mode and reached from anywhere via the ctrl+b o prefix.
func handleFocusSidebar(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.EnterSidebarFocus()
	return o, nil
}

// handleToggleFocusSidebar sends the keyboard to the rail, or back to the panes
// if the rail already has it. One key that goes both ways, so exploring costs
// the same chord twice rather than a chord and an esc.
func handleToggleFocusSidebar(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.SidebarFocused {
		o.ExitSidebarFocus()
		return o, nil
	}
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
	if o.KeybindRegistry == nil {
		o.ExitSidebarFocus()
		return o, nil
	}
	action := lookupAction(msg, o.KeybindRegistry.GetSidebarAction)
	if action == "" {
		// esc leaves the rail when nothing else claims it. The scope swallows
		// unbound keys, so a config that resolves no rail action (one written
		// before this section existed, or a rebound exit) would otherwise trap
		// the keyboard here with no way back to the panes.
		//
		// Checked after the lookup, not before it. Before, a user who put esc on
		// a rail action of their own had it silently overridden, which is the
		// same bug as hardcoding the key: the config said one thing and the
		// program did another, with nothing to tell them apart.
		if key == "esc" {
			o.ExitSidebarFocus()
		}
		return o, nil // consumed either way: the rail owns the keyboard
	}

	// Workspace/session jumps share a numeric suffix.
	if after, ok := strings.CutPrefix(action, sidebarActJumpPrefix); ok {
		if n, err := strconv.Atoi(after); err == nil {
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
	case sidebarActPalette:
		// The rail lists what exists; the palette finds it by name across every
		// session and filters it by who needs a human. Rail focus is kept, so
		// closing the palette comes back to the row the cursor was on; a row that
		// actually relocates the user drops it on the way out.
		return o, o.OpenCommandPalette()
	case sidebarActAgentFilter:
		o.SidebarCycleAgentsFilter()
	case sidebarActAgentSort:
		o.SidebarCycleAgentsSort()
	case sidebarActNarrow:
		o.SidebarSetCollapsed(true)
	case sidebarActWiden:
		o.SidebarSetCollapsed(false)
	case sidebarActKill:
		o.SidebarOpenCursorMenu(true) // the cursor row's menu, opened on its destructive row
	case sidebarActMenu:
		o.SidebarOpenCursorMenu(false)
	case sidebarActHelp:
		o.OpenHelpAtCategory(app.HelpCategorySidebar)
	case sidebarActRename:
		o.SidebarRenameCursor()
	case sidebarActAccent:
		o.SidebarAccentCursor()
	case sidebarActNewSession:
		o.SidebarNewSession()
	case sidebarActNewWindow:
		// The keyboard reach for the terminals header's "+". It makes the pane in
		// the attached session, which is the only one that section ever lists
		// while the rail holds the keyboard.
		o.SidebarNewWindow("")
		o.ExitSidebarFocus() // the new pane is what was asked for
	case sidebarActExit:
		o.ExitSidebarFocus()
	}
	return o, nil
}
