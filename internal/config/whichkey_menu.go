package config

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

// The which-key menus are built from the keybind registry, the source the
// help overlay and `tuios keybinds list` read. They used to be hand-written
// key lists, so a rebound key went on showing its default here while the help
// and the keymap said otherwise.
//
// What stays hand-written is the shape: which actions a menu shows, in which
// section and order, and what each row says. The keys come from the config.

// MenuState is what a which-key menu's rows depend on besides the keymap.
type MenuState struct {
	// Daemon swaps the leader menu's detach and quit rows: in a daemon
	// session d detaches and q opens the quit menu.
	Daemon bool
	// MultiCopy is the number of panes multi copy mode would take, zero when
	// it is off. The copy-mode row says so when it is on.
	MultiCopy int
	// Spotlight is set while the spotlight is on, and the sidebar row then
	// says the key turns it off.
	Spotlight bool
	// Minimized is the number of minimized windows the restore row counts,
	// or -1 to leave the count out.
	Minimized int
}

// menuPart is one action of a row, with what the row says about it when it
// has to stand alone.
type menuPart struct{ action, desc string }

// menuRow is one line of a menu. A row of one part shows every key of its
// action. A row of several parts shows the first key of each, under desc,
// while every part is bound, and falls apart into one row per bound part
// when one is not. A numbered row shows its parts as ranges such as 1-9.
type menuRow struct {
	parts    []menuPart
	desc     string
	numbered bool
	submenu  bool
}

func row(action, desc string) menuRow {
	return menuRow{parts: []menuPart{{action, desc}}, desc: desc}
}

func pair(desc string, parts ...menuPart) menuRow { return menuRow{parts: parts, desc: desc} }

func part(action, desc string) menuPart { return menuPart{action, desc} }

type menuSection struct {
	title string
	rows  []menuRow
}

// The menus are tables, not code that builds them: built per call, the
// composite literals were 24 KB of machine code, and a package-level literal
// of constants is data the linker lays out once. The rows that depend on
// MenuState are put in by menuSections, on a copy.

// workspaceMenuRows is the workspace menu (leader w). The rows after rename are
// bound by a preset whose tab mode opens this menu (zellij).
var workspaceMenuRows = []menuRow{
	{parts: []menuPart{{"workspace_prefix_switch_1", "Switch to workspace"}, {"workspace_prefix_switch_2", "Switch to workspace"}, {"workspace_prefix_switch_3", "Switch to workspace"}, {"workspace_prefix_switch_4", "Switch to workspace"}, {"workspace_prefix_switch_5", "Switch to workspace"}, {"workspace_prefix_switch_6", "Switch to workspace"}, {"workspace_prefix_switch_7", "Switch to workspace"}, {"workspace_prefix_switch_8", "Switch to workspace"}, {"workspace_prefix_switch_9", "Switch to workspace"}}, desc: "Switch to workspace", numbered: true},
	{parts: []menuPart{{"workspace_prefix_move_1", "Move window to workspace"}, {"workspace_prefix_move_2", "Move window to workspace"}, {"workspace_prefix_move_3", "Move window to workspace"}, {"workspace_prefix_move_4", "Move window to workspace"}, {"workspace_prefix_move_5", "Move window to workspace"}, {"workspace_prefix_move_6", "Move window to workspace"}, {"workspace_prefix_move_7", "Move window to workspace"}, {"workspace_prefix_move_8", "Move window to workspace"}, {"workspace_prefix_move_9", "Move window to workspace"}}, desc: "Move window to workspace", numbered: true},
	{parts: []menuPart{{"workspace_prefix_rename", "Rename workspace"}}, desc: "Rename workspace"},
	{parts: []menuPart{{"new_workspace", "New workspace"}}, desc: "New workspace"},
	{parts: []menuPart{{"close_workspace", "Close workspace"}}, desc: "Close workspace"},
	{parts: []menuPart{{"prev_workspace", "Previous workspace"}, {"next_workspace", "Next workspace"}}, desc: "Prev/next workspace"},
	{parts: []menuPart{{"last_workspace", "Last workspace"}}, desc: "Last workspace"},
	{parts: []menuPart{{"workspace_prefix_cancel", "Cancel"}}, desc: "Cancel"},
}

