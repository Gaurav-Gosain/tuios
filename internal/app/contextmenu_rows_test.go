package app

import (
	"fmt"
	"reflect"
	"testing"
)

// The shipped menus became data (contextmenu_rows.go). The builders below are
// the code they replaced, kept as the oracle: each reads the same facts the
// old builder read off the OS, in the old builder's own words. Every target's
// menu is compared over every combination of its facts, so a row whose label,
// glyph, hint, dim, warn or place moved fails here and not in front of a
// person.

func legacyPane(m *OS, f menuFact) []ContextMenuItem {
	closeItem := m.item(glyphClose, "Close pane", "close_window", false)
	closeItem.Warn = true
	return []ContextMenuItem{
		m.item(glyphCopy, "Copy selection", "copy_selection", !f.has(mfSelection)),
		m.item(glyphPaste, "Paste", "paste_clipboard", !f.has(mfCanPaste)),
		separator(),
		m.item(glyphSplitV, "Split right", "split_vertical", !f.has(mfCanSplit)),
		m.item(glyphSplitH, "Split down", "split_horizontal", !f.has(mfCanSplit)),
		separator(),
		m.item(glyphRename, "Rename", "rename_window", false),
		m.item(glyphPalette, "Accent color", "set_accent", !f.has(mfSidebar)),
		m.item(glyphZoom, "Zoom", "toggle_zoom", false),
		m.item(glyphCopy, "Screenshot this window", "screenshot_window", false),
		m.item(glyphMinimize, "Minimize", "minimize_window", f.has(mfScratch)),
		closeItem,
	}
}

func legacyDockItem(m *OS, f menuFact) []ContextMenuItem {
	pos := legacyPos
	action := ""
	if pos >= 0 && pos < 9 {
		action = "restore_minimized_" + string(rune('1'+pos))
	}
	return []ContextMenuItem{
		m.item(glyphRestore, "Restore", action, action == ""),
		m.item(glyphRestore, "Restore all", "restore_all", false),
	}
}

func legacyWorkspace(m *OS, f menuFact) []ContextMenuItem {
	closeItem := m.item(glyphClose, "Close", "close_workspace", f.has(mfWsEmpty))
	closeItem.Warn = true
	return []ContextMenuItem{
		m.item(glyphSwitch, "Switch to", "workspace_pill_switch", f.has(mfWsCurrent)),
		m.item(glyphRename, "Rename", "workspace_prefix_rename", false),
		separator(),
		closeItem,
	}
}

func legacyDock(m *OS, f menuFact) []ContextMenuItem {
	return []ContextMenuItem{
		m.item(glyphNew, "New window", "new_window", false),
		m.item(glyphTiling, "Toggle tiling", "toggle_tiling", false),
		m.item(glyphRestore, "Restore all", "restore_all", !f.has(mfMinimized)),
	}
}

func legacyDesktop(m *OS, _ menuFact) []ContextMenuItem {
	return []ContextMenuItem{
		m.item(glyphNew, "New window", "new_window", false),
		m.item(glyphTiling, "Toggle tiling", "toggle_tiling", false),
		separator(),
		m.item(glyphPalette, "Command palette", "command_palette", false),
		m.item(glyphSettings, "Settings", "open_settings", false),
		m.item(glyphHelp, "Help", "toggle_help", false),
	}
}

func legacyFold(m *OS, f menuFact) ContextMenuItem {
	if f.has(mfCollapsed) {
		return m.item(glyphDown, "Unfold group", "repo_toggle_fold", false)
	}
	return m.item(glyphUp, "Fold group", "repo_toggle_fold", false)
}

