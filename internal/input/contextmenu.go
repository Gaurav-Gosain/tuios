package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// runContextMenuAction dispatches an action chosen from a context menu.
//
// It goes through the same ActionDispatcher that every keybinding goes through,
// which is the point: a menu row carries an action ID and nothing else, so it
// runs exactly the code the key runs. A menu that held its own copy of the
// behaviour would drift from the keybinding the moment either side changed, and
// there is already one popup in this codebase describing keys from a
// hand-written table rather than from the registry.
//
// An empty action means the click landed on the menu but not on a runnable row,
// or that the menu was dismissed; either way nothing runs.
//
// The zero KeyPressMsg is deliberate. Dispatch passes it to the handler, and no
// action reachable from a context menu reads the originating key: the handlers
// that do take their argument are the number-key window selectors, which no
// menu offers.
func runContextMenuAction(action string, o *app.OS) (*app.OS, tea.Cmd) {
	if action == "" {
		return o, nil
	}
	dispatcher := GetDispatcher()
	if !dispatcher.HasAction(action) {
		o.ClearMenuTarget()
		return o, nil
	}
	next, cmd := dispatcher.Dispatch(action, tea.KeyPressMsg{}, o)
	// What a menu row carried (a workspace, a session) lives for exactly this one
	// dispatch, so the same action reached later by key acts on what is in view.
	next.ClearMenuTarget()
	return next, cmd
}

// handleContextMenuKey drives an open context menu from the keyboard: arrows or
// j/k move the selection, enter runs it, escape closes without running
// anything.
//
// Every other key is swallowed. The menu is modal while it is up, so a
// keystroke meant for it can never fall through to a window-manager binding or
// to the shell.
func handleContextMenuKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		o.CloseContextMenu()
	case "up", "k", "shift+tab":
		o.ContextMenuMove(-1)
	case "down", "j", "tab":
		o.ContextMenuMove(1)
	case "enter", " ":
		action := o.ContextMenuSelectedAction()
		o.CloseContextMenu()
		return runContextMenuAction(action, o)
	}
	return o, nil
}