// minimizeMenuRows is the minimize menu (leader m). Row 1 says how many
// windows are minimized; see menuSections.
var minimizeMenuRows = []menuRow{
	{parts: []menuPart{{"minimize_prefix_focused", "Minimize focused window"}}, desc: "Minimize focused window"},
	{parts: []menuPart{{"minimize_prefix_restore_1", "Restore window"}, {"minimize_prefix_restore_2", "Restore window"}, {"minimize_prefix_restore_3", "Restore window"}, {"minimize_prefix_restore_4", "Restore window"}, {"minimize_prefix_restore_5", "Restore window"}, {"minimize_prefix_restore_6", "Restore window"}, {"minimize_prefix_restore_7", "Restore window"}, {"minimize_prefix_restore_8", "Restore window"}, {"minimize_prefix_restore_9", "Restore window"}}, desc: "Restore window", numbered: true},
	{parts: []menuPart{{"minimize_prefix_restore_all", "Restore all"}}, desc: "Restore all"},
	{parts: []menuPart{{"minimize_prefix_cancel", "Cancel"}}, desc: "Cancel"},
}

// windowMenuRows is the window menu (leader t). The rows after tiling are bound
// by a preset whose pane mode opens this menu (zellij).
var windowMenuRows = []menuRow{
	{parts: []menuPart{{"window_prefix_new", "New window"}}, desc: "New window"},
	{parts: []menuPart{{"window_prefix_close", "Close window"}}, desc: "Close window"},
	{parts: []menuPart{{"window_prefix_rename", "Rename window"}}, desc: "Rename window"},
	{parts: []menuPart{{"window_prefix_next", "Next window"}}, desc: "Next window"},
	{parts: []menuPart{{"window_prefix_prev", "Previous window"}}, desc: "Previous window"},
	{parts: []menuPart{{"window_prefix_tiling", "Toggle tiling mode"}}, desc: "Toggle tiling mode"},
	{parts: []menuPart{{"split_vertical", "Split right"}}, desc: "Split right"},
	{parts: []menuPart{{"split_horizontal", "Split down"}}, desc: "Split down"},
	{parts: []menuPart{{"prefix_fullscreen", "Toggle zoom"}}, desc: "Toggle zoom"},
	{parts: []menuPart{{"terminal_focus_left", "Focus pane left"}, {"terminal_focus_down", "Focus pane down"}, {"terminal_focus_up", "Focus pane up"}, {"terminal_focus_right", "Focus pane right"}}, desc: "Focus pane"},
	{parts: []menuPart{{"window_prefix_cancel", "Cancel"}}, desc: "Cancel"},
}

// debugMenuRows is the debug menu (leader D).
var debugMenuRows = []menuRow{
	{parts: []menuPart{{"debug_prefix_logs", "Toggle log viewer"}}, desc: "Toggle log viewer"},
	{parts: []menuPart{{"debug_prefix_cache", "Toggle cache statistics"}}, desc: "Toggle cache statistics"},
	{parts: []menuPart{{"debug_prefix_showkeys", "Toggle showkeys overlay"}}, desc: "Toggle showkeys overlay"},
	{parts: []menuPart{{"debug_prefix_animations", "Toggle animations"}}, desc: "Toggle animations"},
	{parts: []menuPart{{"debug_prefix_cancel", "Cancel"}}, desc: "Cancel"},
}

// tapeMenuRows is the tape menu (leader T).
var tapeMenuRows = []menuRow{
	{parts: []menuPart{{"tape_prefix_manager", "Open tape manager"}}, desc: "Open tape manager"},
	{parts: []menuPart{{"tape_prefix_review", "Review project tape"}}, desc: "Review project tape"},
	{parts: []menuPart{{"tape_prefix_record", "Start recording"}}, desc: "Start recording"},
	{parts: []menuPart{{"tape_prefix_stop", "Stop recording"}}, desc: "Stop recording"},
	{parts: []menuPart{{"tape_prefix_cancel", "Cancel"}}, desc: "Cancel"},
}