func legacySession(m *OS, f menuFact) []ContextMenuItem {
	kill := []ContextMenuItem{m.item(glyphClose, "Kill session", "kill_session", false)}
	if f.has(mfAttached) {
		killNext := m.item(glyphClose, "Kill session, go to next", "kill_session_next", !f.has(mfOthers))
		killQuitAction := "kill_session_quit"
		if f.has(mfDeletable) {
			killQuitAction = "prefix_close_session"
		}
		killQuit := m.item(glyphClose, "Kill session and quit", killQuitAction, false)
		kill = []ContextMenuItem{killNext, killQuit}
	}
	for i := range kill {
		kill[i].Warn = true
	}
	rows := []ContextMenuItem{
		m.item(glyphRename, "Rename", "rename_session", false),
		m.item(glyphPalette, "Session color", "set_session_accent", false),
		m.item(glyphRename, "Note...", "set_note", false),
		m.item(glyphDetach, "Detach", "prefix_detach", !f.has(mfAttached)),
		m.item(glyphSwitch, "Switch session...", "prefix_session_switcher", false),
		m.item(glyphSwitch, "Switch workspace...", "prefix_workspace_switcher", !f.has(mfAttached)),
		m.item(glyphNew, "New worktree...", "session_new_worktree", f.has(mfNoWorktree)),
	}
	if f.has(mfHeadRepo) {
		rows = append(rows, legacyFold(m, f))
	}
	return append(append(rows, separator()), kill...)
}

func legacyRepo(m *OS, f menuFact) []ContextMenuItem {
	return []ContextMenuItem{
		m.item(glyphNew, "New worktree...", "repo_new_worktree", f.has(mfNoRepoRoot)),
		legacyFold(m, f),
	}
}

func legacySelection(m *OS, _ menuFact) []ContextMenuItem {
	return []ContextMenuItem{
		m.item(glyphCopy, "Copy selection", "copy_selection", false),
		m.item(glyphPaste, "Paste", "paste_clipboard", false),
		m.item(glyphClear, "Clear selection", "clear_selection", false),
	}
}

func legacyMachine(m *OS, f menuFact) []ContextMenuItem {
	items := []ContextMenuItem{
		m.item(glyphUp, "Move up", "reorder_up", f.has(mfFirst)),
		m.item(glyphDown, "Move down", "reorder_down", f.has(mfLast)),
	}
	if f.has(mfRestart) {
		items = append(items, separator(), m.item(glyphRestore, "Restart the daemon", "restart_daemon", false))
	}
	return items
}

func legacyFile(m *OS, f menuFact) []ContextMenuItem {
	on, hasTarget, canOpen := f.has(mfOn), f.has(mfTarget), f.has(mfCanOpen)
	open := m.item(glyphFile, "Open", "file_open", true)
	switch {
	case f.has(mfUp):
		open = m.item(glyphUp, "Go up", "file_open", !canOpen)
	case f.has(mfDir) && f.has(mfNamed):
		open = m.item(glyphFolder, "Open folder", "file_open", !canOpen)
	case f.has(mfNamed):
		open = m.item(glyphCopy, "Copy path", "file_open", !canOpen)
	}
	del := m.item(glyphClose, "Delete", "file_delete", !hasTarget)
	del.Warn = true
	forever := m.item(glyphClose, "Delete for good", "file_delete_forever", !hasTarget)
	forever.Warn = true
	rows := []ContextMenuItem{open}
	if f.has(mfNamed) && !f.has(mfUp) {
		rows = append(rows, m.item(glyphPalette, "Preview", "file_quick_look", !canOpen))
	}
	if f.has(mfNamed) && !f.has(mfDir) && !f.has(mfUp) {
		rows = append(rows, m.item(glyphFile, "Edit", "file_edit", !canOpen))
	}
	if f.has(mfNamed) && !f.has(mfUp) {
		rows = append(rows, m.item(glyphFile, "Open with system", "file_open_system", !canOpen))
	}
	if f.has(mfNamed) && f.has(mfDir) && !f.has(mfUp) {
		rows = append(rows, m.item(glyphCopy, "Copy path", "file_copy_path", !canOpen))
	}
	return append(rows,
		separator(),
		m.item(glyphCopy, "Copy", "file_copy", !hasTarget),
		m.item(glyphCut, "Cut", "file_cut", !hasTarget),
		m.item(glyphPaste, "Paste", "file_paste", !on || f.has(mfClipEmpty)),
		m.item(glyphCopy, "Send to host", "file_send", !hasTarget),
		m.item(glyphPaste, "Paste path in pane", "file_insert_path", !f.has(mfNamed) || f.has(mfUp) || f.has(mfNoFocus)),
		separator(),
		m.item(glyphNew, "New file or folder", "file_create", !on),
		m.item(glyphRename, "Rename", "file_rename", !hasTarget),
		del,
		forever,
	)
}

