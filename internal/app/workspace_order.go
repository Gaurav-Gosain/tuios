package app

import (
	tea "charm.land/bubbletea/v2"
)

// The dock's workspace pills can be dragged into any arrangement, and what that
// arrangement is has one careful answer.
//
// A workspace's number is its identity. It is what the window's Workspace field
// carries, what WorkspaceFocus and WorkspaceTrees are keyed by, what leader+1..9
// and opt+1..9 press, what every verb takes, what TUIOS_WORKSPACE exports to a
// hook, and what a resurrected session comes back addressed by. Renumbering on a
// drag would have to move all of that at once and would break every one of those
// addresses for a gesture that meant "put this one first".
//
// So the drag sets a display order and nothing else, which is the same kind of
// thing naming a workspace already is: the dock has shown a workspace's name
// rather than its number since names existed, so what the pill says and what the
// key presses have been separate for as long as there has been anything to say.
// The order is one more choice of that kind, and it is held to the rule the name
// is: it is daemon-owned and session-scoped, so every client attached to a
// session sees the one arrangement, and it is applied at exactly one place, so
// no two surfaces can disagree about it.
//
// That one place is workspaceDisplayOrder. The dock strip, the workspace
// switcher and the rail's terminals section all arrange through it, which is
// what stops a reorder from being visible in the dock and invisible everywhere
// else. Nothing that addresses a workspace goes near it.

// dockWorkspaceDragState is the click-or-drag gesture on a dock workspace pill,
// modelled on the rail's session reorder because it is the same gesture: a left
// press arms it, motion onto another pill turns it into a drag whose draft Order
// the strip draws live, and the release either commits the draft or performs the
// plain switch the press deferred.
//
// The press cannot switch on its own the way it used to. A switch retiles the
// panes and moves the pills, so a drag that began with one would be dragging a
// pill that had already left under the pointer.
type dockWorkspaceDragState struct {
	PressActive bool
	Workspace   int
	PressX      int
	PressY      int
	Dragging    bool
	// Order is the whole displayed arrangement, not just the pills on screen, so
	// a drag inside a scrolled strip cannot drop the workspaces either side of
	// the viewport.
	Order []int
}

// workspaceRank is the position the display order puts workspace ws in, and a
// number past every ranked one for a workspace the order does not mention.
//
// An unranked workspace sorting last is the rail's rule for an undragged
// session, and it is here for the same reason: a workspace made after the
// arrangement was set appends where the numbering put it rather than pushing
// into the middle of an order the user chose.
func (m *OS) workspaceRank(ws int) int {
	for i, n := range m.WorkspaceOrder {
		if n == ws {
			return i
		}
	}
	return len(m.WorkspaceOrder) + ws
}

// workspaceDisplayOrder arranges workspace numbers the way this session shows
// them. It is the one place the order is applied, and it is a no-op for a
// session that has never been rearranged, which is nearly all of them.
func (m *OS) workspaceDisplayOrder(ws []int) []int {
	order := m.WorkspaceOrder
	if m.dockWorkspaceDrag.Dragging {
		order = m.dockWorkspaceDrag.Order
	}
	if len(order) == 0 || len(ws) < 2 {
		return ws
	}
	out := make([]int, 0, len(ws))
	for _, n := range order {
		for _, have := range ws {
			if have == n {
				out = append(out, n)
				break
			}
		}
	}
	// Whatever the order did not mention keeps its numeric place after the rest.
	for _, have := range ws {
		if !containsInt(out, have) {
			out = append(out, have)
		}
	}
	return out
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// BeginDockWorkspaceDrag arms the gesture on the pill at (x, y), reporting
// whether one was there. The switch it might turn out to be is delivered on the
// release.
func (m *OS) BeginDockWorkspaceDrag(x, y int) bool {
	ws := m.DockWorkspacePillAt(x, y)
	if ws <= 0 {
		return false
	}
	m.dockWorkspaceDrag = dockWorkspaceDragState{
		PressActive: true,
		Workspace:   ws,
		PressX:      x,
		PressY:      y,
	}
	return true
}

// DockWorkspaceDragActive reports whether a pill press or drag is in progress,
// so the motion and release handlers route to the strip first.
func (m *OS) DockWorkspaceDragActive() bool {
	return m.dockWorkspaceDrag.PressActive || m.dockWorkspaceDrag.Dragging
}

// DockWorkspaceDragMotion advances the gesture. The first step onto a different
// column commits it to a drag; from then on the dragged workspace follows the
// pill under the pointer in a draft the strip draws live.
//
// The pill under the pointer is read out of the rectangles the renderer recorded
// as it drew, which is the only copy of the strip's geometry there is. Nothing
// here recomputes where a pill landed.
func (m *OS) DockWorkspaceDragMotion(x, y int) bool {
	d := &m.dockWorkspaceDrag
	if !d.PressActive && !d.Dragging {
		return false
	}
	if !d.Dragging {
		if x == d.PressX {
			return true // vertical jitter in a one-row strip is still a click
		}
		d.Dragging = true
		d.Order = m.workspaceDisplayOrder(m.occupiedWorkspaceNumbers())
	}

	target := m.DockWorkspacePillAt(x, y)
	if target <= 0 || target == d.Workspace {
		return true
	}
	from, to := -1, -1
	for i, n := range d.Order {
		if n == d.Workspace {
			from = i
		}
		if n == target {
			to = i
		}
	}
	if from < 0 || to < 0 || to == from {
		return true
	}
	n := d.Order[from]
	d.Order = append(d.Order[:from], d.Order[from+1:]...)
	// to was read before the removal, so afterwards the dragged workspace lands
	// past the target when moving right and before it when moving left: the
	// pills swap as the pointer crosses them.
	at := min(to, len(d.Order))
	d.Order = append(d.Order[:at], append([]int{n}, d.Order[at:]...)...)
	return true
}

// DockWorkspaceDragRelease finishes the gesture: a drag commits its draft and
// sends it to the daemon, and a release that never left the pill performs the
// switch the press deferred.
func (m *OS) DockWorkspaceDragRelease(x, y int) (bool, tea.Cmd) {
	d := m.dockWorkspaceDrag
	if !d.PressActive && !d.Dragging {
		return false, nil
	}
	m.dockWorkspaceDrag = dockWorkspaceDragState{}

	if d.Dragging {
		// Applied here as well as sent, so the strip keeps the arrangement the
		// pointer just built rather than snapping back to the old one and jumping
		// again when the daemon's push lands.
		m.WorkspaceOrder = d.Order
		return true, setWorkspaceOrderCmd(m.SessionName, d.Order)
	}
	if ws := m.DockWorkspacePillAt(x, y); ws == d.Workspace {
		m.SwitchToWorkspace(ws)
	}
	return true, nil
}

// setWorkspaceOrderCmd writes the arrangement through the daemon, which owns it
// for the same reason it owns the workspace names: it has to survive a reattach
// and reach every other client attached to the session.
func setWorkspaceOrderCmd(sessionName string, order []int) tea.Cmd {
	if sessionName == "" || len(order) == 0 {
		return nil
	}
	return labelVerbCmd("Arrange", "set-workspace-order", map[string]any{
		"session": sessionName, "order": order,
	})
}
