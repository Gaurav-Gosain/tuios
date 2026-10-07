package guibridge

import (
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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
	// Sessions
	"next_session": true, "prev_session": true,
}

// numberedActions are the action families that take a digit, 1 to 9.
var numberedActions = []string{
	"select_window_", "switch_workspace_", "move_and_follow_",
	"resize_width_", "resize_height_", "switch_session_",
}

// checkAction says why the action command will not run name, or nil.
func checkAction(name string) error {
	if name == "" {
		return fmt.Errorf("action needs name, an action name")
	}
	if allowedActions[name] {
		return nil
	}
	for _, prefix := range numberedActions {
		if n, ok := strings.CutPrefix(name, prefix); ok && len(n) >= 1 {
			if _, known := config.ActionDescriptions[name]; known {
				return nil
			}
		}
	}
	if _, gui := config.GUIActionDescriptions[name]; gui {
		return fmt.Errorf("%s is a GUI action: the GUI runs it itself", name)
	}
	if _, known := config.ActionDescriptions[name]; known {
		return fmt.Errorf("%s shows something only a screen can show, so the bridge does not run it", name)
	}
	return fmt.Errorf("%q is not an action", name)
}