// menuFactFlags are the facts each target reads, so the test can walk every
// combination of them.
var menuFactFlags = map[string][]menuFact{
	"pane":      {mfSelection, mfCanPaste, mfCanSplit, mfScratch, mfSidebar},
	"selection": nil,
	"desktop":   nil,
	"dock":      {mfMinimized},
	"dock_item": nil,
	"workspace": {mfWsCurrent, mfWsEmpty},
	"session":   {mfAttached, mfOthers, mfDeletable, mfNoWorktree, mfHeadRepo, mfCollapsed},
	"repo":      {mfNoRepoRoot, mfCollapsed},
	"machine":   {mfFirst, mfLast, mfRestart},
	"file":      {mfUp, mfDir, mfNamed, mfOn, mfTarget, mfCanOpen, mfClipEmpty, mfNoFocus},
}

// legacyPos is the dock item's place for legacyDockItem, which read it off
// the OS.
var legacyPos int

// TestShippedMenusAsDataMatchTheOldBuilders compares every target's menu,
// row by row, with what the builder it replaced made, over every combination
// of the facts that menu reads.
func TestShippedMenusAsDataMatchTheOldBuilders(t *testing.T) {
	m := ctxMenuOS(t, 120, 40)
	legacy := map[string]func(*OS, menuFact) []ContextMenuItem{
		"pane": legacyPane, "selection": legacySelection, "desktop": legacyDesktop,
		"dock": legacyDock, "dock_item": legacyDockItem, "workspace": legacyWorkspace,
		"session": legacySession, "repo": legacyRepo, "machine": legacyMachine, "file": legacyFile,
	}
	for _, target := range []string{"pane", "selection", "desktop", "dock", "dock_item", "workspace", "session", "repo", "machine", "file"} {
		if shippedRows(target) == nil || legacy[target] == nil {
			t.Fatalf("the %s menu is missing from the comparison", target)
		}
	}
	for target, old := range legacy {
		flags := menuFactFlags[target]
		for bits := range 1 << len(flags) {
			var f menuFact
			for i, flag := range flags {
				f |= is(bits&(1<<i) != 0, flag)
			}
			switch target {
			case "repo":
				// The repo menu always heads a repository.
				f |= mfHeadRepo
			case "file":
				// fileRowMenu works these out from the others: a row that names
				// nothing opens nothing and is no target.
				if f.has(mfCanOpen) && !f.has(mfNamed) && !f.has(mfUp) ||
					f.has(mfTarget) && !(f.has(mfOn) && f.has(mfNamed)) {
					continue
				}
			}
			if target == "dock_item" {
				for _, pos := range []int{-1, 0, 1, 8, 9, 12} {
					legacyPos = pos
					want, got := old(m, 0), m.dockItemRows(pos)
					if !reflect.DeepEqual(got, want) {
						t.Errorf("dock_item menu at %d:\n got %s\nwant %s", pos, showItems(got), showItems(want))
					}
				}
				continue
			}
			if want, got := old(m, f), m.menu(target, f); !reflect.DeepEqual(got, want) {
				t.Errorf("%s menu with facts %b:\n got %s\nwant %s", target, f, showItems(got), showItems(want))
			}
		}
	}
}

func (f menuFact) has(b menuFact) bool { return f&b != 0 }

func showItems(items []ContextMenuItem) string {
	s := ""
	for _, it := range items {
		if it.Sep {
			s += "\n  ---"
			continue
		}
		s += fmt.Sprintf("\n  %q %q %q dim=%v warn=%v hint=%q", it.Icon, it.Label, it.Action, it.Dim, it.Warn, it.Hint)
	}
	return s
}
