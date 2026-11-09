package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The agents view's header menus. The Board, Group and machine controls each
// open the TUI's one dropdown (dropdown.go), the list the New agent sheet's
// fields open, under the control. s opens the Group menu.
//
// A menu is a one-key layer (CockpitLayerMenu), so the key router, the key
// bar and the click handler find it through CockpitTop like the others. It is
// drawn last, over the whole view, at the place the control was drawn.

// ckMenu is the open header menu: which control it hangs from, the list, and
// what each row does.
type ckMenu struct {
	id    string
	dd    *dropdown
	picks []func(m *OS) tea.Cmd
}

// ckAnchor is where a control was drawn, in canvas cells, for its menu.
type ckAnchor struct{ x, y, w int }

// ckMenuControls are the header controls that open a menu, by action.
var ckMenuControls = map[string]bool{"board": true, "group": true, "scope": true}

// CockpitMenuOpen reports whether a header menu is open.
func (m *OS) CockpitMenuOpen() bool { return m.Inbox.ck.menu != nil }

// cockpitOpenMenu opens the menu of the control action, or closes it when it
// is the one already open.
func (m *OS) cockpitOpenMenu(action string) {
	ck := &m.Inbox.ck
	defer m.cockpitTouch()
	if ck.menu != nil && ck.menu.id == action {
		ck.menu = nil
		return
	}
	items, picks := m.cockpitMenuItems(action)
	if len(items) == 0 {
		ck.menu = nil
		return
	}
	ck.menu = &ckMenu{id: action, dd: newDropdown(items), picks: picks}
}

// cockpitMenuItems are the rows of the control action's menu, and what each
// one does.
func (m *OS) cockpitMenuItems(action string) ([]ddItem, []func(m *OS) tea.Cmd) {
	ck := &m.Inbox.ck
	var items []ddItem
	var picks []func(m *OS) tea.Cmd
	add := func(it ddItem, pick func(m *OS) tea.Cmd) {
		items, picks = append(items, it), append(picks, pick)
	}
	switch action {
	case "board":
		add(ddItem{label: "Agents", note: "the list by state", current: !ck.board}, func(m *OS) tea.Cmd {
			if m.Inbox.ck.board {
				return m.CockpitToggleBoard()
			}
			return nil
		})
		add(ddItem{label: "Board", note: "the task cards", current: ck.board}, func(m *OS) tea.Cmd {
			if !m.Inbox.ck.board {
				return m.CockpitToggleBoard()
			}
			return nil
		})
	case "group":
		by := m.ckGroupBy()
		for i, name := range ckGroupingNames {
			g := ckGroupings[i]
			add(ddItem{label: name, current: g == by}, func(m *OS) tea.Cmd {
				m.cockpitSetGrouping(g)
				return nil
			})
		}
		add(ddItem{label: "Sort", note: m.inboxAgentsSortName(), key: "s"}, func(m *OS) tea.Cmd {
			m.InboxAgentsCycleSort()
			m.cockpitTouchRows()
			return nil
		})
	case "scope":
		scope := m.ckScope()
		one := func(label, host string) {
			add(ddItem{label: label, current: scope == host}, func(m *OS) tea.Cmd {
				m.CockpitScope(host)
				return nil
			})
		}
		one("All machines", "")
		one(m.ckMachine(""), federation.LocalHostName)
		for _, h := range m.FederationHosts {
			if h.Name != federation.LocalHostName {
				one(ckHostLabel(h.Name), h.Name)
			}
		}
	}
	return items, picks
}

// cockpitMenuPick runs row i and closes the menu.
func (m *OS) cockpitMenuPick(i int) tea.Cmd {
	ck := &m.Inbox.ck
	mn := ck.menu
	ck.menu = nil
	m.cockpitTouch()
	if mn == nil || i < 0 || i >= len(mn.picks) || mn.picks[i] == nil {
		return nil
	}
	return mn.picks[i](m)
}

// CockpitMenuKey takes a key while a menu is open. A key the list does not
// take closes the menu, as any other one-key layer.
func (m *OS) CockpitMenuKey(key string) tea.Cmd {
	mn := m.Inbox.ck.menu
	if mn == nil {
		return nil
	}
	i, what := mn.dd.Key(key, true)
	switch what {
	case ddKeyPicked:
		return m.cockpitMenuPick(i)
	case ddKeyClosed:
		m.Inbox.ck.menu = nil
	}
	m.cockpitTouch()
	return nil
}

// cockpitMenuClick takes a click while a menu is open, and reports whether it
// spent it: a row picks, and the rest of the box does nothing. A click outside
// is left to the one-key dismissal, which closes the menu and does nothing
// else, unless it is on the control that opened the menu, which closes it.
func (m *OS) cockpitMenuClick(x, y int) (tea.Cmd, bool) {
	mn := m.Inbox.ck.menu
	if mn == nil {
		return nil, false
	}
	row, inside := mn.dd.RowAt(x, y)
	if !inside {
		return nil, false
	}
	if row >= 0 {
		return m.cockpitMenuPick(row), true
	}
	return nil, true
}

// cockpitMenuHover lights the row under the pointer, and reports whether the
// pointer is over the menu.
func (m *OS) cockpitMenuHover(x, y int) bool {
	mn := m.Inbox.ck.menu
	if mn == nil {
		return false
	}
	if mn.dd.Hover(x, y) {
		m.cockpitTouch()
	}
	_, inside := mn.dd.RowAt(x, y)
	return inside
}

// drawCockpitMenu draws the open menu under its control, over everything the
// view drew. top is the canvas's first row on the screen.
func (m *OS) drawCockpitMenu(c *ckCanvas, l ckLook, top int) {
	ck := &m.Inbox.ck
	mn := ck.menu
	if mn == nil {
		return
	}
	a, ok := ck.anchors[mn.id]
	if !ok {
		ck.menu = nil
		return
	}
	drawDropdown(c, l, mn.dd, a.x-2, a.y, a.w+4, 0, c.h, top)
}

// agentsSortNames name the sorts, in agentsSort order.
var agentsSortNames = [agentsSortCount]string{"needs you", "name", "recent", "session"}

// inboxAgentsSortName is the sort in force, as the Group menu names it.
func (m *OS) inboxAgentsSortName() string {
	return agentsSortNames[m.Inbox.agents.sort%agentsSortCount]
}

// cockpitMenuHints are the menu's keys for the key bar.
func (m *OS) cockpitMenuHints() []overlay.Hint {
	move := "↑↓"
	if overlay.UseASCII() {
		move = "up/down"
	}
	return []overlay.Hint{
		{Key: move, Label: "move"},
		{Key: overlay.EnterKey(), Label: "choose"},
		{Key: "esc", Label: "close", Priority: overlay.HintEssential},
	}
}
