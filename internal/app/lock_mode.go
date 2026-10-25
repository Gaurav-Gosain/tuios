package app

import "github.com/Gaurav-Gosain/tuios/internal/config"

// Lock mode: zellij's locked mode, as keybindings.lock.
//
// With lock on, terminal mode gives the focused pane every key but the
// leader. The terminal_mode keys, the global keys, the workspace chords and
// the hold key all go to the pane as bytes. The leader is the unlock key: it
// opens the leader menu as it always does, one action runs, the menu closes,
// and with it closed the client is locked again. There is no lock flag to
// set and clear. Locked is derived from the menus, so every path that closes
// a menu also locks.
//
// The one state of its own is LockHeld. A repeatable action (keybindings.repeat)
// run from a menu keeps that menu open until esc, so a focus or resize move
// can be pressed again and again. A held menu takes only the repeatable
// actions, as tmux's -r does. Any other key closes it, the client locks again,
// and the key goes to the pane, so the menu's other keys (close, kill, quit)
// never stay live behind a run of moves. That replaces the timed repeat window
// while lock is on: zellij's modes stay until esc, and a deadline would relock
// in the middle of a run of moves.

// MenuState is which leader menu waits for a key: the leader itself, one of
// its sub-prefixes, or a declared menu.
type MenuState struct {
	Prefix, Workspace, Minimize, Tiling, Debug, Tape, Layout, Tuios bool
	Menu                                                            string
}

// MenuState returns the leader menu that waits for a key now.
func (m *OS) MenuState() MenuState {
	return MenuState{
		Prefix:    m.PrefixActive,
		Workspace: m.WorkspacePrefixActive,
		Minimize:  m.MinimizePrefixActive,
		Tiling:    m.TilingPrefixActive,
		Debug:     m.DebugPrefixActive,
		Tape:      m.TapePrefixActive,
		Layout:    m.LayoutPrefixActive,
		Tuios:     m.TuiosPrefixActive,
		Menu:      m.Menu,
	}
}

// SetMenuState opens the menu s names, and closes every other. The zero
// MenuState closes them all.
func (m *OS) SetMenuState(s MenuState) {
	m.PrefixActive = s.Prefix
	m.WorkspacePrefixActive = s.Workspace
	m.MinimizePrefixActive = s.Minimize
	m.TilingPrefixActive = s.Tiling
	m.DebugPrefixActive = s.Debug
	m.TapePrefixActive = s.Tape
	m.LayoutPrefixActive = s.Layout
	m.TuiosPrefixActive = s.Tuios
	m.Menu = s.Menu
}

// MenuOpen reports whether the leader, or one of its menus, waits for a key.
func (m *OS) MenuOpen() bool { return m.prefixArmed() }

// Locked reports whether lock mode gives every key but the leader to the
// focused pane: keybindings.lock is on, the client is in terminal mode, and
// no leader menu is open. Nest mode is locked too, and gives the pane the
// leader as well. See nest_mode.go.
func (m *OS) Locked() bool {
	return (m.Settings.Lock || m.nest) && m.Mode == TerminalMode && !m.prefixArmed()
}

// lockPillShown reports whether the dock's mode pill carries the lock state:
// lock is on and the client is in terminal mode, where it applies.
func (m *OS) lockPillShown() bool {
	return m.Settings.Lock && m.Mode == TerminalMode
}

// heldWhichKey is the which-key panel of a menu lock mode holds open. The
// held menu takes only repeatable actions, so the panel lists only those
// rows, and says that esc closes it and any other key goes to the pane. A
// row stands for the first action it was built from (Keybinding.Action).
func (m *OS) heldWhichKey(title string, groups []config.KeybindingGroup) (string, []config.KeybindingGroup) {
	var out []config.KeybindingGroup
	for _, g := range groups {
		var rows []config.Keybinding
		for _, b := range g.Bindings {
			if !b.Submenu && m.KeybindRegistry != nil && m.KeybindRegistry.PrefixRepeats(b.Action) {
				rows = append(rows, b)
			}
		}
		if len(rows) > 0 {
			out = append(out, config.KeybindingGroup{Title: g.Title, Bindings: rows})
		}
	}
	out = append(out, config.KeybindingGroup{Title: "Close", Bindings: []config.Keybinding{
		{Key: "esc", Description: "Close and lock", Action: "lock_close"},
		{Key: "other", Description: "Close, lock and type", Action: "lock_type"},
	}})
	return title + " (repeat)", out
}
