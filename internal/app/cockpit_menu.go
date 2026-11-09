package app

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The agents view's dropdowns. The header's Board, Group and machine controls
// end in a down mark, and a click on one opens a menu under it: its choices,
// the current one marked, each with the key that picks it. The pointer
// lights the row under it and a click picks it, up and down move the cursor
// and enter picks, a choice's own key picks it, and esc, a key the menu does
// not take, or a click outside it closes it and does nothing else. s opens the
// Group menu, as it opened the one-key group picker the menu replaces.
//
// The menu is a one-key layer (CockpitLayerMenu), so the key router, the key
// bar and the click handler find it through CockpitTop like the others. It is
// drawn last, over the whole view, at the place the control was drawn.

// ckMenuItem is one choice of a menu.
type ckMenuItem struct {
	label, key string
	current    bool
	pick       func(m *OS) tea.Cmd
}

// ckMenu is the open dropdown: which control it hangs from, its choices, the
// row under the cursor and under the pointer, and where it was last drawn, in
// screen cells, for the clicks.
type ckMenu struct {
	id     string
	items  []ckMenuItem
	cursor int
	hover  int
	// x0, y0, x1, y1 is the box as last drawn, in screen cells.
	x0, y0, x1, y1 int
	rowY0          int
}

// ckAnchor is where a control was drawn, in canvas cells, for its menu.
type ckAnchor struct{ x, y, w int }

// ckMenuControls are the header controls that open a menu, by action.
var ckMenuControls = map[string]bool{"board": true, "group": true, "scope": true}

// CockpitMenuOpen reports whether a dropdown is open.
func (m *OS) CockpitMenuOpen() bool { return m.Inbox.ck.menu != nil }

// cockpitOpenMenu opens the dropdown of the control action, or closes it
// when it is the one already open.
func (m *OS) cockpitOpenMenu(action string) {
	ck := &m.Inbox.ck
	defer m.cockpitTouch()
	if ck.menu != nil && ck.menu.id == action {
		ck.menu = nil
		return
	}
	items := m.cockpitMenuItems(action)
	if len(items) == 0 {
		ck.menu = nil
		return
	}
	cur := 0
	for i, it := range items {
		if it.current {
			cur = i
		}
	}
	ck.menu = &ckMenu{id: action, items: items, cursor: cur, hover: -1}
}

// cockpitMenuItems are the choices of the control action's menu.
func (m *OS) cockpitMenuItems(action string) []ckMenuItem {
	ck := &m.Inbox.ck
	var items []ckMenuItem
	switch action {
	case "board":
		items = []ckMenuItem{
			{label: "Agents", current: !ck.board, pick: func(m *OS) tea.Cmd {
				if m.Inbox.ck.board {
					return m.CockpitToggleBoard()
				}
				return nil
			}},
			{label: "Board", current: ck.board, pick: func(m *OS) tea.Cmd {
				if !m.Inbox.ck.board {
					return m.CockpitToggleBoard()
				}
				return nil
			}},
		}
	case "group":
		by := m.ckGroupBy()
		for i, name := range ckGroupingNames {
			g := ckGroupings[i]
			items = append(items, ckMenuItem{label: name, current: g == by, pick: func(m *OS) tea.Cmd {
				m.cockpitSetGrouping(g)
				return nil
			}})
		}
		items = append(items, ckMenuItem{label: "Sort: " + m.inboxAgentsSortName(), key: "s", pick: func(m *OS) tea.Cmd {
			m.InboxAgentsCycleSort()
			m.cockpitTouchRows()
			return nil
		}})
	case "scope":
		scope := m.ckScope()
		add := func(label, host string) {
			items = append(items, ckMenuItem{label: label, current: scope == host, pick: func(m *OS) tea.Cmd {
				m.CockpitScope(host)
				return nil
			}})
		}
		add("All machines", "")
		add(m.ckMachine(""), federation.LocalHostName)
		for _, h := range m.FederationHosts {
			if h.Name != federation.LocalHostName {
				add(ckHostLabel(h.Name), h.Name)
			}
		}
	}
	// The digits pick in order, up to nine; a choice with a key of its own
	// keeps it.
	n := 0
	for i := range items {
		if items[i].key == "" && n < 9 {
			n++
			items[i].key = strconv.Itoa(n)
		}
	}
	return items
}