// layoutMenuRows is the layout menu (leader L).
var layoutMenuRows = []menuRow{
	{parts: []menuPart{{"layout_prefix_load", "Load layout"}}, desc: "Load layout"},
	{parts: []menuPart{{"layout_prefix_save", "Save layout"}}, desc: "Save layout"},
	{parts: []menuPart{{"snap_corner_1", "Snap window to a corner"}, {"snap_corner_2", "Snap window to a corner"}, {"snap_corner_3", "Snap window to a corner"}, {"snap_corner_4", "Snap window to a corner"}}, desc: "Snap window to a corner", numbered: true},
	{parts: []menuPart{{"resize_width_50", "Resize focused window width (%)"}, {"resize_width_60", "Resize focused window width (%)"}, {"resize_width_70", "Resize focused window width (%)"}, {"resize_width_80", "Resize focused window width (%)"}, {"resize_width_90", "Resize focused window width (%)"}}, desc: "Resize focused window width (%)", numbered: true},
	{parts: []menuPart{{"resize_height_50", "Resize focused window height (%)"}, {"resize_height_60", "Resize focused window height (%)"}, {"resize_height_70", "Resize focused window height (%)"}, {"resize_height_80", "Resize focused window height (%)"}, {"resize_height_90", "Resize focused window height (%)"}}, desc: "Resize focused window height (%)", numbered: true},
	{parts: []menuPart{{"cycle_master_position", "Move master to next side"}}, desc: "Move master to next side"},
	{parts: []menuPart{{"swap_with_master", "Swap with master"}}, desc: "Swap with master"},
	{parts: []menuPart{{"focus_master", "Focus master"}}, desc: "Focus master"},
	{parts: []menuPart{{"add_master", "Add master"}, {"remove_master", "Remove master"}}, desc: "Add/remove master"},
	{parts: []menuPart{{"layout_prefix_cancel", "Cancel"}}, desc: "Cancel"},
}

// leaderWorkspaceRows is the leader menu's Workspaces section. A preset
// binds these (tmux's c n p l 0-9 , & !). A row whose action has no key after
// the leader is left out, so the tuios menu does not show the section.
var leaderWorkspaceRows = []menuRow{
	{parts: []menuPart{{"new_workspace", "New workspace"}}, desc: "New workspace"},
	{parts: []menuPart{{"next_workspace", "Next workspace"}, {"prev_workspace", "Previous workspace"}}, desc: "Next/prev workspace"},
	{parts: []menuPart{{"last_workspace", "Last workspace"}}, desc: "Last workspace"},
	{parts: []menuPart{{"switch_workspace_1", "Go to workspace"}, {"switch_workspace_2", "Go to workspace"}, {"switch_workspace_3", "Go to workspace"}, {"switch_workspace_4", "Go to workspace"}, {"switch_workspace_5", "Go to workspace"}, {"switch_workspace_6", "Go to workspace"}, {"switch_workspace_7", "Go to workspace"}, {"switch_workspace_8", "Go to workspace"}, {"switch_workspace_9", "Go to workspace"}}, desc: "Go to workspace", numbered: true},
	{parts: []menuPart{{"rename_workspace", "Rename workspace"}}, desc: "Rename workspace"},
	{parts: []menuPart{{"close_workspace", "Close workspace"}}, desc: "Close workspace"},
	{parts: []menuPart{{"break_pane", "Pane to a new workspace"}}, desc: "Pane to a new workspace"},
}

// leaderWindowRows is the leader menu's Windows section.
var leaderWindowRows = []menuRow{
	{parts: []menuPart{{"prefix_new_window", "Create window"}}, desc: "Create window"},
	{parts: []menuPart{{"prefix_close_window", "Close window"}}, desc: "Close window"},
	{parts: []menuPart{{"prefix_rename_window", "Rename window"}}, desc: "Rename window"},
	{parts: []menuPart{{"prefix_next_window", "Next window"}, {"prefix_prev_window", "Previous window"}}, desc: "Next/prev window"},
	{parts: []menuPart{{"prefix_select_0", "Jump to window"}, {"prefix_select_1", "Jump to window"}, {"prefix_select_2", "Jump to window"}, {"prefix_select_3", "Jump to window"}, {"prefix_select_4", "Jump to window"}, {"prefix_select_5", "Jump to window"}, {"prefix_select_6", "Jump to window"}, {"prefix_select_7", "Jump to window"}, {"prefix_select_8", "Jump to window"}, {"prefix_select_9", "Jump to window"}}, desc: "Jump to window", numbered: true},
	{parts: []menuPart{{"prefix_fullscreen", "Toggle zoom"}}, desc: "Toggle zoom"},
	{parts: []menuPart{{"minimize_window", "Minimize window"}}, desc: "Minimize window"},
}

