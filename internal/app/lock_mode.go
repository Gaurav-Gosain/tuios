package app

import "github.com/Gaurav-Gosain/tuios/internal/config"

// MenuState is which leader menu waits for a key: the leader itself, one of
// its sub-prefixes, or a declared menu.
type MenuState struct {
	Prefix bool
	Sub    SubMenu
	Menu   string
	// Origin is the overlay a declared menu was opened from, when one was
	// (issue #1590): its keys run that overlay's own actions.
	Origin MenuOrigin
}

// MenuHandback is an action a menu hands back to the overlay table it was
// opened from, for one key. The zero value is none.
type MenuHandback struct {
	Set    bool
	Table  config.Table
	Action string
}

// MenuOrigin names the keybinding table of the overlay a declared menu was
// opened from, such as the Inbox or the rail. The zero value is none: the
// menu was opened from the leader, a mode or another menu.
type MenuOrigin struct {
	Set   bool
	Table config.Table
}

// SubMenu names a leader sub-menu. At most one is open at a time, so one value
// holds which, where seven flags could say two were open.
type SubMenu uint8

// The leader sub-menus.
const (
	SubMenuNone SubMenu = iota
	SubMenuWorkspace
	SubMenuMinimize
	SubMenuTiling
	SubMenuDebug
	SubMenuTape
	SubMenuTuios
	SubMenuLayout
	numSubMenus
)

var subMenus = [numSubMenus]struct{ title, name string }{
	SubMenuWorkspace: {"Workspace", "workspace"},
	SubMenuMinimize:  {"Minimize", "minimize"},
	SubMenuTiling:    {"Window", "window"},
	SubMenuDebug:     {"Debug", "debug"},
	SubMenuTape:      {"Tape", "tape"},
	SubMenuTuios:     {"tuios", "tuios"},
	SubMenuLayout:    {"Layout", "layout"},
}

// MenuState returns the leader menu that waits for a key now.
func (m *OS) MenuState() MenuState {
	return MenuState{Prefix: m.PrefixActive, Sub: m.SubMenu, Menu: m.Menu, Origin: m.menuOrigin}
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

func (m *OS) lockPillShown() bool {
	return m.Settings.Lock && m.Mode == TerminalMode
}

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
