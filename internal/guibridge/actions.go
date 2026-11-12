package guibridge

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// The action command runs a keybinding action by its registry name, the way
// the key bound to it would in the terminal client.
//
// Only the actions the action table marks GUI run. The bridge has no screen, so
// an action whose only effect is something it would draw (the help, the
// palette, the settings page, a which-key menu, the rail) would do nothing a
// person could see, and leave the model in a mode the renderer does not know
// it is in. The renderer draws those itself. Every action here changes the
// session: its panes, their layout, the workspaces, or the focus.

// The actions the action command runs are the entries of the action table
// with GUI set (config.GUIRunnable), numbered families included.

// checkAction says why the action command will not run name, or nil.
func checkAction(name string) error {
	if name == "" {
		return errfmt.Errorf("action needs name, an action name")
	}
	if config.GUIRunnable(name) {
		return nil
	}
	// The user's own [[keybindings.command]] entries: a popup, a scratch
	// pane or a new pane, each a pane the state shows.
	if strings.HasPrefix(name, config.CommandActionPrefix) {
		return nil
	}
	if _, gui := config.GUIActionDescriptions[name]; gui {
		return errfmt.Errorf("%s is a GUI action: the GUI runs it itself", name)
	}
	if _, known := config.ActionDescriptions[name]; known {
		return errfmt.Errorf("%s shows something only a screen can show, so the bridge does not run it", name)
	}
	return errfmt.Errorf("%q is not an action the bridge runs", name)
}

// splitActions are the actions that split the focused pane, and the side the
// new pane goes on.
// An alias (prefix_split_vertical) is looked up by the action's own name.
var splitActions = map[string]layout.PreselectionDir{
	"split_vertical":   layout.PreselectionRight,
	"split_horizontal": layout.PreselectionDown,
}

// splitRefusal says why a split action will not run, or nil. The model
// refuses a split that leaves a pane under the smallest size and only shows a
// notification, which the renderer never sees, so the result says it.
func (m *model) splitRefusal(name string) error {
	dir, ok := splitActions[config.CanonicalAction(name)]
	if !ok {
		return nil
	}
	return m.os.SplitRefusal(dir)
}