// leaderPaneRows is the leader menu's Panes section. The arrows walk panes,
// and the prefix stays armed for a moment so a run of them costs one prefix
// press (see the repeat window in internal/input/prefix_repeat.go). The pane
// labels share the hints row: one row more made the panel reach the pane's
// top border at 80x24.
var leaderPaneRows = []menuRow{
	{parts: []menuPart{{"terminal_focus_left", "Focus pane left"}, {"terminal_focus_up", "Focus pane up"}, {"terminal_focus_down", "Focus pane down"}, {"terminal_focus_right", "Focus pane right"}}, desc: "Focus pane"},
	{parts: []menuPart{{"prefix_toggle_tiling", "Toggle tiling"}}, desc: "Toggle tiling"},
	{parts: []menuPart{{"prefix_split_horizontal", "Split horizontal"}}, desc: "Split horizontal"},
	{parts: []menuPart{{"prefix_split_vertical", "Split vertical"}}, desc: "Split vertical"},
	{parts: []menuPart{{"prefix_rotate_split", "Rotate split"}}, desc: "Rotate split"},
	{parts: []menuPart{{"prefix_equalize_splits", "Equalize splits"}}, desc: "Equalize splits"},
	{parts: []menuPart{{"hints", "Hints"}, {"display_panes", "Pane labels"}}, desc: "Hints, labels"},
	{parts: []menuPart{{"last_pane", "Last pane"}}, desc: "Last pane"},
	{parts: []menuPart{{"mark_pane", "Mark pane"}}, desc: "Mark pane"},
	{parts: []menuPart{{"resize_pane_left_5", "Resize pane left"}, {"resize_pane_up_5", "Resize pane up"}, {"resize_pane_down_5", "Resize pane down"}, {"resize_pane_right_5", "Resize pane right"}}, desc: "Resize pane"},
	{parts: []menuPart{{"cycle_layout", "Next layout"}}, desc: "Next layout"},
}

// leaderSessionRows is the leader menu's Sessions section, before the detach
// and quit rows menuSections adds for the session kind.
var leaderSessionRows = []menuRow{
	{parts: []menuPart{{"prev_session", "Previous session"}, {"next_session", "Next session"}}, desc: "Prev/next session"},
	{parts: []menuPart{{"prefix_session_switcher", "Sessions"}}, desc: "Sessions"},
	{parts: []menuPart{{"prefix_workspace_switcher", "Workspaces"}}, desc: "Workspaces"},
	{parts: []menuPart{{"choose_tree", "Find a pane"}}, desc: "Find a pane"},
	{parts: []menuPart{{"goto_picker", "Go to anything"}}, desc: "Go to anything"},
	{parts: []menuPart{{"prefix_close_session", "Close session"}}, desc: "Close session"},
	{parts: []menuPart{{"toggle_scratch", "Scratch session"}}, desc: "Scratch session"},
	{parts: []menuPart{{"last_session", "Last session"}}, desc: "Last session"},
	{parts: []menuPart{{"rename_session", "Rename session"}}, desc: "Rename session"},
}

// leaderMenuRows is the leader menu's Menus section.
var leaderMenuRows = []menuRow{
	{parts: []menuPart{{"prefix_tuios", "tuios"}}, desc: "tuios", submenu: true},
	{parts: []menuPart{{"prefix_workspace", "Workspace"}}, desc: "Workspace", submenu: true},
	{parts: []menuPart{{"prefix_minimize", "Minimize"}}, desc: "Minimize", submenu: true},
	{parts: []menuPart{{"prefix_window", "Window"}}, desc: "Window", submenu: true},
	{parts: []menuPart{{"prefix_layout", "Layout"}}, desc: "Layout", submenu: true},
	{parts: []menuPart{{"prefix_tape", "Tape"}}, desc: "Tape", submenu: true},
	{parts: []menuPart{{"prefix_debug", "Debug"}}, desc: "Debug", submenu: true},
}

