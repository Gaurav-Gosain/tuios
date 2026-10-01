package app

import (
	"maps"
	"slices"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The master-stack shape: where the masters go and how many panes are masters.
//
// Each workspace has its own. A workspace nobody has changed uses the
// configured default (appearance.master_position, master_count and
// master_grid), read live, so a config change reaches every such workspace on
// the next retile. The actions below change the current workspace only and
// record the result in WorkspaceMasterLayout. In a daemon session that map is
// the session's: a change goes to the daemon as an op (session.MsgMasterLayout)
// and every attached client lays the workspace out the same way.

// masterLayoutFor is the shape workspace ws is laid out with.
func (m *OS) masterLayoutFor(ws int) session.MasterLayoutState {
	if st, ok := m.WorkspaceMasterLayout[ws]; ok {
		return normalMasterLayout(st)
	}
	return m.configMasterLayout()
}

// configMasterLayout is the configured default shape.
func (m *OS) configMasterLayout() session.MasterLayoutState {
	return normalMasterLayout(session.MasterLayoutState{
		Position: m.Settings.MasterPosition,
		Count:    m.Settings.MasterCount,
		NoGrid:   m.Settings.MasterNoGrid,
	})
}

// normalMasterLayout turns the zero values into the defaults they stand for.
func normalMasterLayout(st session.MasterLayoutState) session.MasterLayoutState {
	st.Position = config.ValidMasterPosition(st.Position)
	st.Count = config.ClampMasterCount(st.Count)
	return st
}

// masterParams is what the tiler reads for the current workspace.
func (m *OS) masterParams() layout.MasterParams {
	st := m.masterLayoutFor(m.CurrentWorkspace)
	return layout.MasterParams{
		Position:   st.Position,
		Count:      st.Count,
		Ratio:      m.MasterRatio,
		StackRatio: m.WorkspaceStackRatio[m.CurrentWorkspace],
		Grid:       !st.NoGrid,
		Gap:        m.separatorGap(),
	}
}

// inMasterStack reports whether the master-stack layout is the one on screen.
func (m *OS) inMasterStack() bool {
	return m.AutoTiling && !m.UseBSPLayout && !m.UseScrollingLayout
}

// masterOpsOn reports whether master layout changes go to the daemon as ops.
func (m *OS) masterOpsOn() bool {
	return m.IsDaemonSession && m.DaemonClient != nil && m.DaemonClient.MasterLayoutOps()
}

// setMasterLayout records a new shape for the current workspace, tells the
// daemon, and lays the panes out again. It reports whether anything changed.
func (m *OS) setMasterLayout(st session.MasterLayoutState) bool {
	st = normalMasterLayout(st)
	ws := m.CurrentWorkspace
	if old, ok := m.WorkspaceMasterLayout[ws]; ok && normalMasterLayout(old) == st {
		return false
	}
	if m.WorkspaceMasterLayout == nil {
		m.WorkspaceMasterLayout = make(map[int]session.MasterLayoutState)
	}
	m.WorkspaceMasterLayout[ws] = st
	if m.masterOpsOn() {
		if err := m.DaemonClient.SendMasterLayout(ws, st, false); err != nil {
			m.LogError("Failed to send the master layout to the daemon: %v", err)
		}
	}
	if m.inMasterStack() {
		m.TileAllWindows()
		m.SyncStateToDaemon()
	}
	return true
}

// seedMasterLayout gives the session this client's configured shape for the
// workspace on screen when the session has none. Two clients with different
// defaults would otherwise lay one workspace out two ways and fight over the
// size of every PTY on it. The op is sent once per workspace, and the daemon
// applies it only while the workspace still has no shape, so the first client
// to tile it decides. That holds for the default shape too: a client with the
// default config that tiles first keeps the workspace on the default when a
// client with another config attaches later.
func (m *OS) seedMasterLayout() {
	if !m.inMasterStack() || !m.masterOpsOn() {
		return
	}
	ws := m.CurrentWorkspace
	if _, ok := m.WorkspaceMasterLayout[ws]; ok || m.masterSeeded[ws] {
		return
	}
	st := m.configMasterLayout()
	if m.masterSeeded == nil {
		m.masterSeeded = make(map[int]bool)
	}
	m.masterSeeded[ws] = true
	if err := m.DaemonClient.SendMasterLayout(ws, st, true); err != nil {
		m.LogError("Failed to send the master layout to the daemon: %v", err)
	}
}

// adoptWorkspaceMasterLayout takes the session's master-stack shapes, and
// reports whether the shape of the workspace on screen changed. The session's
// map is the whole answer when the daemon takes the ops. An older daemon
// never carries the field, and then the shapes stay this client's own.
func (m *OS) adoptWorkspaceMasterLayout(state *session.SessionState) bool {
	if state == nil || !m.masterOpsOn() {
		return false
	}
	before := m.masterLayoutFor(m.CurrentWorkspace)
	m.WorkspaceMasterLayout = maps.Clone(state.WorkspaceMasterLayout)
	return m.masterLayoutFor(m.CurrentWorkspace) != before
}

// CycleMasterPosition moves the current workspace's masters to the next
// position in config.MasterPositions and returns it, or "" when tiling is off.
func (m *OS) CycleMasterPosition() string {
	if !m.AutoTiling {
		return ""
	}
	st := m.masterLayoutFor(m.CurrentWorkspace)
	i := slices.Index(config.MasterPositions, st.Position)
	st.Position = config.MasterPositions[(i+1)%len(config.MasterPositions)]
	m.setMasterLayout(st)
	return st.Position
}

// SetMasterPosition puts the current workspace's masters at pos. It reports
// whether it applied, which is false when tiling is off or pos is not a
// position.
func (m *OS) SetMasterPosition(pos string) bool {
	if !m.AutoTiling || !slices.Contains(config.MasterPositions, pos) {
		return false
	}
	st := m.masterLayoutFor(m.CurrentWorkspace)
	st.Position = pos
	m.setMasterLayout(st)
	return true
}

// SetMasterCount sets how many panes are masters on the current workspace,
// clamped to the range, and returns the count in force, or 0 when tiling is
// off.
func (m *OS) SetMasterCount(n int) int {
	if !m.AutoTiling {
		return 0
	}
	st := m.masterLayoutFor(m.CurrentWorkspace)
	st.Count = min(max(n, config.MasterCountMin), config.MasterCountMax)
	m.setMasterLayout(st)
	return st.Count
}

// AddMaster makes one more pane a master on the current workspace and returns
// the count in force, or 0 when tiling is off. It stops when every tiled pane
// is a master, because a count past that changes nothing on screen and only
// takes more presses of remove_master to undo.
func (m *OS) AddMaster() int {
	count := m.masterLayoutFor(m.CurrentWorkspace).Count
	if count >= len(m.tilablePanes(m.CurrentWorkspace)) {
		return m.SetMasterCount(count)
	}
	return m.SetMasterCount(count + 1)
}

// RemoveMaster makes one pane fewer a master, down to one, and returns the
// count in force, or 0 when tiling is off.
func (m *OS) RemoveMaster() int {
	return m.SetMasterCount(m.masterLayoutFor(m.CurrentWorkspace).Count - 1)
}

// masterIndex is the index in m.Windows of the current workspace's first
// master, or -1 when the workspace has no tiled pane.
func (m *OS) masterIndex() int {
	for i, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.IsFloating {
			return i
		}
	}
	return -1
}

