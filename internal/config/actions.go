package config

import (
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Action is an action name as config.toml, tape and the protocol spell it.
type Action = string

// RunScope says what runs an action.
type RunScope uint8

const (
	// RunDispatch: the input dispatcher, from any key table that binds it.
	RunDispatch RunScope = iota
	// RunView: the view whose key table holds the action (the Inbox, the
	// review, the rail's lists, copy mode's exit), not the dispatcher.
	RunView
	// RunMotion: a copy-mode motion.
	RunMotion
)

// ActionSpec is one action: its name and the line that describes it. It is
// the one declaration of the action. ActionDescriptions, LookupAction,
// Actions and the handler tables (HandlerTable) are derived from it.
//
// It holds only constants and literals, so the compiler emits actionTable as
// data with no init code. A func or a computed value in it breaks that.
type ActionSpec struct {
	Name Action
	// Desc is one line of plain English, the help and keybinds list text.
	// In a numbered family it holds one %d, the number.
	Desc string
	// Count > 0 makes a numbered family: Name is a prefix, and the actions
	// are Name+"1" to Name+Count.
	Count uint8
	Run   RunScope
	// Hidden keeps the action out of the lists a person reads.
	Hidden bool
}

// actionTable is every action tuios knows. A new action is one entry here.
var actionTable = [...]ActionSpec{
	{Name: "file_search", Desc: "Search files below the folder the sidebar shows", Run: RunView},
	// The rail's files section. They act only while the cursor is on a row of
	// the listing, which is why three of them share a key with a rail binding
	// above and neither loses it.
	{Name: "file_create", Desc: "Files: new file, or a folder with a trailing /"},
	{Name: "file_rename", Desc: "Files: rename this file"},
	{Name: "file_delete", Desc: "Files: delete this file"},
	{Name: "file_delete_forever", Desc: "Files: delete for good, with no trash"},
	{Name: "file_copy", Desc: "Files: copy this file"},
	{Name: "file_cut", Desc: "Files: cut this file"},
	{Name: "file_paste", Desc: "Files: paste into this folder"},
	{Name: "file_open", Desc: "Files: open this folder, or copy this file's path"},
	{Name: "file_edit", Desc: "Files: edit this text file in a new pane"},
	{Name: "file_copy_path", Desc: "Files: copy the path of this file or folder"},
	{Name: "file_quick_look", Desc: "Files: show a preview of this file or folder"},
	{Name: "file_open_system", Desc: "Files: open this file or folder with the system opener"},
	{Name: "seek_down", Desc: "Preview: move forward in the file", Run: RunView},
	{Name: "seek_up", Desc: "Preview: move back in the file", Run: RunView},
	{Name: "half_down", Desc: "Preview: move half a page forward", Run: RunView},
	{Name: "half_up", Desc: "Preview: move half a page back", Run: RunView},
	{Name: "go_inside", Desc: "Preview: move the keyboard inside the file", Run: RunView},
	{Name: "go_back", Desc: "Preview: move the keyboard back to the list", Run: RunView},
	{Name: "full_size", Desc: "Preview: fill the area of the panes, or go back", Run: RunView},
	{Name: "line_down", Desc: "Preview: move one line forward", Run: RunView},
	{Name: "line_up", Desc: "Preview: move one line back", Run: RunView},
	{Name: "start", Desc: "Preview: go to the start of the file", Run: RunView},
	{Name: "find", Desc: "Preview: search in the file", Run: RunView},
	{Name: "find_next", Desc: "Preview: go to the next match", Run: RunView},
	{Name: "find_prev", Desc: "Preview: go to the match before", Run: RunView},
	{Name: "filmstrip", Desc: "Preview: show the filmstrip of a film, or hide it", Run: RunView},
	{Name: "copy_mode", Desc: "Preview: copy mode on the text, to select and copy it", Run: RunView},
	{Name: "next_view", Desc: "Preview: show the next view of the file", Run: RunView},
	{Name: "open_details", Desc: "Preview: open the closed sections of a Markdown file", Run: RunView},
	{Name: "column_left", Desc: "Preview: move a column left, or 1 second back in a film", Run: RunView},
	{Name: "column_right", Desc: "Preview: move a column right, or 1 second on in a film", Run: RunView},
	{Name: "go_to", Desc: "Preview: go to a line", Run: RunView},
	{Name: "wrap", Desc: "Preview: wrap long lines, or cut them", Run: RunView},
	{Name: "end", Desc: "Preview: go to the end of the file", Run: RunView},
	{Name: "file_send", Desc: "Files: send this file or folder to a host"},
	{Name: "file_insert_path", Desc: "Files: paste the path into the focused pane"},
	// The Copies panel: the copies between machines.
	{Name: "transfers_down", Desc: "Copies: next copy", Run: RunView},
	{Name: "transfers_up", Desc: "Copies: previous copy", Run: RunView},
	{Name: "transfers_pause", Desc: "Copies: pause or go on with the copy", Run: RunView},
	{Name: "transfers_cancel", Desc: "Copies: cancel the copy, after a question", Run: RunView},
	{Name: "transfers_retry", Desc: "Copies: try a failed copy again", Run: RunView},
	{Name: "transfers_clear", Desc: "Copies: remove the copies that ended", Run: RunView},
	{Name: "transfers_answer", Desc: "Copies: answer a copy that waits for an answer", Run: RunView},
	{Name: "transfers_close", Desc: "Copies: close the panel", Run: RunView},

	{Name: "list_up", Desc: "Lists: up one row", Run: RunView},
	{Name: "list_down", Desc: "Lists: down one row", Run: RunView},
	{Name: "list_page_up", Desc: "Lists: up a page", Run: RunView},
	{Name: "list_page_down", Desc: "Lists: down a page", Run: RunView},
	{Name: "list_first", Desc: "Lists: first row", Run: RunView},
	{Name: "list_last", Desc: "Lists: last row", Run: RunView},

	// The agents view, the prompt open over it, and the mailbox. Each acts
	// only while its overlay is up. The action names keep the inbox_ prefix
	// they had before the Inbox became the agents view, so configs keep
	// working.
	{Name: "inbox_down", Desc: "Agents view: next row", Run: RunView},
	{Name: "inbox_up", Desc: "Agents view: previous row", Run: RunView},
	{Name: "inbox_page_down", Desc: "Agents view: ten rows down", Run: RunView},
	{Name: "inbox_page_up", Desc: "Agents view: ten rows up", Run: RunView},
	{Name: "inbox_first", Desc: "Agents view: first row", Run: RunView},
	{Name: "inbox_last", Desc: "Agents view: last row", Run: RunView},
	{Name: "inbox_go", Desc: "Agents view: go to the agent's pane and close the view, or open the mail", Run: RunView},
	{Name: "inbox_peek", Desc: "Agents view: read the prompt here and answer it", Run: RunView},
	{Name: "inbox_dismiss", Desc: "Agents view: dismiss what needs you on the row", Run: RunView},
	{Name: "inbox_reply", Desc: "Agents view: reply to the agent or the mail, or reject the held approval", Run: RunView},
	{Name: "inbox_resume", Desc: "Agents view: resume the conversation in its pane, or every marked one", Run: RunView},
	{Name: "inbox_pass_on", Desc: "Agents view: pass held mail on to the agent it was for", Run: RunView},
	{Name: "inbox_filter", Desc: "Agents view: show only what needs you, then each kind, then every agent", Run: RunView},
	{Name: "inbox_select", Desc: "Agents view: search the agents, with selector terms such as harness:codex", Run: RunView},
	{Name: "inbox_mailbox", Desc: "Agents view: open the whole mailbox", Run: RunView},
	{Name: "inbox_close", Desc: "Agents view: close", Run: RunView},
	{Name: "inbox_escape", Desc: "Agents view: go back one step: out of the region, then the search, then the view", Run: RunView},
	{Name: "inbox_region_next", Desc: "Agents view: focus the next region: list, focus, reply, side column", Run: RunView},
	{Name: "inbox_region_prev", Desc: "Agents view: focus the region before", Run: RunView},
	{Name: "inbox_next_needs", Desc: "Agents view: go to the next agent that needs you", Run: RunView},
	{Name: "inbox_list_narrower", Desc: "Agents view: make the list narrower", Run: RunView},
	{Name: "inbox_list_wider", Desc: "Agents view: make the list wider", Run: RunView},
	{Name: "inbox_side", Desc: "Agents view: hide the side column, or show it again", Run: RunView},
	{Name: "inbox_zoom", Desc: "Agents view: show the focused region alone, or the whole view again", Run: RunView},
	{Name: "inbox_write", Desc: "Agents view: write a reply to the selected agent", Run: RunView},
	{Name: "inbox_sort", Desc: "Agents view: group by state, session, machine or project, or step the sort", Run: RunView},
	{Name: "inbox_view_pane", Desc: "Agents view: zoom the selected agent's pane, and come back with the same key", Run: RunView},
	{Name: "inbox_review", Desc: "Agents view: review the changes in the agent's pane", Run: RunView},
	{Name: "inbox_snooze", Desc: "Agents view: snooze what needs you on the row, then 1 to 4 for how long", Run: RunView},
	{Name: "inbox_undo", Desc: "Agents view: undo the last dismiss, snooze or settle", Run: RunView},
	{Name: "inbox_settle", Desc: "Agents view: settle the agent, or every marked one. Put away until it needs you", Run: RunView},
	{Name: "inbox_show_snoozed", Desc: "Agents view: show or hide the snoozed rows", Run: RunView},
	{Name: "inbox_deny_reason", Desc: "Agents view: reject the approval or plan and say why", Run: RunView},
	{Name: "inbox_new_agent", Desc: "Agents view: start a new agent", Run: RunView},
	{Name: "inbox_detail_down", Desc: "Agents view: scroll the card down", Run: RunView},
	{Name: "inbox_detail_up", Desc: "Agents view: scroll the card up", Run: RunView},
	{Name: "inbox_allow", Desc: "Agents view: allow the held approval once, or approve the plan", Run: RunView},
	{Name: "inbox_allow_always", Desc: "Agents view: always allow the held approval", Run: RunView},
	{Name: "inbox_open", Desc: "Agents view: open the focus on a narrow screen, or show every row of a long group", Run: RunView},
	{Name: "inbox_back", Desc: "Agents view: go back from the focus on a narrow screen", Run: RunView},
	{Name: "inbox_help", Desc: "Agents view: show every key", Run: RunView},
	{Name: "inbox_mark", Desc: "Agents view: mark the agent or Resume row for a bulk answer", Run: RunView},
	{Name: "inbox_mark_all", Desc: "Agents view: mark every agent shown, or clear the marks", Run: RunView},
	{Name: "inbox_tab_prev", Desc: "Agents view: the focus's tab to the left", Run: RunView},
	{Name: "inbox_tab_next", Desc: "Agents view: the focus's tab to the right", Run: RunView},
	{Name: "inbox_expand", Desc: "Agents view: show every line of every card in the thread, or fold them again", Run: RunView},
	{Name: "inbox_fold", Desc: "Agents view: fold or open the group of the row under the cursor", Run: RunView},
	{Name: "inbox_prompt_prev", Desc: "Agents view: show the thread's previous prompt at the top", Run: RunView},
	{Name: "inbox_prompt_next", Desc: "Agents view: show the thread's next prompt at the top", Run: RunView},
	{Name: "inbox_child", Desc: "Agents view: select the next agent a card in the thread started. Enter opens its thread", Run: RunView},
	{Name: "inbox_compare", Desc: "Agents view: compare the attempts of the fan under the cursor, ask a judge, keep one", Run: RunView},
	{Name: "inbox_board", Desc: "Agents view: show the task board, or the agents again", Run: RunView},
	{Name: "inbox_take", Desc: "Agents view: take the card for the focused pane", Run: RunView},
	{Name: "inbox_ship", Desc: "Agents view: Ready to merge: commit, push or open a pull request for the card in review", Run: RunView},
	{Name: "inbox_stage_prev", Desc: "Agents view: move the card to the stage before", Run: RunView},
	{Name: "inbox_stage_next", Desc: "Agents view: move the card to the stage after", Run: RunView},
	{Name: "inbox_board_layout", Desc: "Agents view: show the board as columns or as a list", Run: RunView},
	{Name: "inbox_dispatch_mode", Desc: "Agents view: on the board, turn task dispatch from manual to pull to auto", Run: RunView},
	{Name: "inbox_dispatch_pause", Desc: "Agents view: pause all task dispatch, or start it again", Run: RunView},
	{Name: "inbox_view_list", Desc: "Agents view: show the agents as a column or as a strip above the focus", Run: RunView},
	{Name: "inbox_artifact", Desc: "Agents view: open what the agent has shown, such as a TML form, and use it", Run: RunView},

	// The rail's agent rows. They act only while the cursor is on an agent
	// row, and a key they share with a rail binding keeps its rail meaning on
	// every other row.
	{Name: "agent_unread", Desc: "Agents: mark the pane's finished turn unread", Run: RunView},
	{Name: "agent_snooze", Desc: "Agents: snooze what needs you in the pane", Run: RunView},
	{Name: "agent_reply", Desc: "Agents: reply to the agent", Run: RunView},
	{Name: "agent_review", Desc: "Agents: review the pane's changes", Run: RunView},
	{Name: "agent_subagents", Desc: "Agents: open or close the subagent rows", Run: RunView},
	{Name: "agent_cancel_queued", Desc: "Agents: drop the newest queued message", Run: RunView},
	{Name: "peek_approve", Desc: "Prompt: approve", Run: RunView},
	{Name: "peek_approve_always", Desc: "Prompt: approve and do not ask again", Run: RunView},
	{Name: "peek_deny", Desc: "Prompt: reject", Run: RunView},
	{Name: "peek_type", Desc: "Prompt: type an answer", Run: RunView},
	{Name: "peek_read_again", Desc: "Prompt: read the prompt again", Run: RunView},
	{Name: "peek_go", Desc: "Prompt: go to the pane", Run: RunView},
	{Name: "peek_back", Desc: "Prompt: back to the agents view", Run: RunView},
	{Name: "mail_down", Desc: "Mailbox: next thread", Run: RunView},
	{Name: "mail_up", Desc: "Mailbox: previous thread", Run: RunView},
	{Name: "mail_page_down", Desc: "Mailbox: ten threads down", Run: RunView},
	{Name: "mail_page_up", Desc: "Mailbox: ten threads up", Run: RunView},
	{Name: "mail_open", Desc: "Mailbox: open the thread, or reply in an open one", Run: RunView},
	{Name: "mail_reply", Desc: "Mailbox: reply in the open thread", Run: RunView},
	{Name: "mail_focus_pane", Desc: "Mailbox: go to the pane that last wrote", Run: RunView},
	{Name: "mail_new", Desc: "Mailbox: write a new message to an agent", Run: RunView},
	{Name: "mail_back", Desc: "Mailbox: back, or close", Run: RunView},

	// The rail's reorder actions. They are here for the machine header's menu,
	// which resolves every row's label and key hint from this table.
	{Name: "reorder_up", Desc: "Rail: move this session or machine up"},
	{Name: "reorder_down", Desc: "Rail: move this session or machine down"},

	// Window Management
	{Name: "new_window", Desc: "New window"},
	{Name: "new_window_ssh", Desc: "New window that runs the focused pane's ssh"},
	{Name: "close_window", Desc: "Close window"},
	{Name: "rename_window", Desc: "Rename window"},
	{Name: "set_accent", Desc: "Accent color"},
	// Reached from the rail's session row and its menu; the row carries which
	// session, so it has no key of its own.
	{Name: "set_session_accent", Desc: "Session color"},
	{Name: "set_note", Desc: "Write a note for the session: what it is for or what comes next"},
	{Name: "minimize_window", Desc: "Minimize window"},
	{Name: "restore_all", Desc: "Restore all minimized"},
	{Name: "toggle_zoom", Desc: "Toggle zoom (fullscreen)"},
	{Name: "toggle_pip", Desc: "Pin or unpin the picture-in-picture view"},
	{Name: "focus_rail_pane", Desc: "Type into the pane in the rail"},
	{Name: "start_screensaver", Desc: "Start the screen saver now"},
	{Name: "screenshot", Desc: "Pick what to screenshot"},
	{Name: "screenshot_window", Desc: "Screenshot this window"},
	{Name: "screenshot_screen", Desc: "Screenshot the whole screen"},
	{Name: "next_window", Desc: "Next window"},
	{Name: "prev_window", Desc: "Previous window"},
	{Name: "last_pane", Desc: "Back to the previous pane"},
	{Name: "select_window_", Desc: "Select window %d", Count: 9},

	// Multifocus. Neither action has a default key.
	{Name: "toggle_multifocus_active", Desc: "Add or remove the focused pane from multifocus"},
	{Name: "toggle_multifocus_all", Desc: "Add all panes on the workspace to multifocus, or clear it"},
	{Name: "close_workspace", Desc: "Close every pane on a workspace, after a confirmation"},

	// Workspaces
	{Name: "next_workspace", Desc: "Switch to the next workspace that has panes"},
	{Name: "prev_workspace", Desc: "Switch to the previous workspace that has panes"},
	{Name: "switch_workspace_", Desc: "Switch to workspace %d", Count: 9},
	{Name: "move_and_follow_", Desc: "Move to workspace %d and follow", Count: 9},
	// Reached by the chord and by a dock pill's menu, so both name it the same.
	{Name: "workspace_prefix_rename", Desc: "Rename workspace"},
	{Name: "workspace_pill_switch", Desc: "Switch to workspace"},

	// Layout
	{Name: "snap_left", Desc: "Snap or focus left"},
	{Name: "snap_right", Desc: "Snap or focus right"},
	{Name: "focus_up", Desc: "Focus the window above"},
	{Name: "focus_down", Desc: "Focus the window below"},
	{Name: "snap_fullscreen", Desc: "Fullscreen"},
	{Name: "unsnap", Desc: "Unsnap"},
	{Name: "snap_corner_1", Desc: "Snap to top-left"},
	{Name: "snap_corner_2", Desc: "Snap to top-right"},
	{Name: "snap_corner_3", Desc: "Snap to bottom-left"},
	{Name: "snap_corner_4", Desc: "Snap to bottom-right"},
	{Name: "toggle_tiling", Desc: "Toggle tiling mode"},
	{Name: "swap_left", Desc: "Swap left"},
	{Name: "swap_right", Desc: "Swap right"},
	{Name: "swap_up", Desc: "Swap up"},
	{Name: "swap_down", Desc: "Swap down"},
	{Name: "resize_master_shrink", Desc: "Decrease pane width"},
	{Name: "resize_master_grow", Desc: "Increase pane width"},
	{Name: "resize_height_shrink", Desc: "Decrease focused window height"},
	{Name: "resize_height_grow", Desc: "Increase focused window height"},
	{Name: "resize_master_shrink_left", Desc: "Decrease pane width from left edge"},
	{Name: "resize_master_grow_left", Desc: "Increase pane width from left edge"},
	{Name: "resize_height_shrink_top", Desc: "Decrease focused window height from top edge"},
	{Name: "resize_height_grow_top", Desc: "Increase focused window height from top edge"},
	{Name: "resize_width_10", Desc: "Set focused window width to 10%"},
	{Name: "resize_width_20", Desc: "Set focused window width to 20%"},
	{Name: "resize_width_30", Desc: "Set focused window width to 30%"},
	{Name: "resize_width_40", Desc: "Set focused window width to 40%"},
	{Name: "resize_width_50", Desc: "Set focused window width to 50%"},
	{Name: "resize_width_60", Desc: "Set focused window width to 60%"},
	{Name: "resize_width_70", Desc: "Set focused window width to 70%"},
	{Name: "resize_width_80", Desc: "Set focused window width to 80%"},
	{Name: "resize_width_90", Desc: "Set focused window width to 90%"},
	{Name: "resize_height_10", Desc: "Set focused window height to 10%"},
	{Name: "resize_height_20", Desc: "Set focused window height to 20%"},
	{Name: "resize_height_30", Desc: "Set focused window height to 30%"},
	{Name: "resize_height_40", Desc: "Set focused window height to 40%"},
	{Name: "resize_height_50", Desc: "Set focused window height to 50%"},
	{Name: "resize_height_60", Desc: "Set focused window height to 60%"},
	{Name: "resize_height_70", Desc: "Set focused window height to 70%"},
	{Name: "resize_height_80", Desc: "Set focused window height to 80%"},
	{Name: "resize_height_90", Desc: "Set focused window height to 90%"},

	// BSP Tiling
	{Name: "split_horizontal", Desc: "Split window horizontally (top/bottom)"},
	{Name: "split_vertical", Desc: "Split window vertically (left/right)"},
	{Name: "split_ssh_horizontal", Desc: "Split top/bottom, and run the focused pane's ssh in the new pane"},
	{Name: "split_ssh_vertical", Desc: "Split left/right, and run the focused pane's ssh in the new pane"},
	{Name: "rotate_split", Desc: "Rotate split"},
	{Name: "equalize_splits", Desc: "Equalize all split ratios"},
	{Name: "preselect_left", Desc: "Preselect left for next window"},
	{Name: "preselect_right", Desc: "Preselect right for next window"},
	{Name: "preselect_up", Desc: "Preselect up for next window"},
	{Name: "preselect_down", Desc: "Preselect down for next window"},
	{Name: "cycle_tiling_scheme", Desc: "Cycle the workspace's tiling scheme: spiral, longest side, alternate, smart split"},
	{Name: "set_tiling_scheme_spiral", Desc: "Set the workspace's tiling scheme to spiral"},
	{Name: "set_tiling_scheme_longest_side", Desc: "Set the workspace's tiling scheme to longest side"},
	{Name: "set_tiling_scheme_alternate", Desc: "Set the workspace's tiling scheme to alternate"},
	{Name: "set_tiling_scheme_smart_split", Desc: "Set the workspace's tiling scheme to smart split"},

	// Master-stack
	{Name: "cycle_master_position", Desc: "Move the master panes to the next side: left, right, top, bottom, center"},
	{Name: "set_master_position_left", Desc: "Put the master panes on the left"},
	{Name: "set_master_position_right", Desc: "Put the master panes on the right"},
	{Name: "set_master_position_top", Desc: "Put the master panes at the top"},
	{Name: "set_master_position_bottom", Desc: "Put the master panes at the bottom"},
	{Name: "set_master_position_center", Desc: "Put the master panes in the center, with the stack on both sides"},
	{Name: "add_master", Desc: "Make one more pane a master pane"},
	{Name: "remove_master", Desc: "Make one pane fewer a master pane"},
	{Name: "swap_with_master", Desc: "Swap the focused pane with the master pane"},
	{Name: "focus_master", Desc: "Focus the master pane"},

	// Mode Control
	{Name: "enter_terminal_mode", Desc: "Enter terminal mode"},
	{Name: "enter_window_mode", Desc: "Enter window management mode"},
	{Name: "hold_window_mode", Desc: "Hold for window management mode", Run: RunView},
	{Name: "toggle_help", Desc: "Toggle help"},
	{Name: "open_settings", Desc: "Open settings"},
	{Name: "quit", Desc: "Quit"},
	{Name: "focus_sidebar", Desc: "Focus sidebar"},
	{Name: "next_session", Desc: "Next session"},
	{Name: "prev_session", Desc: "Previous session"},
	{Name: "next_host", Desc: "Go to the next machine"},
	{Name: "prev_host", Desc: "Go to the previous machine"},
	{Name: "next_agent", Desc: "Next agent pane, in the rail's order"},
	{Name: "prev_agent", Desc: "Previous agent pane, in the rail's order"},
	{Name: "switch_session_", Desc: "Switch to session %d", Count: 9},

	// Clipboard
	{Name: "copy_selection", Desc: "Copy selection to clipboard"},
	{Name: "paste_clipboard", Desc: "Paste from clipboard"},
	{Name: "paste_image", Desc: "Paste the clipboard image as a file path"},
	{Name: "paste_buffer", Desc: "Paste the newest paste buffer"},
	{Name: "choose_buffer", Desc: "Choose a paste buffer to paste"},
	{Name: "clear_selection", Desc: "Clear the text selection"},
	{Name: "hints", Desc: "Label the text on the pane to copy it"},
	{Name: "display_panes", Desc: "Label the panes and focus one by its label"},
	{Name: "pick_path", Desc: "Pick a file or folder and type its path at the cursor"},
	{Name: "toggle_scratch", Desc: "Show or hide the scratch terminal"},
	{Name: "toggle_calm", Desc: "Calm mode"},
	{Name: "hints_all_panes", Desc: "Label the text on all panes to copy it"},

	// The scrollback in the editor (prefix E, ctrl+g in copy mode)
	{Name: ActionEditScrollback, Desc: "Open the scrollback in the editor"},
	// No default key (cmd+k in other terminals)
	{Name: ActionClearScrollback, Desc: "Clear the scrollback and the screen above the prompt"},

	// Copy mode search (no default keybinding)
	{Name: ActionCopyModeSearchForward, Desc: "Enter copy mode and search forward"},
	{Name: ActionCopyModeSearchBackward, Desc: "Enter copy mode and search backward"},
	{Name: ActionCopyModeLeft, Desc: "Copy mode: move left", Run: RunMotion},
	{Name: ActionCopyModeRight, Desc: "Copy mode: move right", Run: RunMotion},
	{Name: ActionCopyModeUp, Desc: "Copy mode: move up", Run: RunMotion},
	{Name: ActionCopyModeDown, Desc: "Copy mode: move down", Run: RunMotion},
	{Name: ActionCopyModeLineStart, Desc: "Copy mode: go to the start of the line", Run: RunMotion},
	{Name: ActionCopyModeFirstNonBlank, Desc: "Copy mode: go to the first nonblank character of the line", Run: RunMotion},
	{Name: ActionCopyModeLineEnd, Desc: "Copy mode: go to the last nonblank character of the line", Run: RunMotion},
	{Name: ActionCopyModeExit, Desc: "Copy mode: leave copy mode; from a selection, copy it first", Run: RunView},
	{Name: ActionCopyModeWordForward, Desc: "Copy mode: move forward a word", Run: RunMotion},
	{Name: ActionCopyModeWordBackward, Desc: "Copy mode: move backward a word", Run: RunMotion},
	{Name: ActionCopyModeWordEnd, Desc: "Copy mode: move to the end of the word", Run: RunMotion},
	{Name: ActionCopyModeBigWordForward, Desc: "Copy mode: move forward a word, words split only at spaces", Run: RunMotion},
	{Name: ActionCopyModeBigWordBackward, Desc: "Copy mode: move backward a word, words split only at spaces", Run: RunMotion},
	{Name: ActionCopyModeBigWordEnd, Desc: "Copy mode: move to the end of the word, words split only at spaces", Run: RunMotion},
	{Name: ActionCopyModeHalfPageUp, Desc: "Copy mode: move up half a page", Run: RunMotion},
	{Name: ActionCopyModeHalfPageDown, Desc: "Copy mode: move down half a page", Run: RunMotion},
	{Name: ActionCopyModePageUp, Desc: "Copy mode: move up a page", Run: RunMotion},
	{Name: ActionCopyModePageDown, Desc: "Copy mode: move down a page", Run: RunMotion},
	{Name: ActionCopyModeBottom, Desc: "Copy mode: go to the last line, or to line N after a count", Run: RunMotion},
	{Name: ActionCopyModeScreenTop, Desc: "Copy mode: go to the top row of the view", Run: RunMotion},
	{Name: ActionCopyModeScreenMiddle, Desc: "Copy mode: go to the middle row of the view", Run: RunMotion},
	{Name: ActionCopyModeScreenBottom, Desc: "Copy mode: go to the last row of the view", Run: RunMotion},
	{Name: ActionCopyModeParagraphUp, Desc: "Copy mode: go to the blank line above the paragraph", Run: RunMotion},
	{Name: ActionCopyModeParagraphDown, Desc: "Copy mode: go to the blank line below the paragraph", Run: RunMotion},
	{Name: ActionCopyModeMatchingBracket, Desc: "Copy mode: go to the matching bracket", Run: RunMotion},
	{Name: ActionCopyModeRepeatCharSearch, Desc: "Copy mode: repeat the last f, F, t or T", Run: RunMotion},
	{Name: ActionCopyModeRepeatCharSearchBack, Desc: "Copy mode: repeat the last f, F, t or T the other way", Run: RunMotion},
	{Name: ActionReviewNextHunk, Desc: "Review: next hunk, going on into the next file", Run: RunView},
	{Name: ActionReviewPrevHunk, Desc: "Review: previous hunk, going back into the previous file", Run: RunView},
	{Name: ActionReviewNextFile, Desc: "Review: next file", Run: RunView},
	{Name: ActionReviewPrevFile, Desc: "Review: previous file", Run: RunView},
	{Name: ActionReviewStageHunk, Desc: "Review: stage the hunk under the cursor", Run: RunView},
	{Name: ActionReviewUnstageHunk, Desc: "Review: unstage the hunk under the cursor", Run: RunView},
	{Name: ActionReviewStageFile, Desc: "Review: stage the whole file", Run: RunView},
	{Name: ActionReviewUnstageFile, Desc: "Review: unstage the whole file", Run: RunView},
	{Name: ActionReviewUndoStage, Desc: "Review: undo the last stage or unstage", Run: RunView},

	// Session lifecycle (context menu rows; no default keybinding)
	{Name: "settings_sidebar", Desc: "Sidebar settings"},
	{Name: "rename_session", Desc: "Rename the session the menu was opened on"},
	{Name: "rename_workspace", Desc: "Rename the workspace the session is showing"},
	{Name: "kill_session", Desc: "Kill the session the menu was opened on"},
	{Name: "kill_session_next", Desc: "Kill session, go to next"},
	{Name: "kill_session_quit", Desc: "Kill session and quit"},
	// The palette, the settings, the rail's menu for this machine and the
	// version notice; no default key.
	{Name: "restart_daemon", Desc: "Restart the daemon with the newest tuios installed"},

	// System
	{Name: "toggle_logs", Desc: "Toggle log viewer"},
	{Name: "toggle_cache_stats", Desc: "Toggle cache statistics"},
	{Name: "toggle_spotlight", Desc: "Toggle spotlight"},

	// Prefix Mode
	{Name: "prefix_new_window", Desc: "Create new window"},
	{Name: "prefix_close_window", Desc: "Close current window"},
	{Name: "prefix_rename_window", Desc: "Rename window"},
	{Name: "prefix_settings", Desc: "Open settings"},
	{Name: "prefix_keybinds", Desc: "Open the keybind manager"},
	{Name: "prefix_next_window", Desc: "Next window"},
	{Name: "prefix_prev_window", Desc: "Previous window"},
	{Name: "prefix_select_0", Desc: "Jump to window 0"},
	{Name: "prefix_select_1", Desc: "Jump to window 1"},
	{Name: "prefix_select_2", Desc: "Jump to window 2"},
	{Name: "prefix_select_3", Desc: "Jump to window 3"},
	{Name: "prefix_select_4", Desc: "Jump to window 4"},
	{Name: "prefix_select_5", Desc: "Jump to window 5"},
	{Name: "prefix_select_6", Desc: "Jump to window 6"},
	{Name: "prefix_select_7", Desc: "Jump to window 7"},
	{Name: "prefix_select_8", Desc: "Jump to window 8"},
	{Name: "prefix_select_9", Desc: "Jump to window 9"},
	{Name: "prefix_toggle_tiling", Desc: "Toggle tiling mode"},
	{Name: "prefix_workspace", Desc: "Enter workspace prefix"},
	{Name: "prefix_minimize", Desc: "Enter minimize prefix"},
	{Name: "prefix_window", Desc: "Enter window prefix"},
	{Name: "prefix_detach", Desc: "Detach, leave running"},
	{Name: "prefix_close_session", Desc: "Close session and panes"},
	{Name: "prefix_exit_mode", Desc: "Leave terminal mode"},
	{Name: "prefix_selection", Desc: "Enter copy/scrollback mode"},
	{Name: "prefix_help", Desc: "Toggle help"},
	{Name: "prefix_debug", Desc: "Enter debug prefix"},
	{Name: "prefix_tape", Desc: "Enter tape manager prefix"},
	{Name: "prefix_quit", Desc: "Quit (daemon: kills session)"},
	{Name: "prefix_fullscreen", Desc: "Fullscreen current window"},
	{Name: "prefix_split_horizontal", Desc: "Split window horizontally"},
	{Name: "prefix_split_vertical", Desc: "Split window vertically"},
	{Name: "mark_pane", Desc: "Mark the focused pane, or clear the mark"},
	{Name: "join_marked_left", Desc: "Join the marked pane left of this one"},
	{Name: "join_marked_right", Desc: "Join the marked pane right of this one"},
	{Name: "join_marked_up", Desc: "Join the marked pane above this one"},
	{Name: "join_marked_down", Desc: "Join the marked pane below this one"},
	{Name: "resize_pane_left_5", Desc: "Narrow the focused pane by 5"},
	{Name: "resize_pane_right_5", Desc: "Widen the focused pane by 5"},
	{Name: "resize_pane_up_5", Desc: "Shorten the focused pane by 5"},
	{Name: "resize_pane_down_5", Desc: "Heighten the focused pane by 5"},
	{Name: "resize_pane_left_20", Desc: "Narrow the focused pane by 20"},
	{Name: "resize_pane_right_20", Desc: "Widen the focused pane by 20"},
	{Name: "resize_pane_up_20", Desc: "Shorten the focused pane by 20"},
	{Name: "resize_pane_down_20", Desc: "Heighten the focused pane by 20"},
	{Name: "new_workspace", Desc: "Go to the first empty workspace"},
	{Name: "last_workspace", Desc: "Go back to the last workspace"},
	{Name: "break_pane", Desc: "Move the focused pane to the first empty workspace, and follow it"},
	{Name: "last_session", Desc: "Go back to the last session"},
	{Name: "send_prefix", Desc: "Send the leader key to the focused pane"},
	{Name: "nest", Desc: "Send every key to the pane, the leader too, until nest_exit_key"},
	{Name: "move_workspace_left", Desc: "Swap this workspace with the one before it"},
	{Name: "move_workspace_right", Desc: "Swap this workspace with the one after it"},
	{Name: "send_to_workspace_", Desc: "Send the focused pane to workspace %d", Count: 9},
	{Name: "reload_config", Desc: "Reload the config file"},
	{Name: "toggle_mouse", Desc: "Turn mouse capture off or on (off: the terminal selects text)"},
	{Name: "cycle_layout", Desc: "Cycle the layout: BSP, master-stack, scrolling"},
	{Name: "prefix_rotate_split", Desc: "Rotate split"},
	{Name: "prefix_equalize_splits", Desc: "Equalize all splits"},
	{Name: "prefix_scrollback", Desc: "Open the scrollback browser"},
	{Name: "prefix_screenshot", Desc: "Take a screenshot"},
	{Name: "prefix_command_palette", Desc: "Open the command palette"},
	{Name: "prefix_file_search", Desc: "Search files below the focused pane's directory"},
	{Name: "prefix_toggle_sidebar", Desc: "Toggle the session sidebar"},
	{Name: "prefix_toggle_other_rail", Desc: "Toggle the opposite sidebar"},
	{Name: "prefix_toggle_spotlight", Desc: "Toggle spotlight"},
	{Name: "prefix_explore", Desc: "Focus/leave sidebar"},
	{Name: "prefix_jump_notif", Desc: "Jump to newest message"},
	{Name: "prefix_last_message", Desc: "Show the last message in full"},
	{Name: "prefix_mail", Desc: "Open the agents view on the mail for you"},
	{Name: "prefix_inbox", Desc: "Open the agents view"},
	{Name: "prefix_transfers", Desc: "Show the copies between machines"},
	{Name: "prefix_next_attention", Desc: "Jump to the oldest item needing you"},
	{Name: "prefix_review", Desc: "Review the focused pane's changes"},
	{Name: "prefix_next_finished", Desc: "Jump to the newest unseen finished turn"},
	{Name: "prefix_agents_settings", Desc: "Open the Agents settings"},
	{Name: "prefix_session_switcher", Desc: "Open the session switcher"},
	{Name: "prefix_new_task", Desc: "Write a new task card"},
	{Name: "prefix_workspace_switcher", Desc: "Open the workspace switcher"},
	{Name: "choose_tree", Desc: "Find a pane in every session and go to it"},
	{Name: "goto_picker", Desc: "Go to an agent, session, workspace or pane"},
	{Name: "new_worktree", Desc: "Make a worktree session in the focused pane's repository"},
	{Name: "prefix_layout", Desc: "Enter layout prefix"},

	// Tape Prefix
	{Name: "tape_prefix_manager", Desc: "Open tape manager"},
	{Name: "tape_prefix_review", Desc: "Review project tape"},
	{Name: "tape_prefix_record", Desc: "Start recording"},
	{Name: "tape_prefix_stop", Desc: "Stop recording"},
	{Name: "tape_prefix_cancel", Desc: "Cancel tape prefix"},

	// Tape Actions
	{Name: "toggle_tape_manager", Desc: "Toggle tape manager"},
	{Name: "stop_recording", Desc: "Stop tape recording"},

	// Debug Prefix
	{Name: "debug_prefix_logs", Desc: "Toggle log viewer"},
	{Name: "debug_prefix_cache", Desc: "Toggle cache statistics"},
	{Name: "debug_prefix_animations", Desc: "Toggle animations"},
	{Name: "debug_prefix_showkeys", Desc: "Toggle showkeys overlay"},
	{Name: "debug_prefix_cancel", Desc: "Cancel debug prefix"},

	// Terminal Mode (direct keybinds, no prefix required)
	{Name: "terminal_next_window", Desc: "Next window (terminal mode)"},
	{Name: "terminal_prev_window", Desc: "Previous window (terminal mode)"},
	{Name: "terminal_exit_mode", Desc: "Exit terminal mode (to window mode)"},
	{Name: "terminal_focus_left", Desc: "Focus the pane to the left"},
	{Name: "terminal_focus_right", Desc: "Focus the pane to the right"},
	{Name: "terminal_focus_up", Desc: "Focus the pane above"},
	{Name: "terminal_focus_down", Desc: "Focus the pane below"},
	{Name: "terminal_scroll_up", Desc: "Scroll into the pane's scrollback", Run: RunView},
	{Name: "terminal_scroll_down", Desc: "Scroll back down toward live output", Run: RunView},
	{Name: "terminal_scroll_page_up", Desc: "Scroll up one page", Run: RunView},
	{Name: "terminal_scroll_page_down", Desc: "Scroll down one page", Run: RunView},
	{Name: "terminal_paste_host", Desc: "Paste from the host clipboard", Run: RunView},

	// Global (window mode and terminal mode alike)
	{Name: "command_palette", Desc: "Open the command palette"},
	{Name: "launcher", Desc: "Open the app launcher"},
	{Name: "new_session", Desc: "Make a session, asking which machine"},

	// Script playback
	{Name: "script_pause", Desc: "Pause or resume the playing tape", Run: RunView},

	// Layout Prefix
	{Name: "layout_prefix_load", Desc: "Open the layout picker"},
	{Name: "layout_prefix_save", Desc: "Save the current layout"},
	{Name: "layout_prefix_cancel", Desc: "Cancel layout prefix"},
	{Name: "window_prefix_new", Desc: "Create a window"},
	{Name: "window_prefix_close", Desc: "Close the window"},
	{Name: "window_prefix_rename", Desc: "Rename the window"},
	{Name: "window_prefix_next", Desc: "Focus the next window"},
	{Name: "window_prefix_prev", Desc: "Focus the previous window"},
	{Name: "window_prefix_tiling", Desc: "Turn tiling on or off"},
	{Name: "window_prefix_float", Desc: "Float the pane or tile it. A TML popup becomes a pane, and a TML pane a popup"},
	{Name: "prefix_tuios", Desc: "Open the tuios menu: the tuios keys a preset moved"},
	{Name: "open_keybind_presets", Desc: "Choose the keys: tuios, tmux, screen or zellij"},
	// Command palette rows that had their own code and no action. As actions
	// they can be bound to a key and named in a tape. None has a default key.
	{Name: "new_window_on_host", Desc: "Open a new window on another machine"},
	{Name: "toggle_shared_borders", Desc: "Toggle shared borders between neighbouring panes"},
	{Name: "next_saved_layout", Desc: "Load the next saved layout"},
	{Name: "prev_saved_layout", Desc: "Load the previous saved layout"},
	{Name: "clear_multifocus", Desc: "Take every pane out of multifocus"},
	{Name: "layout_bsp", Desc: "Tile the workspace as a BSP tree"},
	{Name: "layout_master_stack", Desc: "Tile the workspace as master and stack"},
	{Name: "layout_scrolling", Desc: "Tile the workspace as scrolling columns"},
	{Name: "layout_off", Desc: "Turn tiling off on the workspace"},
	{Name: "all_windows", Desc: "Show every window of every workspace"},
	{Name: "theme_picker", Desc: "Choose a theme"},
	{Name: "look_picker", Desc: "Choose a look: a theme with its borders and dock"},
	{Name: "share_look", Desc: "Share my look"},
	{Name: "edit_theme", Desc: "Edit the current theme"},
	{Name: "toggle_focus_follows_mouse", Desc: "Toggle focus follows mouse"},
	{Name: "add_machine", Desc: "Add a machine"},
	{Name: "open_mailbox", Desc: "Open the agents' mailbox"},
	{Name: "dispatch_pause", Desc: "Pause all task dispatch"},
	{Name: "dispatch_resume", Desc: "Start task dispatch again"},
	{Name: "multi_copy_mode", Desc: "Copy mode in every pane of the multifocus set"},

	// The names the dispatcher runs that have no description of their own:
	// the prefix sub-menus' copies of window and workspace actions, and the
	// scrolling layout's keys. A key bound to one in config.toml runs it,
	// and a tape may name it, but no list shows it.
	{Name: "minimize_prefix_cancel", Hidden: true},
	{Name: "minimize_prefix_focus_only", Hidden: true},
	{Name: "minimize_prefix_focused", Hidden: true},
	{Name: "minimize_prefix_restore_", Count: 9, Hidden: true},
	{Name: "minimize_prefix_restore_all", Hidden: true},
	{Name: "repo_new_worktree", Hidden: true},
	{Name: "repo_toggle_fold", Hidden: true},
	{Name: "restore_minimized_", Count: 9, Hidden: true},
	{Name: "scroll_consume", Hidden: true},
	{Name: "scroll_cycle_width", Hidden: true},
	{Name: "scroll_expel", Hidden: true},
	{Name: "scroll_focus_left", Hidden: true},
	{Name: "scroll_focus_right", Hidden: true},
	{Name: "scroll_maximize", Hidden: true},
	{Name: "scroll_move_left", Hidden: true},
	{Name: "scroll_move_right", Hidden: true},
	{Name: "session_new_worktree", Hidden: true},
	{Name: "smart_split", Hidden: true},
	{Name: "window_prefix_cancel", Hidden: true},
	{Name: "workspace_prefix_cancel", Hidden: true},
	{Name: "workspace_prefix_move_", Count: 9, Hidden: true},
	{Name: "workspace_prefix_switch_", Count: 9, Hidden: true},
}

// Actions returns the table, with each numbered family as one entry.
func Actions() []ActionSpec { return actionTable[:] }

// LookupAction returns the action a name names: an entry of the table, or
// one member of a numbered family, with its number in Desc.
func LookupAction(name string) (ActionSpec, bool) {
	spec, ok := actionIndex()[name]
	return spec, ok
}

// actionIndex maps every action name, family members included, to its spec.
var actionIndex = sync.OnceValue(func() map[string]ActionSpec {
	idx := make(map[string]ActionSpec, len(actionTable)+40)
	for _, spec := range actionTable {
		if spec.Count == 0 {
			idx[spec.Name] = spec
			continue
		}
		for n := 1; n <= int(spec.Count); n++ {
			member := spec
			member.Name = spec.Name + strconv.Itoa(n)
			member.Desc = strings.Replace(spec.Desc, "%d", strconv.Itoa(n), 1)
			member.Count = 0
			idx[member.Name] = member
		}
	}
	return idx
})

// actionDescriptions is ActionDescriptions: every name and its line, for the
// actions that are not Hidden.
func actionDescriptions() map[string]string {
	idx := actionIndex()
	out := make(map[string]string, len(idx))
	for name, spec := range idx {
		if !spec.Hidden {
			out[name] = spec.Desc
		}
	}
	return out
}

// HandlerTable binds a function to each action of one RunScope. Bind panics
// on a name that is not in the action table or not of the table's scope, so
// a handler cannot be bound under a name nothing else knows. Missing lists
// the actions of the scope that have no function.
type HandlerTable[H any] struct {
	scope RunScope
	fns   map[Action]H
}

// NewHandlerTable returns an empty table for the actions of scope.
func NewHandlerTable[H any](scope RunScope) *HandlerTable[H] {
	return &HandlerTable[H]{scope: scope, fns: make(map[Action]H)}
}

// Bind binds fn to the action a, a table entry or one member of a numbered
// family.
func (t *HandlerTable[H]) Bind(a Action, fn H) {
	spec, ok := LookupAction(a)
	if !ok {
		panic("config: action " + a + " is bound to a handler but is not in the action table")
	}
	if spec.Run != t.scope {
		panic("config: action " + a + " is bound in a handler table of another scope")
	}
	t.fns[a] = fn
}

// Lookup returns the function bound to name.
func (t *HandlerTable[H]) Lookup(name string) (H, bool) {
	fn, ok := t.fns[name]
	return fn, ok
}

// Missing lists, sorted, the actions of the table's scope with no function.
func (t *HandlerTable[H]) Missing() []Action {
	var missing []Action
	for name, spec := range actionIndex() {
		if spec.Run != t.scope {
			continue
		}
		if _, ok := t.fns[name]; !ok {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	return missing
}