// leaderToolRows is the leader menu's Tools section.
var leaderToolRows = []menuRow{
	{parts: []menuPart{{"prefix_command_palette", "Command palette"}}, desc: "Command palette"},
	{parts: []menuPart{{"reload_config", "Reload config"}}, desc: "Reload config"},
	{parts: []menuPart{{"send_prefix", "Send the leader"}}, desc: "Send the leader"},
	{parts: []menuPart{{"launcher", "Launcher"}}, desc: "Launcher"},
	{parts: []menuPart{{"prefix_settings", "Settings"}}, desc: "Settings"},
	{parts: []menuPart{{"prefix_keybinds", "Keybindings"}}, desc: "Keybindings"},
	{parts: []menuPart{{"prefix_screenshot", "Screenshot"}}, desc: "Screenshot"},
	{parts: []menuPart{{"prefix_transfers", "Copies"}}, desc: "Copies"},
	{parts: []menuPart{{"prefix_jump_notif", "Newest message"}}, desc: "Newest message"},
	{parts: []menuPart{{"prefix_last_message", "Show last message"}}, desc: "Show last message"},
	{parts: []menuPart{{"prefix_help", "Help"}}, desc: "Help"},
}

// leaderAgentRows is the leader menu's Agents section.
var leaderAgentRows = []menuRow{
	{parts: []menuPart{{"prefix_inbox", "Agents view"}}, desc: "Agents view"},
	{parts: []menuPart{{"prefix_next_attention", "Oldest waiting"}}, desc: "Oldest waiting"},
	{parts: []menuPart{{"prefix_mail", "Mail for you"}}, desc: "Mail for you"},
	{parts: []menuPart{{"prefix_next_finished", "Newest finished"}}, desc: "Newest finished"},
	{parts: []menuPart{{"prefix_review", "Review changes"}}, desc: "Review changes"},
	{parts: []menuPart{{"prefix_agents_settings", "Agents settings"}}, desc: "Agents settings"},
}

// menuSections is the layout of one which-key menu. The sub-prefixes are one
// untitled section each: they are a handful of lines and a heading over them
// would say what the panel's title already does.
func menuSections(prefixType string, st MenuState) []menuSection {
	one := func(rows []menuRow) []menuSection { return []menuSection{{rows: rows}} }
	switch prefixType {
	case "workspace":
		return one(workspaceMenuRows)
	case "minimize":
		rows := minimizeMenuRows
		if st.Minimized >= 0 {
			// The restore row says how many windows are minimized.
			rows = slices.Clone(rows)
			r := rows[1]
			r.desc = fmt.Sprintf("Restore window (%d minimized)", st.Minimized)
			r.parts = slices.Clone(r.parts)
			for i := range r.parts {
				r.parts[i].desc = r.desc
			}
			rows[1] = r
		}
		return one(rows)
	case "window":
		return one(windowMenuRows)
	case "debug":
		return one(debugMenuRows)
	case "tape":
		return one(tapeMenuRows)
	case "layout":
		return one(layoutMenuRows)
	}

	// The leader's menu, in the sections the panel flows into columns. Each
	// section is a few lines, so a column holds a section or two whole and the
	// panel stays short enough to read without scrolling the eye down a list.
	sessions := menuSection{title: "Sessions", rows: slices.Clip(leaderSessionRows)}
	modes := menuSection{title: "Modes"}
	// In daemon mode d detaches and Esc leaves for window mode; in local mode
	// both leave for window mode.
	if st.Daemon {
		sessions.rows = append(sessions.rows, row("prefix_detach", "Detach session"), row("prefix_quit", "Quit menu"))
		modes.rows = append(modes.rows, row("prefix_exit_mode", "Window mode"))
	} else {
		sessions.rows = append(sessions.rows, row("prefix_quit", "Quit application"))
		modes.rows = append(modes.rows, pair("Window mode", part("prefix_detach", "Window mode"), part("prefix_exit_mode", "Window mode")))
	}
	// With multifocus on, the copy-mode key enters multi copy mode, and the
	// menu says so: this is where a multifocus user finds out it exists.
	copyRow := pair("Copy/paste image/buffer", part("prefix_selection", "Copy mode"), part("paste_image", "Paste image"), part("paste_buffer", "Paste buffer"))
	if st.MultiCopy > 0 {
		copyRow.desc = fmt.Sprintf("Multi copy (%d)/paste image/buffer", st.MultiCopy)
		copyRow.parts[0].desc = fmt.Sprintf("Multi copy (%d)", st.MultiCopy)
	}
	sidebarRow := pair("Sidebar/other/spotlight", part("prefix_toggle_sidebar", "Sidebar"), part("prefix_toggle_other_rail", "Other rail"), part("prefix_toggle_spotlight", "Spotlight"))
	if st.Spotlight {
		// While the beam is on, its row says the key turns it off.
		sidebarRow.desc = "Sidebar/other/spotlight off"
		sidebarRow.parts[2].desc = "Spotlight off"
	}
	modes.rows = append(modes.rows,
		// Copy mode and the two pastes share a line: the leader's menu
		// fills an 80x24 screen, and a line more pushes it off the bottom.
		copyRow,
		// The scrollback browser and the paste buffer list share one for
		// the same reason.
		pair("Scrollback/editor/buffers", part("prefix_scrollback", "Scrollback browser"), part(ActionEditScrollback, "Scrollback in editor"), part("choose_buffer", "Paste buffers")),
		// One row for two keys: the leader's menu fills an 80x24 screen,
		// and a row more pushes it off the bottom.
		sidebarRow,
		row("prefix_explore", "Focus sidebar"),
		row("prefix_file_search", "Search files"),
	)
	return []menuSection{
		{title: "Workspaces", rows: leaderWorkspaceRows},
		{title: "Windows", rows: leaderWindowRows},
		{title: "Panes", rows: leaderPaneRows},
		sessions,
		modes,
		{title: "Menus", rows: leaderMenuRows},
		{title: "Tools", rows: leaderToolRows},
		{title: "Agents", rows: leaderAgentRows},
	}
}

