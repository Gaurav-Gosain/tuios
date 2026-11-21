package app

import (
	"maps"
	"slices"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

func (m *OS) masterLayoutFor(ws int) session.MasterLayoutState {
	if st, ok := m.WorkspaceMasterLayout[ws]; ok {
		return normalMasterLayout(st)
	}
	return m.configMasterLayout()
}

func (m *OS) configMasterLayout() session.MasterLayoutState {
	return normalMasterLayout(session.MasterLayoutState{
		Position: m.Settings.MasterPosition,
		Count:    m.Settings.MasterCount,
		NoGrid:   m.Settings.MasterNoGrid,
	})
}

func normalMasterLayout(st session.MasterLayoutState) session.MasterLayoutState {
	st.Position = config.ValidMasterPosition(st.Position)
	st.Count = config.ClampMasterCount(st.Count)
	return st
}

func (m *OS) masterParams() layout.MasterParams {
	st := m.masterLayoutFor(m.CurrentWorkspace)
	return layout.MasterParams{
		Position:   st.Position,
		Count:      st.Count,
		Ratio:      m.MasterRatio,
		StackRatio: m.WorkspaceStackRatio[m.CurrentWorkspace],
		Splits:     m.WorkspaceMasterSplits[m.CurrentWorkspace],
		Grid:       !st.NoGrid,
		Gap:        m.separatorGap(),
	}
}

func (m *OS) inMasterStack() bool {
	return m.AutoTiling && !m.UseBSPLayout && !m.UseScrollingLayout
}

func (m *OS) masterOpsOn() bool {
	return m.IsDaemonSession && m.DaemonClient != nil
}

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
	if !m.AutoTiling || !config.IsMasterPosition(pos) {
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
	st.Count = config.ClampMasterCount(n)
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

func (m *OS) masterIndex() int {
	panes := m.tilablePanes(m.CurrentWorkspace)
	if len(panes) == 0 {
		return -1
	}
	return m.windowIndex(panes[0])
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
	panes := m.tilablePanes(m.CurrentWorkspace)
	if len(panes) < 2 {
		return false
	}
	other := panes[0]
	if other == focused {
		other = panes[1]
	}
	target := m.windowIndex(other)
	m.SwapWindowsInstant(m.FocusedWindow, target)
	m.TileAllWindows()
	m.SyncStateToDaemon()
	return true
}

// MasterPositionMessage is the dock message for a new master position. Outside
// the master-stack layout it also says where the position takes effect, so a
// key that seems to do nothing explains itself.
func MasterPositionMessage(m *OS, pos string) string {
	return m.masterLayoutMessage("Master position: " + pos)
}

// MasterCountMessage is the dock message for a new master count, on the same
// terms as MasterPositionMessage.
func MasterCountMessage(m *OS, n int) string {
	return m.masterLayoutMessage("Master panes: " + strconv.Itoa(n))
}

func (m *OS) masterLayoutMessage(msg string) string {
	if !m.inMasterStack() {
		msg += ". This applies in the master-stack layout."
	}
	return msg
}

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