// FocusMaster moves the focus to the first master. It reports false when the
// master-stack layout is not on screen or there is no pane.
func (m *OS) FocusMaster() bool {
	if !m.inMasterStack() {
		return false
	}
	i := m.masterIndex()
	if i < 0 {
		return false
	}
	m.FocusWindow(i)
	m.MarkAllDirty()
	return true
}

// SwapWithMaster swaps the focused pane with the first master, so the focused
// pane becomes the master and keeps the focus. On the master itself it swaps
// with the first stack pane, as dwm's zoom does. It reports false when the
// master-stack layout is not on screen or there is nothing to swap with.
func (m *OS) SwapWithMaster() bool {
	if !m.inMasterStack() || m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		return false
	}
	focused := m.Windows[m.FocusedWindow]
	if focused.Workspace != m.CurrentWorkspace || focused.Minimized || focused.IsFloating {
		return false
	}
	target := m.masterIndex()
	if target == m.FocusedWindow {
		target = -1
		for i := m.FocusedWindow + 1; i < len(m.Windows); i++ {
			w := m.Windows[i]
			if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.IsFloating {
				target = i
				break
			}
		}
	}
	if target < 0 {
		return false
	}
	m.SwapWindowsInstant(m.FocusedWindow, target)
	// The panes swapped rectangles, which are the slots they now hold. A
	// retile settles anything a resize had left between slots.
	m.TileAllWindows()
	m.SyncStateToDaemon()
	return true
}

// MasterPositionMessage is the dock message for a new master position. Outside
// the master-stack layout it also says where the position takes effect, so a
// key that seems to do nothing explains itself.
func MasterPositionMessage(m *OS, pos string) string {
	msg := "Master position: " + pos
	if !m.inMasterStack() {
		msg += ". This applies in the master-stack layout."
	}
	return msg
}

// MasterCountMessage is the dock message for a new master count, on the same
// terms as MasterPositionMessage.
func MasterCountMessage(m *OS, n int) string {
	msg := "Master panes: " + strconv.Itoa(n)
	if !m.inMasterStack() {
		msg += ". This applies in the master-stack layout."
	}
	return msg
}

// adoptConfigMasterLayout lands a config-side change to the master-stack shape
// (the settings page, set-config, a config reload) on the workspace on screen.
// The other workspaces follow the config until something changes them, except
// in a daemon session, where each one keeps the shape the session settled for
// it.
func (m *OS) adoptConfigMasterLayout() {
	cur := m.configMasterLayout()
	if cur == m.lastConfigMaster {
		return
	}
	m.lastConfigMaster = cur
	if !m.AutoTiling {
		return
	}
	if _, ok := m.WorkspaceMasterLayout[m.CurrentWorkspace]; ok || m.masterOpsOn() {
		m.setMasterLayout(cur)
	}
}