// IsAgentPrefixKeybinding reports whether a prefix menu line is one that only
// means something to a person running agents: the Inbox, the oldest waiting
// item, the Inbox on its mail, reviewing a pane's changes, the newest finished
// turn and the Agents settings tab. The client leaves them out of the menu
// until an agent has been seen; the keys work either way.
func IsAgentPrefixKeybinding(k Keybinding) bool {
	switch k.Action {
	case "prefix_inbox", "prefix_next_attention", "prefix_mail", "prefix_review", "prefix_next_finished", "prefix_agents_settings":
		return true
	}
	return false
}

// IsTransfersPrefixKeybinding reports whether a prefix menu line is the one
// that opens the Copies panel. The client leaves it out while no copy exists,
// as it draws nothing else for copies then; the key works either way. An
// 80x24 screen has no row to spare for it.
func IsTransfersPrefixKeybinding(k Keybinding) bool {
	return k.Action == "prefix_transfers"
}

// IsAgentsSettingsPrefixKeybinding reports whether a prefix menu line is the
// one that opens the settings page's Agents tab. The client leaves it out
// where it has no such tab, such as over SSH.
func IsAgentsSettingsPrefixKeybinding(k Keybinding) bool {
	return k.Action == "prefix_agents_settings"
}

// IsReviewPrefixKeybinding reports whether a prefix menu line is the review
// of the focused pane. The client leaves it out of the menu on a daemon that
// cannot review.
func IsReviewPrefixKeybinding(k Keybinding) bool {
	return k.Action == "prefix_review"
}

// prefixMenuScopes maps a which-key menu to the keyboard scope its keys are
// read from.
var prefixMenuScopes = map[string]string{
	"":          ScopePrefix,
	"window":    ScopePrefixWindow,
	"minimize":  ScopePrefixMinimize,
	"workspace": ScopePrefixWorkspce,
	"debug":     ScopePrefixDebug,
	"tape":      ScopePrefixTape,
	"layout":    ScopePrefixLayout,
	"tuios":     ScopePrefixTuios,
}

