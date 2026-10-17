package app

import (
	"encoding/json"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The dock's workspace pills can be dragged into any arrangement, and a
// workspace's number is its position: dropping a pill renumbers the workspaces
// to the order the strip shows, the way the move keys do (see
// RenumberWorkspaces). The pill's {index}, opt+N, the verbs and every attached
// client then agree with where it is drawn.
//
// The renumber is the daemon's, so every client attached to the session takes
// the same one. Between the drop and the state that carries it, the strip keeps
// drawing the dropped order (pendingWorkspaceOrder), so the pills do not snap
// back for the length of the round trip. workspaceDisplayOrder is where that
// order and a drag's draft are applied: the dock strip, the workspace switcher
// and the rail's terminals section all arrange through it.

// dockWorkspaceDragState is the click-or-drag gesture on a dock workspace pill,
// modelled on the rail's session reorder because it is the same gesture: a left
// press arms it, motion onto another pill turns it into a drag whose draft Order
// the strip draws live, and the release either commits the draft or performs the
// plain switch the press deferred.
//
// The press cannot switch on its own. A switch retiles the panes and moves the
// pills, so a drag that began with one would be dragging a pill that had
// already left under the pointer.
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
func (m *OS) workspaceRank(ws int) int {
	for i, n := range m.pendingWorkspaceOrder {
		if n == ws {
			return i
		}
	}
	return len(m.pendingWorkspaceOrder) + ws
}

// workspaceDisplayOrder arranges workspace numbers the way this client shows
// them: by number, except while a drag is in flight or its drop is on its way
// to the daemon.
func (m *OS) workspaceDisplayOrder(ws []int) []int {
	order := m.pendingWorkspaceOrder
	if m.dockWorkspaceDrag.Dragging {
		order = m.dockWorkspaceDrag.Order
	}
	if len(order) == 0 || len(ws) < 2 {
		return ws
	}
	out := make([]int, 0, len(ws))
	for _, n := range order {
		if slices.Contains(ws, n) {
			out = append(out, n)
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
	return slices.Contains(xs, x)
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

// DockWorkspaceDragRelease finishes the gesture: a drag renumbers the
// workspaces to its draft, and a release that never left the pill performs the
// switch the press deferred.
func (m *OS) DockWorkspaceDragRelease(x, y int) (bool, tea.Cmd) {
	d := m.dockWorkspaceDrag
	if !d.PressActive && !d.Dragging {
		return false, nil
	}
	m.dockWorkspaceDrag = dockWorkspaceDragState{}

	if d.Dragging {
		if slices.IsSorted(d.Order) {
			return true, nil
		}
		// Drawn until the renumber lands, so the strip keeps the arrangement
		// the pointer just built rather than snapping back to the old one and
		// jumping again when the daemon's state arrives.
		cmd := m.RenumberWorkspaces(d.Order)
		if cmd != nil {
			m.pendingWorkspaceOrder = d.Order
		}
		return true, cmd
	}
	if ws := m.DockWorkspacePillAt(x, y); ws == d.Workspace {
		m.SwitchToWorkspace(ws)
	}
	return true, nil
}

// WorkspaceRenumberedMsg is the daemon's answer to a renumber. Legacy says the
// daemon is from a build that only recorded a display order, so the client
// renumbers its own state and pushes it, as clients did before the daemon
// renumbered.
type WorkspaceRenumberedMsg struct {
	Order  []int
	Legacy bool
	// Moved says the daemon renumbered something, so a state carrying the
	// renumber is on its way.
	Moved bool
	Err   error
}

// setWorkspaceOrderCmd asks the daemon to renumber the session's workspaces
// to order. The daemon owns the numbering for the same reason it owns the
// workspace names: it has to survive a reattach and reach every other client
// attached to the session. The call is made from a command, never from
// Update, for the reason labelVerbCmd gives.
func setWorkspaceOrderCmd(dial agentMailDial, sessionName string, order []int) tea.Cmd {
	if sessionName == "" || len(order) == 0 {
		return nil
	}
	return func() tea.Msg {
		c, err := dial()
		if err != nil {
			return WorkspaceRenumberedMsg{Order: order, Err: err}
		}
		defer func() { _ = c.Close() }()
		raw, err := c.Call("set-workspace-order", map[string]any{"session": sessionName, "order": order})
		if err != nil {
			if stale := session.StaleDaemonError("set-workspace-order", err); stale != nil {
				err = stale
			}
			return WorkspaceRenumberedMsg{Order: order, Err: err}
		}
		var res struct {
			Renumbered map[string]int `json:"renumbered"`
		}
		_ = json.Unmarshal(raw, &res)
		return WorkspaceRenumberedMsg{Order: order, Legacy: res.Renumbered == nil, Moved: len(res.Renumbered) > 0}
	}
}

// handleWorkspaceRenumbered settles a renumber the daemon answered. A failed
// one leaves the pills where they were. A daemon too old to renumber took the
// order as a display order and moved nothing, so the client moves its own
// workspaces and pushes them, which that daemon takes.
//
// That daemon also stored the order as a display order, which its other
// clients would apply on top of the numbering, and which a later daemon would
// apply again on restore. Sending the same workspaces in ascending order is
// how such a daemon clears it.
func (m *OS) handleWorkspaceRenumbered(msg WorkspaceRenumberedMsg) tea.Cmd {
	if msg.Err != nil {
		m.pendingWorkspaceOrder = nil
		m.workspaceMoveInFlight = false
		m.ShowNotification("Moving the workspace failed: "+msg.Err.Error(), "error", m.Settings.NotificationDuration*2)
		return nil
	}
	if !msg.Legacy {
		if !msg.Moved {
			m.workspaceMoveInFlight = false
		}
		return nil
	}
	m.pendingWorkspaceOrder = nil
	m.workspaceMoveInFlight = false
	m.renumberLocalWorkspaces(workspaceSlots(msg.Order))
	m.SyncStateToDaemon()
	return labelVerbCmd(m.verbDialer(), workspaceArrangeWhat, "set-workspace-order", map[string]any{
		"session": m.SessionName, "order": slices.Sorted(slices.Values(msg.Order)),
	})
}

// workspaceArrangeWhat names a renumber in the notice of a failed daemon
// write.
const workspaceArrangeWhat = "Arrange"