// cockpitMenuPick runs choice i and closes the menu.
func (m *OS) cockpitMenuPick(i int) tea.Cmd {
	ck := &m.Inbox.ck
	mn := ck.menu
	ck.menu = nil
	m.cockpitTouch()
	if mn == nil || i < 0 || i >= len(mn.items) || mn.items[i].pick == nil {
		return nil
	}
	return mn.items[i].pick(m)
}

// CockpitMenuKey takes a key while a menu is open: up and down move, enter
// picks the cursor's row, a choice's key picks it, and any other key closes
// the menu.
func (m *OS) CockpitMenuKey(key string) tea.Cmd {
	mn := m.Inbox.ck.menu
	if mn == nil {
		return nil
	}
	switch key {
	case "up", "k":
		mn.cursor = (mn.cursor + len(mn.items) - 1) % len(mn.items)
		mn.hover = -1
		m.cockpitTouch()
		return nil
	case "down", "j":
		mn.cursor = (mn.cursor + 1) % len(mn.items)
		mn.hover = -1
		m.cockpitTouch()
		return nil
	case "enter":
		return m.cockpitMenuPick(mn.cursor)
	}
	for i, it := range mn.items {
		if it.key == key {
			return m.cockpitMenuPick(i)
		}
	}
	m.Inbox.ck.menu = nil
	m.cockpitTouch()
	return nil
}

// cockpitMenuRowAt is the row of the open menu at screen cell x, y, and
// whether the cell is inside the menu's box at all.
func (m *OS) cockpitMenuRowAt(x, y int) (row int, inside bool) {
	mn := m.Inbox.ck.menu
	if mn == nil || x < mn.x0 || x >= mn.x1 || y < mn.y0 || y >= mn.y1 {
		return -1, false
	}
	if i := y - mn.rowY0; i >= 0 && i < len(mn.items) {
		return i, true
	}
	return -1, true
}

// cockpitMenuClick takes a click while a menu is open, and reports whether it
// spent it: a row picks, the box's frame does nothing, and the control that
// opened the menu closes it. A click elsewhere is left to the one-key
// dismissal, which closes the menu and does nothing else.
func (m *OS) cockpitMenuClick(x, y int) (tea.Cmd, bool) {
	if m.Inbox.ck.menu == nil {
		return nil, false
	}
	row, inside := m.cockpitMenuRowAt(x, y)
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
	row, inside := m.cockpitMenuRowAt(x, y)
	if row != mn.hover {
		mn.hover = row
		if row >= 0 {
			mn.cursor = row
		}
		m.cockpitTouch()
	}
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
	pal := l.pal
	// Each row: the mark of the current choice, the label, and the key at
	// the right, with a cell of air at each end.
	mark := "✓"
	if l.ascii {
		mark = "*"
	}
	labelW, keyW := 0, 0
	for _, it := range mn.items {
		labelW = max(labelW, ckWidth(ckSeg{t: it.label}))
		keyW = max(keyW, ckWidth(ckSeg{t: it.key}))
	}
	inner := 2 + labelW + 4 + keyW
	w := min(max(inner+4, a.w), c.w)
	h := min(len(mn.items)+2, c.h)
	x := max(min(a.x, c.w-w), 0)
	y := a.y + 1
	if y+h > c.h {
		y = max(a.y-h, 0)
	}
	// The ground fills inside the frame only: a box line sits mid-cell, and
	// a ground under it showed half a cell past the line all round.
	base := pal.Surface
	c.fill(x+1, y+1, w-2, h-2, base)
	spawnFrame(c, l, x, y, w, h)
	for i, it := range mn.items {
		ry := y + 1 + i
		if ry >= y+h-1 {
			break
		}
		st := overlay.RowState{Cursor: i == mn.cursor, Focused: true, Hover: i == mn.hover}
		ground := pal.Ground(st, base)
		c.fill(x+1, ry, w-2, 1, ground)
		fg := pal.Fg
		if !it.current && !st.Cursor {
			fg = pal.FgDim
		}
		lead := "  "
		if it.current {
			lead = mark + " "
		}
		c.text(x+2, ry, x+w-2, ckSeg{t: lead, fg: pal.Accent, bg: ground, attrs: ckBold}, ckSeg{t: it.label, fg: fg, bg: ground})
		c.textR(x+w-3, ry, ckSeg{t: it.key, fg: pal.Accent, bg: ground, attrs: ckBold})
		c.mark(pal, x+1, ry, w-2, 1, st)
	}
	mn.x0, mn.y0, mn.x1, mn.y1 = x, top+y, x+w, top+y+h
	mn.rowY0 = top + y + 1
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
