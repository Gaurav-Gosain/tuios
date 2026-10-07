package guibridge

import (
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
)

// The action command runs a keybinding action by its registry name, the way
// the key bound to it would in the terminal client.
//
// Only the actions on the allow list below run. The bridge has no screen, so
// an action whose only effect is something it would draw (the help, the
// palette, the settings page, a which-key menu, the rail) would do nothing a
// person could see, and leave the model in a mode the renderer does not know
// it is in. The renderer draws those itself. Every action here changes the
// session: its panes, their layout, the workspaces, or the focus.

// allowedActions are the actions the action command runs.
var allowedActions = map[string]bool{
	// Panes
	"new_window": true, "close_window": true, "minimize_window": true,
	"restore_all": true, "toggle_zoom": true,
	"next_window": true, "prev_window": true, "last_pane": true,
	"focus_up": true, "focus_down": true,
	"terminal_focus_left": true, "terminal_focus_right": true,
	"terminal_focus_up": true, "terminal_focus_down": true,
	"toggle_multifocus_active": true, "toggle_multifocus_all": true,
	"prefix_next_attention": true, "prefix_next_finished": true,
	// Tiling and splits
	"toggle_tiling": true, "split_horizontal": true, "split_vertical": true,
	"rotate_split": true, "equalize_splits": true,
	"swap_left": true, "swap_right": true, "swap_up": true, "swap_down": true,
	"preselect_left": true, "preselect_right": true, "preselect_up": true, "preselect_down": true,
	"cycle_tiling_scheme":      true,
	"set_tiling_scheme_spiral": true, "set_tiling_scheme_longest_side": true,
	"set_tiling_scheme_alternate": true, "set_tiling_scheme_smart_split": true,
	"resize_master_shrink": true, "resize_master_grow": true,
	"resize_height_shrink": true, "resize_height_grow": true,
	"resize_master_shrink_left": true, "resize_master_grow_left": true,
	"resize_height_shrink_top": true, "resize_height_grow_top": true,
	"cycle_master_position":    true,
	"set_master_position_left": true, "set_master_position_right": true,
	"set_master_position_top": true, "set_master_position_bottom": true,
	"set_master_position_center": true,
	"add_master":                 true, "remove_master": true, "swap_with_master": true, "focus_master": true,
	// Floating panes
	"snap_left": true, "snap_right": true, "snap_fullscreen": true, "unsnap": true,
	"snap_corner_1": true, "snap_corner_2": true, "snap_corner_3": true, "snap_corner_4": true,
	"toggle_scratch": true,
	// Window mode's focus keys
	"nav_left": true, "nav_right": true, "nav_up": true, "nav_down": true,
	// Sessions
	"next_session": true, "prev_session": true,
	// The same work under the leader and its sub-prefixes, which is where
	// the renderer's own prefix resolver ends up.
	"prefix_new_window": true, "prefix_close_window": true,
	"prefix_next_window": true, "prefix_prev_window": true,
	"prefix_toggle_tiling": true, "prefix_fullscreen": true,
	"prefix_split_horizontal": true, "prefix_split_vertical": true,
	"prefix_rotate_split": true, "prefix_equalize_splits": true,
	"window_prefix_new": true, "window_prefix_close": true,
	"window_prefix_next": true, "window_prefix_prev": true, "window_prefix_tiling": true,
	"minimize_prefix_focused": true, "minimize_prefix_restore_all": true,
}

// numberedActions are the action families that end in one digit.
var numberedActions = []string{
	"select_window_", "switch_workspace_", "move_and_follow_",
	"resize_width_", "resize_height_", "switch_session_",
	"restore_minimized_", "prefix_select_", "minimize_prefix_restore_",
	"workspace_prefix_switch_", "workspace_prefix_move_",
}

// checkAction says why the action command will not run name, or nil.
func checkAction(name string) error {
	if name == "" {
		return fmt.Errorf("action needs name, an action name")
	}
	if allowedActions[name] {
		return nil
	}
	// The user's own [[keybindings.command]] entries: a popup, a scratch
	// pane or a new pane, each a pane the state shows.
	if strings.HasPrefix(name, config.CommandActionPrefix) {
		return nil
	}
	for _, prefix := range numberedActions {
		if n, ok := strings.CutPrefix(name, prefix); ok && len(n) == 1 && n[0] >= '0' && n[0] <= '9' {
			return nil
		}
	}
	if _, gui := config.GUIActionDescriptions[name]; gui {
		return fmt.Errorf("%s is a GUI action: the GUI runs it itself", name)
	}
	if _, known := config.ActionDescriptions[name]; known {
		return fmt.Errorf("%s shows something only a screen can show, so the bridge does not run it", name)
	}
	return fmt.Errorf("%q is not an action the bridge runs", name)
}

// splitActions are the actions that split the focused pane, and the side the
// new pane goes on.
var splitActions = map[string]layout.PreselectionDir{
	"split_vertical": layout.PreselectionRight, "prefix_split_vertical": layout.PreselectionRight,
	"split_horizontal": layout.PreselectionDown, "prefix_split_horizontal": layout.PreselectionDown,
}

// splitRefusal says why a split action will not run, or nil. The model
// refuses a split that leaves a pane under the smallest size and only shows a
// notification, which the renderer never sees, so the result says it.
func (m *model) splitRefusal(name string) error {
	dir, ok := splitActions[name]
	if !ok {
		return nil
	}
	return m.os.SplitRefusal(dir)
}
