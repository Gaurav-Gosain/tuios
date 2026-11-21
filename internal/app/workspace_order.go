package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

type dockWorkspaceDragState struct {
	PressActive bool
	Workspace   int
	PressX      int
	PressY      int
	Dragging    bool
	Order       []int
}

func (m *OS) workspaceRank(ws int) int {
	for i, n := range m.pendingWorkspaceOrder {
		if n == ws {
			return i
		}
	}
	return len(m.pendingWorkspaceOrder) + ws
}

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
			return true
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
		_ = jsonx.Unmarshal(raw, &res)
		return WorkspaceRenumberedMsg{Order: order, Legacy: res.Renumbered == nil, Moved: len(res.Renumbered) > 0}
	}
}

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

const workspaceArrangeWhat = "Arrange"
