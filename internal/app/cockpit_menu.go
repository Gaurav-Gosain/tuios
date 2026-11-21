package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
)

type ckMenu struct {
	id    string
	dd    *dropdown
	picks []func(m *OS) tea.Cmd
}

type ckAnchor struct{ x, y, w int }

var ckMenuControls = map[string]bool{"board": true, "group": true, "scope": true, "talked": true}

// CockpitMenuOpen reports whether a header menu is open.
func (m *OS) CockpitMenuOpen() bool { return m.Inbox.ck.menu != nil }

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
	case "talked":
		e, ok := m.cockpitAgent()
		if !ok {
			break
		}
		for _, tp := range m.ckTalkedWith(e)[ck.talkShown:] {
			note := plural.Count(tp.n, "message")
			if tp.peer.closed {
				note += sepWord() + "closed"
			}
			window := tp.window
			add(ddItem{label: tp.peer.name, note: note, muted: tp.peer.key == ""}, func(m *OS) tea.Cmd { return m.cockpitTalkClick(window) })
		}
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
	i, what := mn.dd.Key(key, true, m.ListKeys)
	switch what {
	case ddKeyPicked:
		return m.cockpitMenuPick(i)
	case ddKeyClosed:
		m.Inbox.ck.menu = nil
	}
	m.cockpitTouch()
	return nil
}

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

var agentsSortNames = [agentsSortCount]string{"needs you", "name", "recent", "session"}

func (m *OS) inboxAgentsSortName() string {
	return agentsSortNames[m.Inbox.agents.sort%agentsSortCount]
}

func (m *OS) cockpitMenuHints() []overlay.Hint {
	return m.listHints([]overlay.Hint{
		{Key: hintListMoveLetters, Label: "move"},
		{Key: overlay.EnterKey(), Label: "choose"},
		{Key: "esc", Label: "close", Priority: overlay.HintEssential},
	})
}
