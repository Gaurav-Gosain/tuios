package app

import (
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/invisible"
)

// The right-click menus as data. Each target's shipped rows are a list of
// menuRow below: the action a row runs, its glyph and label, and two
// conditions on the menu's facts, whether it shows and whether it is dimmed.
// A row that reads differently by state (a file's open row, a group's fold,
// the kill rows of the attached session) is a few rows whose show conditions
// leave one. The builders in contextmenu_build.go work out the facts and the
// title, and buildMenu makes the rows.
//
// A [menus.TARGET] table (config/menus_config.go) changes the shipped rows: it
// replaces them, or adds rows after one and removes others. A row it names by
// a built-in action that the target ships keeps that row's glyph, label and
// conditions; another built-in action reads its description; user:NAME reads
// the action's label, and shows only on a target the action is offered on,
// when its when matches. A { menu = "NAME" } row, the action open_menu:NAME,
// opens that menu in the menu's place: a [menus.NAME], or the older
// [keybindings.menus.NAME] read as rows.
//
// The facts are bits and a condition is two masks, not a func: a func per
// condition cost about 20 KB of binary in closures and their line tables.

// menuFact is one fact a menu's rows read about its target.
type menuFact uint32

const (
	// pane
	mfSelection menuFact = 1 << iota
	mfCanPaste
	mfCanSplit
	mfScratch
	mfSidebar
	// dock item: the window is one of the first nine minimized
	mfRestorable
	// dock, workspace
	mfMinimized
	mfWsCurrent
	mfWsEmpty
	// session, repo
	mfAttached
	mfOthers
	mfDeletable
	mfNoWorktree
	mfHeadRepo
	mfCollapsed
	mfNoRepoRoot
	// machine
	mfFirst
	mfLast
	mfRestart
	// file
	mfUp
	mfDir
	mfNamed
	mfOn
	mfTarget
	mfCanOpen
	mfClipEmpty
	mfNoFocus
)

// is is v when c holds, for building a menu's facts.
func is(c bool, v menuFact) menuFact {
	if c {
		return v
	}
	return 0
}

// menuFacts are what a menu's rows read, worked out once when it opens.
type menuFacts struct {
	bits menuFact
	// pos is the dock item's place among the minimized windows.
	pos int
}

// rowCond holds when every bit of all is set and no bit of none is, or the
// opposite when not is set.
type rowCond struct {
	all, none menuFact
	not       bool
}

func (c rowCond) holds(f menuFact) bool {
	return (f&c.all == c.all && f&c.none == 0) != c.not
}

// menuRow is one row. An action of config.MenuSeparator is a separator. The
// zero show always shows, and the zero dim never dims. when are the when
// tables of a row from config, the action's and the row's own, which all
// have to match.
type menuRow struct {
	action, icon, label string
	show, dim           rowCond
	warn                bool
	when                []map[string]any
}

// sep is a separator row.
var sep = menuRow{action: config.MenuSeparator}