// PrefixMenuGroups returns a which-key menu's lines, in the sections the panel
// draws them under, with the keys the registry has bound. prefixType is ""
// for the leader's menu, or the name of a sub-prefix: workspace, minimize,
// window, debug, tape or layout.
//
// A row whose action has no live key is left out: an unbound key, or one
// another action shadows, does nothing, and a menu that offers it is wrong.
// A key bound in the prefix to an action the menu has no row for, such as a
// command entry, gets a row of its own at the end.
//
// A nil registry reads the shipped keymap.
func PrefixMenuGroups(r *KeybindRegistry, prefixType string, st MenuState) []KeybindingGroup {
	if r == nil {
		r = defaultMenuRegistry()
	}
	live := r.liveScope(prefixMenuScopes[prefixType])

	listed := map[string]bool{}
	var groups []KeybindingGroup
	for _, sec := range menuSections(prefixType, st) {
		g := KeybindingGroup{Title: sec.title}
		for _, mr := range sec.rows {
			for _, p := range mr.parts {
				listed[p.action] = true
			}
			g.Bindings = append(g.Bindings, mr.build(live)...)
		}
		if len(g.Bindings) > 0 {
			groups = append(groups, g)
		}
	}

	var extra []Keybinding
	for _, b := range live.order {
		if listed[b.Action] {
			continue
		}
		listed[b.Action] = true
		extra = append(extra, Keybinding{
			Key:         joinKeyLabels(live.keys[b.Action]),
			Description: b.Desc,
			Action:      b.Action,
		})
	}
	if len(extra) > 0 {
		switch {
		case prefixType != "":
			// A sub-prefix is one untitled list; the extra rows go on its end.
			if len(groups) == 0 {
				groups = append(groups, KeybindingGroup{})
			}
			groups[0].Bindings = append(groups[0].Bindings, extra...)
		default:
			groups = append(groups, KeybindingGroup{Title: "Other", Bindings: extra})
		}
	}
	return groups
}

// build turns a row into the lines it draws, given the live keys.
func (mr menuRow) build(live *liveScope) []Keybinding {
	first := mr.parts[0].action
	switch {
	case mr.numbered:
		keys := make([]string, len(mr.parts))
		bound := false
		for i, p := range mr.parts {
			if ks := live.keys[p.action]; len(ks) > 0 {
				keys[i] = ks[0]
				bound = true
			}
		}
		if !bound {
			return nil
		}
		return []Keybinding{{Key: numberedLabel(keys), Description: mr.desc, Action: first}}
	case len(mr.parts) == 1:
		ks := live.keys[first]
		if len(ks) == 0 {
			return nil
		}
		return []Keybinding{{Key: joinKeyLabels(ks), Description: mr.desc, Submenu: mr.submenu, Action: first}}
	}

	firsts := make([]string, 0, len(mr.parts))
	for _, p := range mr.parts {
		if ks := live.keys[p.action]; len(ks) > 0 {
			firsts = append(firsts, KeyLabel(ks[0]))
		}
	}
	if len(firsts) == len(mr.parts) {
		key := strings.Join(firsts, "/")
		if mod, ok := arrowGroup(firsts); ok {
			// The four arrows read as one group: ←↑↓→, or Alt+←↑↓→ with
			// one modifier, which keeps the key column narrow.
			key = mod
			for _, l := range firsts {
				key += l[len(mod):]
			}
		}
		return []Keybinding{{Key: key, Description: mr.desc, Submenu: mr.submenu, Action: first}}
	}
	// A part is unbound, so the shared line would name a key that does not
	// do what it says. Each bound part gets its own line instead.
	var out []Keybinding
	for _, p := range mr.parts {
		if ks := live.keys[p.action]; len(ks) > 0 {
			out = append(out, Keybinding{Key: joinKeyLabels(ks), Description: p.desc, Submenu: mr.submenu, Action: p.action})
		}
	}
	return out
}

// arrowGroup reports whether every label is an arrow with the same modifier
// before it, and returns that modifier ("" for bare arrows).
func arrowGroup(labels []string) (string, bool) {
	mod := ""
	for i, l := range labels {
		var arrow string
		for _, a := range []string{"←", "↑", "↓", "→"} {
			if strings.HasSuffix(l, a) {
				arrow = a
			}
		}
		if arrow == "" {
			return "", false
		}
		m := strings.TrimSuffix(l, arrow)
		if i == 0 {
			mod = m
		} else if m != mod {
			return "", false
		}
	}
	return mod, true
}

// joinKeyLabels shows every key of one action, as "|/\".
func joinKeyLabels(keys []string) string {
	labels := make([]string, len(keys))
	for i, k := range keys {
		labels[i] = KeyLabel(k)
	}
	return strings.Join(labels, "/")
}

// digitKey reads a key as a digit, plain or shifted. "5" is (5, false),
// "shift+5" and "%" are (5, true).
func digitKey(key string) (digit int, shifted, ok bool) {
	k := key
	if rest, found := strings.CutPrefix(strings.ToLower(k), "shift+"); found {
		k, shifted = rest, true
	}
	if len(k) != 1 {
		return 0, false, false
	}
	if k[0] >= '0' && k[0] <= '9' {
		return int(k[0] - '0'), shifted, true
	}
	// The key a config writes as "!" is shift+1 on a US layout.
	if digit, found := shiftedDigitsReverse[k]; found && !shifted {
		return int(digit[0] - '0'), true, true
	}
	return 0, false, false
}

