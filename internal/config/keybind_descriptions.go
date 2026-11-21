package config

import (
	"fmt"
	"strconv"
	"strings"
)

var scopedDescriptions = map[string]map[string]string{
	ScopeSidebar: {
		"exit":          "Leave the rail, back to the panes",
		"cursor_down":   "Move the cursor down a row",
		"cursor_up":     "Move the cursor up a row",
		"first":         "First row, or the first file from a file row",
		"last":          "Last row, or the last file from a file row",
		"collapse":      "Previous section, or close a tree folder",
		"expand":        "Next section, or open a tree folder",
		"activate":      "Activate the row: attach, or focus the pane",
		"reorder_down":  "Move the session or machine down the rail",
		"reorder_up":    "Move the session or machine up the rail",
		"section":       "Cycle the sessions, terminals and agents sections",
		"switch_edge":   "Focus the other visible rail",
		"agents_filter": "Agents: all sessions, or this one",
		"agents_sort":   "Agents: needs you, priority, or recency",
		"file_search":   "Files: search below the folder the sidebar shows",
		"mail":          "Open the mailbox, for the pane under the cursor",
		"palette":       "Find a pane in any session, or filter by @state",
		"narrow":        "Collapse the rail. On the divider: split down",
		"widen":         "Expand the rail. On the divider: split up",
		"new_session":   "New session, the sessions header's +",
		"new_window":    "New terminal, the terminals header's +",
		"menu":          "Open the menu for the row under the cursor",
		"kill":          "Open that row's menu on its Close or Kill row",
		"rename":        "Rename the window under the cursor",
		"accent":        "Recolor the window under the cursor",
		"help":          "Show this list of the rail's keys",
	},
	ScopePrefixWindow: {
		"new_window":           "New window",
		"close_window":         "Close window",
		"window_prefix_rename": "Rename window",
		"prefix_next_window":   "Next window",
		"prefix_prev_window":   "Previous window",
		"toggle_tiling":        "Toggle tiling mode",
		"window_prefix_float":  "Float or tile the pane",
		"prefix_cancel":        "Cancel the window prefix",
	},
	ScopePrefixMinimize: {
		"minimize_prefix_focused":    "Minimize the focused window",
		"restore_all":                "Restore all minimized windows",
		"minimize_prefix_focus_only": "Minimize the other windows on the workspace, or restore them",
		"prefix_cancel":              "Cancel the minimize prefix",
	},
	ScopePrefixWorkspce: {
		"prefix_cancel": "Cancel the workspace prefix",
	},
}

var numberedDescriptions = map[string]map[string]string{
	ScopeSidebar:        {"jump_": "Jump to session %s in the rail"},
	ScopePrefixMinimize: {"minimize_prefix_restore_": "Restore minimized window %s"},
	ScopePrefixWorkspce: {
		"workspace_prefix_switch_": "Switch to workspace %s",
		"workspace_prefix_move_":   "Move the window to workspace %s",
	},
	ScopeWindowMode: {"restore_minimized_": "Restore minimized window %s"},
}

// ScopedDescription is the description of action as it acts in scope: the
// scope's own text when it has one, then ActionDescriptions, then the action
// name with its underscores opened.
func ScopedDescription(scope, action string, workspaceOffset int) string {
	if number, ok := workspaceActionNumber(action); ok && workspaceOffset != 0 {
		return strings.Replace(ScopedDescription(scope, action, 0), strconv.Itoa(number), strconv.Itoa(number-workspaceOffset), 1)
	}
	if d := scopedDescriptions[scope][action]; d != "" {
		return d
	}
	for prefix, format := range numberedDescriptions[scope] {
		if n, ok := strings.CutPrefix(action, prefix); ok && len(n) == 1 && n[0] >= '0' && n[0] <= '9' {
			return fmt.Sprintf(format, n)
		}
	}
	return describeAction(action)
}