var (
	menuFoldRows = []menuRow{
		{action: "repo_toggle_fold", icon: glyphDown, label: "Unfold group", show: rowCond{all: mfHeadRepo | mfCollapsed}},
		{action: "repo_toggle_fold", icon: glyphUp, label: "Fold group", show: rowCond{all: mfHeadRepo, none: mfCollapsed}},
	}
	menuPaneRows = []menuRow{
		{action: "copy_selection", icon: glyphCopy, label: "Copy selection", dim: rowCond{none: mfSelection}},
		{action: "paste_clipboard", icon: glyphPaste, label: "Paste", dim: rowCond{none: mfCanPaste}},
		sep,
		{action: "split_vertical", icon: glyphSplitV, label: "Split right", dim: rowCond{none: mfCanSplit}},
		{action: "split_horizontal", icon: glyphSplitH, label: "Split down", dim: rowCond{none: mfCanSplit}},
		sep,
		{action: "rename_window", icon: glyphRename, label: "Rename"},
		{action: "set_accent", icon: glyphPalette, label: "Accent color", dim: rowCond{none: mfSidebar}},
		{action: "toggle_zoom", icon: glyphZoom, label: "Zoom"},
		{action: "screenshot_window", icon: glyphCopy, label: "Screenshot this window"},
		{action: "minimize_window", icon: glyphMinimize, label: "Minimize", dim: rowCond{all: mfScratch}},
		{action: "close_window", icon: glyphClose, label: "Close pane", warn: true},
	}
	menuSelectionRows = []menuRow{
		{action: "copy_selection", icon: glyphCopy, label: "Copy selection"},
		{action: "paste_clipboard", icon: glyphPaste, label: "Paste"},
		{action: "clear_selection", icon: glyphClear, label: "Clear selection"},
	}
	menuDockItemRows = []menuRow{
		{action: "restore_minimized_{pos}", icon: glyphRestore, label: "Restore", dim: rowCond{none: mfRestorable}},
		{action: "restore_all", icon: glyphRestore, label: "Restore all"},
	}
	menuWorkspaceRows = []menuRow{
		{action: "workspace_pill_switch", icon: glyphSwitch, label: "Switch to", dim: rowCond{all: mfWsCurrent}},
		{action: "workspace_prefix_rename", icon: glyphRename, label: "Rename"},
		sep,
		{action: "close_workspace", icon: glyphClose, label: "Close", dim: rowCond{all: mfWsEmpty}, warn: true},
	}
	menuDockRows = []menuRow{
		{action: "new_window", icon: glyphNew, label: "New window"},
		{action: "toggle_tiling", icon: glyphTiling, label: "Toggle tiling"},
		{action: "restore_all", icon: glyphRestore, label: "Restore all", dim: rowCond{none: mfMinimized}},
	}
	menuDesktopRows = []menuRow{
		{action: "new_window", icon: glyphNew, label: "New window"},
		{action: "toggle_tiling", icon: glyphTiling, label: "Toggle tiling"},
		sep,
		{action: "command_palette", icon: glyphPalette, label: "Command palette"},
		{action: "open_settings", icon: glyphSettings, label: "Settings"},
		{action: "toggle_help", icon: glyphHelp, label: "Help"},
	}
	menuSessionRows = slices.Concat([]menuRow{
		{action: "rename_session", icon: glyphRename, label: "Rename"},
		{action: "set_session_accent", icon: glyphPalette, label: "Session color"},
		{action: "set_note", icon: glyphRename, label: "Note..."},
		{action: "prefix_detach", icon: glyphDetach, label: "Detach", dim: rowCond{none: mfAttached}},
		{action: "prefix_session_switcher", icon: glyphSwitch, label: "Switch session..."},
		{action: "prefix_workspace_switcher", icon: glyphSwitch, label: "Switch workspace...", dim: rowCond{none: mfAttached}},
		{action: "session_new_worktree", icon: glyphNew, label: "New worktree...", dim: rowCond{all: mfNoWorktree}},
	}, menuFoldRows, []menuRow{
		sep,
		{action: "kill_session", icon: glyphClose, label: "Kill session", show: rowCond{none: mfAttached}, warn: true},
		{action: "kill_session_next", icon: glyphClose, label: "Kill session, go to next", show: rowCond{all: mfAttached}, dim: rowCond{none: mfOthers}, warn: true},
		{action: "kill_session_quit", icon: glyphClose, label: "Kill session and quit", show: rowCond{all: mfAttached, none: mfDeletable}, warn: true},
		{action: "prefix_close_session", icon: glyphClose, label: "Kill session and quit", show: rowCond{all: mfAttached | mfDeletable}, warn: true},
	})
	menuRepoRows = slices.Concat([]menuRow{
		{action: "repo_new_worktree", icon: glyphNew, label: "New worktree...", dim: rowCond{all: mfNoRepoRoot}},
	}, menuFoldRows)
	menuMachineRows = []menuRow{
		{action: "reorder_up", icon: glyphUp, label: "Move up", dim: rowCond{all: mfFirst}},
		{action: "reorder_down", icon: glyphDown, label: "Move down", dim: rowCond{all: mfLast}},
		{action: config.MenuSeparator, show: rowCond{all: mfRestart}},
		{action: "restart_daemon", icon: glyphRestore, label: "Restart the daemon", show: rowCond{all: mfRestart}},
	}
	// A row that names nothing has nothing to open, so mfCanOpen is never set
	// with the open row of an unnamed one, which is always dimmed.
	menuFileRows = []menuRow{
		{action: "file_open", icon: glyphUp, label: "Go up", show: rowCond{all: mfUp}, dim: rowCond{none: mfCanOpen}},
		{action: "file_open", icon: glyphFolder, label: "Open folder", show: rowCond{all: mfDir | mfNamed, none: mfUp}, dim: rowCond{none: mfCanOpen}},
		{action: "file_open", icon: glyphCopy, label: "Copy path", show: rowCond{all: mfNamed, none: mfUp | mfDir}, dim: rowCond{none: mfCanOpen}},
		{action: "file_open", icon: glyphFile, label: "Open", show: rowCond{none: mfUp | mfNamed}, dim: rowCond{none: mfCanOpen}},
		{action: "file_quick_look", icon: glyphPalette, label: "Preview", show: rowCond{all: mfNamed, none: mfUp}, dim: rowCond{none: mfCanOpen}},
		{action: "file_edit", icon: glyphFile, label: "Edit", show: rowCond{all: mfNamed, none: mfUp | mfDir}, dim: rowCond{none: mfCanOpen}},
		{action: "file_open_system", icon: glyphFile, label: "Open with system", show: rowCond{all: mfNamed, none: mfUp}, dim: rowCond{none: mfCanOpen}},
		{action: "file_copy_path", icon: glyphCopy, label: "Copy path", show: rowCond{all: mfNamed | mfDir, none: mfUp}, dim: rowCond{none: mfCanOpen}},
		sep,
		{action: "file_copy", icon: glyphCopy, label: "Copy", dim: rowCond{none: mfTarget}},
		{action: "file_cut", icon: glyphCut, label: "Cut", dim: rowCond{none: mfTarget}},
		{action: "file_paste", icon: glyphPaste, label: "Paste", dim: rowCond{all: mfOn, none: mfClipEmpty, not: true}},
		{action: "file_send", icon: glyphCopy, label: "Send to host", dim: rowCond{none: mfTarget}},
		{action: "file_insert_path", icon: glyphPaste, label: "Paste path in pane", dim: rowCond{all: mfNamed, none: mfUp | mfNoFocus, not: true}},
		sep,
		{action: "file_create", icon: glyphNew, label: "New file or folder", dim: rowCond{none: mfOn}},
		{action: "file_rename", icon: glyphRename, label: "Rename", dim: rowCond{none: mfTarget}},
		{action: "file_delete", icon: glyphClose, label: "Delete", dim: rowCond{none: mfTarget}, warn: true},
		{action: "file_delete_forever", icon: glyphClose, label: "Delete for good", dim: rowCond{none: mfTarget}, warn: true},
	}
)