// numberedLabel shows a numbered row's keys, one per action in order and ""
// for an unbound one. A run of consecutive digits reads as a range: the nine
// workspace keys are 1-9, the shifted ones Shift+1-9. A key moved off the
// run, or an unbound one, splits it: 1-2/x/4-9.
func numberedLabel(keys []string) string {
	var pieces []string
	i := 0
	for i < len(keys) {
		if keys[i] == "" {
			i++
			continue
		}
		d, sh, ok := digitKey(keys[i])
		j := i + 1
		if ok {
			for j < len(keys) {
				d2, sh2, ok2 := digitKey(keys[j])
				if !ok2 || sh2 != sh || d2 != d+(j-i) {
					break
				}
				j++
			}
		}
		if j-i >= 2 {
			last, _, _ := digitKey(keys[j-1])
			label := fmt.Sprintf("%d-%d", d, last)
			if sh {
				label = "Shift+" + label
			}
			pieces = append(pieces, label)
		} else {
			pieces = append(pieces, KeyLabel(keys[i]))
		}
		i = j
	}
	return strings.Join(pieces, "/")
}

// keyNames is how a which-key row spells a named key.
var keyNames = map[string]string{
	"esc": "Esc", "escape": "Esc", "tab": "Tab", "enter": "Enter", "return": "Enter",
	"space": "Space", "backspace": "Backspace", "delete": "Delete", "insert": "Insert",
	"home": "Home", "end": "End", "pgup": "PgUp", "pgdown": "PgDn",
	"left": "←", "right": "→", "up": "↑", "down": "↓",
	"ctrl": "Ctrl", "alt": "Alt", "shift": "Shift", "super": "Super",
	"opt": "Opt", "option": "Opt", "cmd": "Cmd", "meta": "Meta", "hyper": "Hyper",
}

// KeyLabel is how a which-key row shows one key from the config: named keys
// and modifiers capitalised (esc is Esc, shift+tab is Shift+Tab), the arrows
// as arrows, and a printable key as it is.
func KeyLabel(key string) string {
	if utf8.RuneCountInString(key) == 1 {
		return key
	}
	parts := strings.Split(key, "+")
	// "ctrl++" is ctrl and the plus key.
	if before, ok := strings.CutSuffix(key, "++"); ok {
		parts = append(strings.Split(before, "+"), "+")
	}
	for i, p := range parts {
		if name, ok := keyNames[strings.ToLower(p)]; ok {
			parts[i] = name
			continue
		}
		if len(p) >= 2 && (p[0] == 'f' || p[0] == 'F') && strings.Trim(p[1:], "0123456789") == "" {
			parts[i] = "F" + p[1:]
		}
	}
	return strings.Join(parts, "+")
}

// liveScope is one scope's keys that run: per action in config order, and the
// bindings themselves in the order the registry reports them.
type liveScope struct {
	keys  map[string][]string
	order []Binding
}

// liveScope returns the scope's live keys, kept until the next buildMappings
// with the help's table: a which-key menu is drawn every frame while it is
// open, and a rebind ends in a Reload that drops this.
func (r *KeybindRegistry) liveScope(scope string) *liveScope {
	r.pressesMu.Lock()
	defer r.pressesMu.Unlock()
	if ls := r.live[scope]; ls != nil {
		return ls
	}
	if r.live == nil {
		r.live = map[string]*liveScope{}
	}
	ls := &liveScope{keys: map[string][]string{}}
	for _, b := range r.Bindings() {
		if b.Scope != scope || b.Unbound || b.Shadowed || b.Key == "" {
			continue
		}
		ls.keys[b.Action] = append(ls.keys[b.Action], b.Key)
		ls.order = append(ls.order, b)
	}
	r.live[scope] = ls
	return ls
}

// defaultMenuRegistry reads the shipped keymap, for a caller with no
// registry of its own.
var defaultMenuRegistry = sync.OnceValue(func() *KeybindRegistry {
	return NewKeybindRegistry(DefaultConfig())
})
