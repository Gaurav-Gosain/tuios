package config

import (
	"fmt"
	"strings"
)

// scopedDescriptions are the descriptions of actions whose name means
// something only inside one scope. The rail's "exit", "kill" and "help", and
// the prefix sub-menus' numbered actions, have no entry in
// ActionDescriptions: a name such as "help" is not unique enough to describe
// there, and a name such as "new_window" means a different thing on the rail.
// Without these, a listing fell back to the action name with its underscores
// opened ("window prefix new").
//
// The rail's texts are the help overlay's (see generateSidebarBindings in
// internal/app, which reads them from here), so the two cannot drift.
var scopedDescriptions = map[string]map[string]string{
	ScopeSidebar: {
		"exit":          "Leave the rail, back to the panes",
		"cursor_down":   "Move the cursor down a row",
		"cursor_up":     "Move the cursor up a row",
		"first":         "Jump to the first row",
		"last":          "Jump to the last row",
		"collapse":      "Step up to the previous section",
		"expand":        "Step down to the next section",
		"activate":      "Activate the row: attach, or focus the pane",
		"reorder_down":  "Move the session or machine down the rail",
		"reorder_up":    "Move the session or machine up the rail",
		"section":       "Cycle the sessions, terminals and agents sections",
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
		"window_prefix_new":    "New window",
		"window_prefix_close":  "Close window",
		"window_prefix_rename": "Rename window",
		"window_prefix_next":   "Next window",
		"window_prefix_prev":   "Previous window",
		"window_prefix_tiling": "Toggle tiling mode",
		"window_prefix_cancel": "Cancel the window prefix",
	},
	ScopePrefixMinimize: {
		"minimize_prefix_focused":     "Minimize the focused window",
		"minimize_prefix_restore_all": "Restore all minimized windows",
		"minimize_prefix_cancel":      "Cancel the minimize prefix",
	},
	ScopePrefixWorkspce: {
		"workspace_prefix_cancel": "Cancel the workspace prefix",
	},
}

// numberedDescriptions describe the numbered action families by their
// prefix, per scope. The number is the action name's last character.
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
func ScopedDescription(scope, action string) string {
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