// shippedRows are the rows tuios ships for a target, nil for a submenu.
func shippedRows(name string) []menuRow {
	switch name {
	case "pane":
		return menuPaneRows
	case "selection":
		return menuSelectionRows
	case "dock_item":
		return menuDockItemRows
	case "workspace":
		return menuWorkspaceRows
	case "dock":
		return menuDockRows
	case "desktop":
		return menuDesktopRows
	case "session":
		return menuSessionRows
	case "repo":
		return menuRepoRows
	case "machine":
		return menuMachineRows
	case "file":
		return menuFileRows
	}
	return nil
}

// menuDepthMax bounds how deep submenus nest, so a loop a validation warning
// named still stops.
const menuDepthMax = 8

// menuRowsFor is the rows of the menu name before the facts: the shipped
// rows with the person's table applied, or a declared menu's rows.
func (m *OS) menuRowsFor(name string) []menuRow {
	rows := shippedRows(name)
	if m.UserConfig == nil {
		return rows
	}
	if entries, _, ok := m.UserConfig.MenuRows(name); ok {
		return m.menuRowsOf(name, entries)
	}
	spec, ok := m.UserConfig.Menus[name]
	if !ok {
		return rows
	}
	if items, set := spec.MenuItems(); set {
		return m.menuRowsOf(name, items)
	}
	var kept []menuRow
	at := -1
	for _, r := range rows {
		if !slices.Contains(spec.Remove, strings.TrimSuffix(r.action, "{pos}")) {
			kept = append(kept, r)
			if r.action == spec.After {
				at = len(kept)
			}
		}
	}
	if at < 0 {
		at = len(kept)
	}
	return slices.Concat(kept[:at], m.menuRowsOf(name, spec.MenuAdd()), kept[at:])
}

// menuRowsOf turns the rows a person wrote into menu rows for the menu name.
func (m *OS) menuRowsOf(name string, entries []config.MenuEntry) []menuRow {
	var out []menuRow
	for _, e := range entries {
		if e.Sep {
			out = append(out, sep)
			continue
		}
		n := len(out)
		switch {
		case e.Menu != "":
			_, label, _ := m.UserConfig.MenuRows(e.Menu)
			if label == "" {
				label = e.Menu
			}
			out = append(out, menuRow{action: e.RowAction(), icon: glyphSwitch, label: label})
		case strings.HasPrefix(e.Action, config.UserActionPrefix):
			out = m.appendUserRow(out, name, e.Action)
		default:
			for _, r := range shippedRows(name) {
				if strings.TrimSuffix(r.action, "{pos}") == e.Action {
					out = append(out, r)
				}
			}
			if spec, ok := config.LookupAction(e.Action); len(out) == n && ok {
				label := spec.Desc
				if label == "" {
					label = e.Action
				}
				out = append(out, menuRow{action: e.Action, label: label})
			}
		}
		// The row's own label and when apply to what it became. A label from
		// config passes the invisible-character strip, as every label here.
		for i := n; i < len(out); i++ {
			if e.Label != "" {
				out[i].label = e.Label
			}
			out[i].label = invisible.Strip(printableTitle(out[i].label))
			if e.When != nil {
				out[i].when = append(slices.Clone(out[i].when), e.When)
			}
		}
	}
	return out
}

