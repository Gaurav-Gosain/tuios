package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// ArmLeader opens the leader menu, with no sub-menu or declared menu, and
// starts the which-key wait.
func (m *OS) ArmLeader() {
	m.setMenus(MenuState{Prefix: true}, true, "leader")
}

// OpenSubMenu opens sub-menu s of the leader and starts the which-key wait
// again, so the chord gets a fresh timeout.
func (m *OS) OpenSubMenu(s SubMenu) {
	m.setMenus(MenuState{Prefix: true, Sub: s}, true, "sub-menu")
}

// OpenMenu opens the declared menu name.
func (m *OS) OpenMenu(name string) {
	m.setMenus(MenuState{Prefix: true, Menu: name}, true, "menu")
}

// OpenMenuFrom opens the declared menu name over the overlay whose table is
// origin, so its keys run that overlay's actions (issue #1590). hold keeps
// the which-key time, for a sticky menu after its key ran.
func (m *OS) OpenMenuFrom(name string, origin config.Table, hold bool) {
	m.setMenus(MenuState{Prefix: true, Menu: name, Origin: MenuOrigin{Set: true, Table: origin}}, !hold, "menu over an overlay")
}

// HoldMenu keeps the sticky menu name open after its key ran. The which-key
// time stays as it was: a menu that stays open stays shown, with no new wait.
func (m *OS) HoldMenu(name string) {
	m.setMenus(MenuState{Prefix: true, Menu: name}, false, "sticky menu")
}

// KeepLeaderPending keeps the leader armed after a key it could not read, so
// the key can be typed again. An open sub-menu stays open with it, on
// purpose.
func (m *OS) KeepLeaderPending() {
	s := m.MenuState()
	s.Prefix = true
	m.setMenus(s, false, "key typed again")
}

// CloseMenus closes the leader and every menu.
func (m *OS) CloseMenus() {
	m.setMenus(MenuState{}, false, "close")
}

// SetMenuState opens the menu s names, and closes every other. The zero
// MenuState closes them all. Lock mode uses it to put back a whole state.
func (m *OS) SetMenuState(s MenuState) {
	m.setMenus(s, false, "lock mode")
}

func (m *OS) setMenus(s MenuState, arm bool, cause string) {
	if was := m.MenuState(); was != s && verboseLog() {
		m.LogInfo("menu: %s to %s (%s)", menuStateName(was), menuStateName(s), cause)
	}
	m.PrefixActive, m.SubMenu, m.Menu, m.menuOrigin = s.Prefix, s.Sub, s.Menu, s.Origin
	if arm {
		m.LastPrefixTime = time.Now()
	}
}

func menuStateName(s MenuState) string {
	switch {
	case s.Menu != "":
		return "menu " + s.Menu
	case s.Sub != SubMenuNone:
		return "sub-menu " + subMenus[s.Sub].name
	case s.Prefix:
		return "leader"
	}
	return "closed"
}
