package main

import (
	"runtime"
	"strings"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/config"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The command palette's keys, and the tuios keymap's actions it lists.
//
// The palette opens on the tuios config's palette keys while tuios keys are
// on: [keybindings.global] command_palette (Ctrl+P by default), as in the
// TUI, and [keybindings.gui] command_palette (Ctrl+Shift+P, which a GUI on
// macOS reads as Cmd+P). The menu's Command palette item (palette.open)
// opens it too, and on macOS Cmd+Shift+P, as in editors. Cmd+K is not one:
// it clears the terminal on macOS, and Ctrl+K is the shell's on Linux.

// keyChord is a key with the modifiers held for it.
type keyChord struct {
	mods ui.Modifiers
	key  ui.Key
}

// paletteChords reads the palette keys of one section of the tuios
// keymap. gui says the section is [keybindings.gui], whose Ctrl+Shift a
// GUI on macOS reads as Cmd.
func paletteChords(keys []string, gui bool) []keyChord {
	var out []keyChord
	for _, k := range keys {
		mods, key, ok := leaderChord(normKey(k))
		if !ok || mods == 0 {
			continue // a bare key would type nowhere else
		}
		if gui && runtime.GOOS == "darwin" && mods&(ui.Ctrl|ui.Shift) == ui.Ctrl|ui.Shift {
			mods = mods&^(ui.Ctrl|ui.Shift) | ui.Super
		}
		out = append(out, keyChord{mods, key})
	}
	return out
}

// paletteShortcuts opens the palette on its keys. The window's shortcuts
// see a key before the terminal does, and take only these chords: every
// other key reaches the pane.
func (a *App) paletteShortcuts(c *ui.Context) {
	open := false
	if a.keysOn() && !a.tk.live() {
		for _, k := range a.tk.palette {
			open = c.Shortcut(k.mods, k.key) || open
		}
	}
	if runtime.GOOS == "darwin" {
		open = c.Shortcut(ui.Super|ui.Shift, ui.KeyP) || open
	}
	if open {
		a.openPalette()
	}
}

// keyRow is a tuios keymap action the palette lists with the keys the
// config gives it under the prefix. kero names the guios action it is the
// same as, whose row then shows those keys too; else title names its own
// row, which runs the action as the prefix does. tui says only a tuios TUI
// on the session runs it.
type keyRow struct {
	action string
	kero   string
	title  func() string
	tui    bool
}

// keyRows are the keymap's actions guios runs, in the order the palette
// lists their own rows. The numbered ones (workspaces, panes) are added by
// number.
var keyRows = []keyRow{
	{action: "prefix_new_window", kero: "pane.new"},
	{action: "prefix_split_vertical", kero: "pane.split.right"},
	{action: "prefix_split_horizontal", kero: "pane.split.down"},
	{action: "prefix_close_window", kero: "pane.close"},
	{action: "prefix_next_window", kero: "pane.next"},
	{action: "prefix_prev_window", kero: "pane.previous"},
	{action: "terminal_focus_left", kero: "pane.left"},
	{action: "terminal_focus_right", kero: "pane.right"},
	{action: "terminal_focus_up", kero: "pane.up"},
	{action: "terminal_focus_down", kero: "pane.down"},
	{action: "prefix_fullscreen", kero: "pane.zoom"},
	{action: "prev_session", kero: "session.previous"},
	{action: "next_session", kero: "session.next"},
	{action: "prefix_close_session", kero: "session.close"},
	{action: "new_worktree", kero: "session.newWorktree"},
	{action: "prefix_settings", kero: "settings.open"},
	{action: "prefix_file_search", kero: "files.toggle"},
	{action: "prefix_review", kero: "changes.toggle"},
	{action: "prefix_inbox", kero: "agents.toggle"},
	{action: "prefix_next_attention", kero: "session.attention"},
	{action: "prefix_toggle_sidebar", kero: "sidebar.toggle"},
	{action: "new_workspace", kero: "workspace.new"},
	{action: "next_workspace", kero: "workspace.next"},
	{action: "prev_workspace", kero: "workspace.previous"},
	// The layouts, which guios lays out itself (layoutKeys).
	{action: "prefix_toggle_tiling", kero: "layout.tiling"},
	{action: "cycle_layout", kero: "layout.next"},
	{action: "prefix_rotate_split", kero: "layout.rotate"},
	{action: "prefix_equalize_splits", kero: "layout.equalize"},
	{action: "window_prefix_float", kero: "layout.float"},
	{action: "cycle_master_position", kero: "layout.master.position"},
	{action: "swap_with_master", kero: "layout.master.swap"},
	{action: "focus_master", kero: "layout.master.focus"},
	{action: "add_master", kero: "layout.master.add"},
	{action: "remove_master", kero: "layout.master.remove"},
	{action: "scroll_cycle_width", kero: "layout.scroll.width"},
	{action: "scroll_maximize", kero: "layout.scroll.max"},
	{action: "scroll_consume", kero: "layout.scroll.consume"},
	{action: "scroll_expel", kero: "layout.scroll.expel"},
	{action: "scroll_move_left", kero: "layout.scroll.moveLeft"},
	{action: "scroll_move_right", kero: "layout.scroll.moveRight"},
	{action: "snap_fullscreen", kero: "layout.snap.full"},
	{action: "unsnap", kero: "layout.snap.unsnap"},
	{action: "minimize_prefix_focused", title: locales.PaletteMinimizePane},
	{action: "minimize_prefix_restore_all", title: locales.PaletteRestorePanes},
	{action: "close_workspace", title: locales.PaletteCloseWorkspace, tui: true},
	{action: "last_workspace", title: locales.PaletteLastWorkspace, tui: true},
}

func init() {
	for n := 1; n <= 9; n++ {
		keyRows = append(keyRows,
			keyRow{action: "workspace_prefix_switch_" + itoa(n), kero: "workspace." + itoa(n)},
			keyRow{action: "workspace_prefix_move_" + itoa(n), kero: "pane.workspace." + itoa(n)})
	}
}

// prefixKeys is what to press for a keymap action under the prefix, as
// "Ctrl+B |"; "" while tuios keys are off, or for an action the config
// gives no prefix key.
func (a *App) prefixKeys(action string) string {
	tk := &a.tk
	if !a.keysOn() || tk.reg == nil {
		return ""
	}
	for _, p := range config.PressesByAction(tk.reg)[action] {
		if rest, ok := strings.CutPrefix(p, tk.leader+" "); ok {
			return pressLabel(tk.leader, rest)
		}
	}
	return ""
}

// pressLabel shows the leader and the keys after it as guios's menus show
// keys: "Ctrl+B |", "Ctrl+B w 1".
func pressLabel(leader, rest string) string {
	parts := []string{chordLabel(leader)}
	for _, k := range strings.Fields(rest) {
		parts = append(parts, chordLabel(k))
	}
	return strings.Join(parts, " ")
}