// appendUserRow adds the row of the user action behind action to the menu
// name, when the action is offered there. A submenu takes the action
// wherever its parent menu is. A [[keybindings.command]] entry is offered on
// a pane.
func (m *OS) appendUserRow(out []menuRow, name, action string) []menuRow {
	a, ok := m.UserConfig.UserActionFor(action)
	if !ok {
		c, ok := m.UserConfig.Keybindings.CommandFor(config.CommandActionPrefix + strings.TrimPrefix(action, config.UserActionPrefix))
		if !ok {
			return out
		}
		a.Label = c.Label()
	}
	targets := a.Targets
	if len(targets) == 0 {
		targets = []string{"pane"}
	}
	if config.IsMenuTarget(name) && !slices.Contains(targets, name) {
		return out
	}
	r := menuRow{action: action, label: a.DisplayLabel()}
	if a.When != nil {
		r.when = []map[string]any{a.When}
	}
	return append(out, r)
}

// buildMenu makes the rows of the menu name for the facts f: the rows that
// show, each dimmed or not, with no separator at either end or two in a row.
func (m *OS) buildMenu(name string, f menuFacts) []ContextMenuItem {
	var out []ContextMenuItem
	var user map[string]string
	for _, r := range m.menuRowsFor(name) {
		if !r.show.holds(f.bits) {
			continue
		}
		if r.when != nil && user == nil {
			user = m.userFacts()
		}
		if slices.ContainsFunc(r.when, func(w map[string]any) bool { return !config.WhenMatches(w, user) }) {
			continue
		}
		if r.action == config.MenuSeparator {
			if len(out) > 0 && !out[len(out)-1].Sep {
				out = append(out, separator())
			}
			continue
		}
		action, ok := strings.CutSuffix(r.action, "{pos}")
		if ok {
			action += strconv.Itoa(f.pos + 1)
			if f.bits&mfRestorable == 0 {
				action = ""
			}
		}
		// A row from config has no glyph. A blank one keeps its label in line
		// with the labels of the rows that have one.
		icon := r.icon
		if icon == "" {
			icon = " "
		}
		it := m.item(icon, r.label, action, r.dim != rowCond{} && r.dim.holds(f.bits))
		it.Warn = r.warn
		// A row that opens a menu says so in the right column, with the
		// cursor's chevron, where the other rows have their key.
		if _, sub := config.MenuNameOf(action); sub {
			it.Hint = chevron(&m.Settings)
		}
		out = append(out, it)
	}
	if n := len(out); n > 0 && out[n-1].Sep {
		out = out[:n-1]
	}
	return out
}

// userFacts are what an action's when reads about the focused pane.
func (m *OS) userFacts() map[string]string {
	f := map[string]string{"host": m.AttachedHost, "has_selection": "false"}
	if w := m.GetFocusedWindow(); w != nil {
		f["program"], f["agent_state"], f["harness"] = w.ForegroundCmd, w.AgentState, w.AgentHarness
		f["has_selection"] = strconv.FormatBool(w.HasSelection())
	}
	return f
}

// dockItemRows is the dock item menu of the minimized window at pos.
func (m *OS) dockItemRows(pos int) []ContextMenuItem {
	return m.buildMenu("dock_item", menuFacts{bits: is(pos >= 0 && pos < 9, mfRestorable), pos: pos})
}

// menu builds the menu name from the facts bits.
func (m *OS) menu(name string, bits menuFact) []ContextMenuItem {
	return m.buildMenu(name, menuFacts{bits: bits})
}

// OpenSubmenu opens the submenu name in the place of the menu its row was
// taken from: the same anchor and the same target, so a row in it acts on
// what the first menu was opened on.
func (m *OS) OpenSubmenu(name string) {
	from := m.menuFrom
	if from == nil || m.UserConfig == nil || from.depth >= menuDepthMax || config.IsMenuTarget(name) {
		return
	}
	_, title, ok := m.UserConfig.MenuRows(name)
	if !ok {
		return
	}
	cm := *from
	cm.depth++
	cm.Title = name
	if title != "" {
		cm.Title = invisible.Strip(printableTitle(title))
	}
	cm.Items = m.menu(name, 0)
	cm.Scroll, cm.ScrollFrom = 0, 0
	cm.Selected = -1
	cm.Selected = cm.Next(1)
	m.ContextMenu = &cm
}
