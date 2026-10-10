package config

import (
	"fmt"
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

func subRow(action, desc string) menuRow {
	r := row(action, desc)
	r.submenu = true
	return r
}

func pair(desc string, parts ...menuPart) menuRow { return menuRow{parts: parts, desc: desc} }

func part(action, desc string) menuPart { return menuPart{action, desc} }

// numbered is a row of actions that differ only in a number, such as the nine
// workspace switches.
func numbered(desc string, actions ...string) menuRow {
	r := menuRow{desc: desc, numbered: true}
	for _, a := range actions {
		r.parts = append(r.parts, menuPart{a, desc})
	}
	return r
}

// actionRun is the actions prefix+from .. prefix+to, the names a numbered row
// is built from.
func actionRun(prefix string, from, to, step int) []string {
	var out []string
	for n := from; n <= to; n += step {
		out = append(out, fmt.Sprintf("%s%d", prefix, n))
	}
	return out
}

type menuSection struct {
	title string
	rows  []menuRow
}

// menuSections is the layout of one which-key menu. The sub-prefixes are one
// untitled section each: they are a handful of lines and a heading over them
// would say what the panel's title already does.
func menuSections(prefixType string, st MenuState) []menuSection {
	one := func(rows ...menuRow) []menuSection { return []menuSection{{rows: rows}} }
	cancel := func(action string) menuRow { return row(action, "Cancel") }
	switch prefixType {
	case "workspace":
		return one(
			numbered("Switch to workspace", actionRun("workspace_prefix_switch_", 1, 9, 1)...),
			numbered("Move window to workspace", actionRun("workspace_prefix_move_", 1, 9, 1)...),
			row("workspace_prefix_rename", "Rename workspace"),
			cancel("workspace_prefix_cancel"),
		)
	case "minimize":
		restore := "Restore window"
		if st.Minimized >= 0 {
			restore = fmt.Sprintf("Restore window (%d minimized)", st.Minimized)
		}
		return one(
			row("minimize_prefix_focused", "Minimize focused window"),
			numbered(restore, actionRun("minimize_prefix_restore_", 1, 9, 1)...),
			row("minimize_prefix_restore_all", "Restore all"),
			cancel("minimize_prefix_cancel"),
		)
	case "window":
		return one(
			row("window_prefix_new", "New window"),
			row("window_prefix_close", "Close window"),
			row("window_prefix_rename", "Rename window"),
			row("window_prefix_next", "Next window"),
			row("window_prefix_prev", "Previous window"),
			row("window_prefix_tiling", "Toggle tiling mode"),
			cancel("window_prefix_cancel"),
		)
	case "debug":
		return one(
			row("debug_prefix_logs", "Toggle log viewer"),
			row("debug_prefix_cache", "Toggle cache statistics"),
			row("debug_prefix_showkeys", "Toggle showkeys overlay"),
			row("debug_prefix_animations", "Toggle animations"),
			cancel("debug_prefix_cancel"),
		)
	case "tape":
		return one(
			row("tape_prefix_manager", "Open tape manager"),
			row("tape_prefix_review", "Review project tape"),
			row("tape_prefix_record", "Start recording"),
			row("tape_prefix_stop", "Stop recording"),
			cancel("tape_prefix_cancel"),
		)
	case "layout":
		return one(
			row("layout_prefix_load", "Load layout"),
			row("layout_prefix_save", "Save layout"),
			numbered("Snap window to a corner", actionRun("snap_corner_", 1, 4, 1)...),
			numbered("Resize focused window width (%)", actionRun("resize_width_", 50, 90, 10)...),
			numbered("Resize focused window height (%)", actionRun("resize_height_", 50, 90, 10)...),
			row("cycle_master_position", "Move master to next side"),
			row("swap_with_master", "Swap with master"),
			row("focus_master", "Focus master"),
			pair("Add/remove master", part("add_master", "Add master"), part("remove_master", "Remove master")),
			cancel("layout_prefix_cancel"),
		)
	}

	// The leader's menu, in the sections the panel flows into columns. Each
	// section is a few lines, so a column holds a section or two whole and the
	// panel stays short enough to read without scrolling the eye down a list.
	windows := menuSection{title: "Windows", rows: []menuRow{
		row("prefix_new_window", "Create window"),
		row("prefix_close_window", "Close window"),
		row("prefix_rename_window", "Rename window"),
		pair("Next/prev window", part("prefix_next_window", "Next window"), part("prefix_prev_window", "Previous window")),
		numbered("Jump to window", actionRun("prefix_select_", 0, 9, 1)...),
		row("prefix_fullscreen", "Toggle zoom"),
	}}
	panes := menuSection{title: "Panes", rows: []menuRow{
		// The arrows walk panes, and the prefix stays armed for a moment
		// so a run of them costs one prefix press. See the repeat window
		// in internal/input/prefix_repeat.go.
		pair("Focus pane",
			part("terminal_focus_left", "Focus pane left"),
			part("terminal_focus_up", "Focus pane up"),
			part("terminal_focus_down", "Focus pane down"),
			part("terminal_focus_right", "Focus pane right")),
		row("prefix_toggle_tiling", "Toggle tiling"),
		row("prefix_split_horizontal", "Split horizontal"),
		row("prefix_split_vertical", "Split vertical"),
		row("prefix_rotate_split", "Rotate split"),
		row("prefix_equalize_splits", "Equalize splits"),
		// Hints label the focused pane's text. The row is here rather than
		// under Tools because Tools shares a column with Menus in the narrow
		// layout, and one row more there made the panel the tallest column
		// at 80x24, where it reached the pane's bottom border.
		// The pane labels share the hints row: one row more made the
		// panel reach the pane's top border at 80x24.
		pair("Hints, labels", part("hints", "Hints"), part("display_panes", "Pane labels")),
	}}
	sessions := menuSection{title: "Sessions", rows: []menuRow{
		pair("Prev/next session", part("prev_session", "Previous session"), part("next_session", "Next session")),
		row("prefix_session_switcher", "Sessions"),
		row("prefix_workspace_switcher", "Workspaces"),
		row("choose_tree", "Find a pane"),
		row("prefix_close_session", "Close session"),
		row("toggle_scratch", "Scratch session"),
	}}
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
		pair("Scrollback/buffers", part("prefix_scrollback", "Scrollback browser"), part("choose_buffer", "Paste buffers")),
		// One row for two keys: the leader's menu fills an 80x24 screen,
		// and a row more pushes it off the bottom.
		sidebarRow,
		row("prefix_explore", "Focus sidebar"),
		row("prefix_file_search", "Search files"),
	)
	menus := menuSection{title: "Menus", rows: []menuRow{
		subRow("prefix_workspace", "Workspace"),
		subRow("prefix_minimize", "Minimize"),
		subRow("prefix_window", "Window"),
		subRow("prefix_layout", "Layout"),
		subRow("prefix_tape", "Tape"),
		subRow("prefix_debug", "Debug"),
	}}
	tools := menuSection{title: "Tools", rows: []menuRow{
		row("prefix_command_palette", "Command palette"),
		row("launcher", "Launcher"),
		row("prefix_settings", "Settings"),
		row("prefix_keybinds", "Keybindings"),
		row("prefix_screenshot", "Screenshot"),
		row("prefix_jump_notif", "Newest message"),
		row("prefix_last_message", "Show last message"),
		row("prefix_help", "Help"),
	}}
	agents := menuSection{title: "Agents", rows: []menuRow{
		row("prefix_inbox", "Inbox"),
		row("prefix_next_attention", "Oldest waiting"),
		row("prefix_mail", "Inbox: mail"),
		row("prefix_next_finished", "Newest finished"),
		row("prefix_review", "Review changes"),
		row("prefix_agents_settings", "Agents settings"),
	}}
	return []menuSection{windows, panes, sessions, modes, menus, tools, agents}
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

// PrefixMenuActions lists every action a which-key menu has a row for, in
// order. The coverage test reads it to check that each action a prefix binds
// by default has a row with a description.
func PrefixMenuActions(prefixType string, st MenuState) []string {
	var out []string
	for _, sec := range menuSections(prefixType, st) {
		for _, mr := range sec.rows {
			for _, p := range mr.parts {
				out = append(out, p.action)
			}
		}
	}
	return out
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
		sep := "/"
		if allArrows(firsts) {
			// The four arrows read as one group: ←↑↓→.
			sep = ""
		}
		return []Keybinding{{Key: strings.Join(firsts, sep), Description: mr.desc, Submenu: mr.submenu, Action: first}}
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

func allArrows(labels []string) bool {
	for _, l := range labels {
		switch l {
		case "←", "↑", "↓", "→":
		default:
			return false
		}
	}
	return true
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
	if strings.HasSuffix(key, "++") {
		parts = append(strings.Split(strings.TrimSuffix(key, "++"), "+"), "+")
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
