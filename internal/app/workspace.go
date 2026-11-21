package app

import (
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// SwitchToWorkspace switches to the specified workspace, picking a default focus
// (the workspace's saved window, else its first visible one).
//
// It is the switch a person makes: a key, a click, the palette, the
// switcher. With workspaces.new_window_when_empty on, a switch to a workspace
// with no panes opens one there. Scripts and tapes switch with
// switchToWorkspace, because they bring their own panes. A move that follows
// its pane comes here with the pane already on the workspace, so it opens
// nothing. Another client following this switch, and a switch the daemon
// makes (tuios select-workspace, tuios xpanes, return_when_empty), apply the
// session state without coming here, so only the client that made the
// switch opens the pane, and the session gets one.
func (m *OS) SwitchToWorkspace(workspace int) {
	asked := workspace
	workspace = m.newWorkspaceSlot(workspace)
	from := m.CurrentWorkspace
	var fromPane *terminal.Window
	if fw := m.GetFocusedWindow(); fw != nil && fw.Workspace == from && !fw.IsScratch {
		fromPane = fw
	}
	typing := m.Mode == TerminalMode
	m.switchToWorkspace(workspace, -1)
	if asked != from && workspace == from {
		from = 0
	}
	m.openPaneOnEmptyWorkspace(from, workspace, fromPane, typing)
}

func (m *OS) workspaceTabKey(ws int) int {
	if k, ok := m.workspaceTabKeys[ws]; ok {
		return k
	}
	return ws
}

func (m *OS) newWorkspaceSlot(workspace int) int {
	if !m.IsDaemonSession || session.IsScratchWorkspace(workspace) {
		return workspace
	}
	last := 0
	for ws := 1; ws <= m.NumWorkspaces; ws++ {
		req, asked := m.paneRequests[ws]
		if m.GetWorkspaceWindowCount(ws) > 0 || (asked && time.Since(req.at) < paneRequestTimeout) {
			last = ws
		}
	}
	return min(workspace, last+1)
}

// NextWorkspace switches to the next workspace that holds at least one
// pane, wrapping at the end. With no other such workspace it does nothing, so
// a run of presses cannot land on an empty workspace, and with
// workspaces.new_window_when_empty on it cannot open a pane on each one.
func (m *OS) NextWorkspace() { m.cycleWorkspace(1) }

// PrevWorkspace is NextWorkspace in the other direction.
func (m *OS) PrevWorkspace() { m.cycleWorkspace(-1) }

// LastWorkspace goes back to the workspace this client was on before the
// current one, as tmux's last-window does. Two presses go back and forth.
func (m *OS) LastWorkspace() {
	if m.lastWorkspace < 1 || m.lastWorkspace == m.CurrentWorkspace {
		m.ShowNotification("No last "+m.workspaceWord(), "info", m.Settings.NotificationDuration)
		return
	}
	m.SwitchToWorkspace(m.lastWorkspace)
}

// BreakPane moves the focused pane to the first workspace with no pane and
// goes with it, as tmux's break-pane does.
func (m *OS) BreakPane() {
	ws := m.NewWorkspaceTarget()
	switch {
	case m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows):
		return
	case ws == 0:
		m.ShowNotification("Every "+m.workspaceWord()+" has a pane", "info", m.Settings.NotificationDuration)
		return
	}
	m.MoveWindowToWorkspaceAndFollow(m.FocusedWindow, ws)
}

func (m *OS) cycleWorkspace(step int) {
	n := m.NumWorkspaces
	for i := 1; i < n; i++ {
		ws := ((m.CurrentWorkspace-1+step*i)%n+n)%n + 1
		if m.GetWorkspaceWindowCount(ws) > 0 {
			m.SwitchToWorkspace(ws)
			return
		}
	}
}

func (m *OS) openPaneOnEmptyWorkspace(from, workspace int, fromPane *terminal.Window, typing bool) {
	if m.UserConfig == nil || !m.UserConfig.Workspaces.NewWindowWhenEmpty {
		return
	}
	if from == workspace || m.CurrentWorkspace != workspace || session.IsScratchWorkspace(workspace) {
		return
	}
	for _, w := range m.Windows {
		if w.Workspace == workspace && !w.IsScratch {
			return
		}
	}
	if m.IsDaemonSession && m.DaemonClient != nil {
		if req, ok := m.paneRequests[workspace]; ok && time.Since(req.at) < paneRequestTimeout {
			req.typing = typing
			m.paneRequests[workspace] = req
			return
		}
		cwdFrom, sshFrom := "", ""
		if fromPane != nil {
			cwdFrom = fromPane.ID
			if m.FollowSSHOnNewWindow() {
				sshFrom = fromPane.ID
			}
		}
		cols, rows, _ := m.newPaneContentSize()
		if err := m.DaemonClient.SendNewWindowFrom(workspace, m.workspaceTabKey(workspace), cols, rows, cwdFrom, sshFrom); err != nil {
			m.LogError("Failed to ask the daemon for a pane on workspace %d: %v", workspace, err)
			return
		}
		if m.paneRequests == nil {
			m.paneRequests = make(map[int]paneRequest)
		}
		m.paneRequests[workspace] = paneRequest{at: time.Now(), typing: typing}
		return
	}
	dir := ""
	if fromPane != nil {
		dir = fromPane.CWD()
	}
	before := len(m.Windows)
	m.addLocalWindow(dir, "", nil)
	if typing && len(m.Windows) > before && m.FocusedWindow == len(m.Windows)-1 && m.Mode == WindowManagementMode {
		m.EnterTerminalMode()
	}
}

type paneRequest struct {
	at     time.Time
	typing bool
}

const paneRequestTimeout = 5 * time.Second

func (m *OS) settlePaneRequests() {
	for ws, req := range m.paneRequests {
		idx := -1
		for i, w := range m.Windows {
			if w.Workspace == ws && !w.IsScratch {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		delete(m.paneRequests, ws)
		if ws != m.CurrentWorkspace {
			continue
		}
		if m.FocusedWindow < 0 {
			m.FocusWindow(idx)
		}
		if req.typing && time.Since(req.at) < paneRequestTimeout &&
			m.FocusedWindow == idx && m.Mode == WindowManagementMode {
			m.EnterTerminalMode()
		}
	}
}

// PaneRequestsInFlight lists the workspaces this client asked a pane for that
// has not arrived, for GetSessionInfo.
func (m *OS) PaneRequestsInFlight() []int {
	out := []int{}
	for ws, req := range m.paneRequests {
		if time.Since(req.at) < paneRequestTimeout {
			out = append(out, ws)
		}
	}
	slices.Sort(out)
	return out
}

func (m *OS) switchToWorkspace(workspace, focusTarget int) {
	m.settleSizes(func() { m.switchToWorkspaceHeld(workspace, focusTarget) })
}

func (m *OS) switchToWorkspaceHeld(workspace, focusTarget int) {
	if (workspace < 1 || workspace > m.NumWorkspaces) && !session.IsScratchWorkspace(workspace) {
		m.LogWarn("Cannot switch to workspace %d: out of range (1-%d)", workspace, m.NumWorkspaces)
		return
	}

	if workspace == m.CurrentWorkspace {
		return
	}
	m.CancelWindowResize()
	m.CloseHints()
	m.ClosePaneLabels()

	if m.TapeRecorder != nil && m.TapeRecorder.IsRecording() {
		m.TapeRecorder.RecordWorkspaceSwitch(workspace)
	}

	oldWorkspace := m.CurrentWorkspace
	if !session.IsScratchWorkspace(oldWorkspace) && !session.IsScratchWorkspace(workspace) {
		m.lastWorkspace = oldWorkspace
	}
	windowsInNew := m.GetWorkspaceWindowCount(workspace)
	m.LogInfo("Switching workspace: %d → %d (%d windows)", oldWorkspace, workspace, windowsInNew)

	if len(m.Animations) > 0 {
		m.landSnapAnimations()
		m.Animations = m.Animations[:0]
		m.LogInfo("Landed and cancelled animations during workspace switch")
	}
	m.beginWorkspaceSlide(oldWorkspace, workspace)

	if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		if m.Windows[m.FocusedWindow].Workspace == m.CurrentWorkspace {
			m.WorkspaceFocus[m.CurrentWorkspace] = m.FocusedWindow
		}
	}
	m.SaveCurrentLayout()

	if m.IsDaemonSession && m.DaemonClient != nil {
		m.UnsubscribeWorkspaceWindows(oldWorkspace)
		m.SubscribeWorkspaceWindows(workspace)
	}

	switch {
	case session.IsScratchWorkspace(workspace):
		m.enterScratchWorkspace(oldWorkspace, workspace)
	case session.IsScratchWorkspace(oldWorkspace):
		m.leaveScratchWorkspace()
	}

	m.CurrentWorkspace = workspace
	m.clearViewedWorkspaceMarks()
	layoutModeBefore := m.LayoutModeName()
	m.RestoreWorkspaceLayout(workspace)
	layoutModeChanged := m.LayoutModeName() != layoutModeBefore

	focusedSet := false
	if focusTarget >= 0 && focusTarget < len(m.Windows) && m.Windows[focusTarget].Workspace == workspace {
		m.FocusWindow(focusTarget)
		focusedSet = true
	}

	if !focusedSet {
		if savedFocus, exists := m.WorkspaceFocus[workspace]; exists {
			if savedFocus >= 0 && savedFocus < len(m.Windows) {
				if m.Windows[savedFocus].Workspace == workspace && !m.Windows[savedFocus].Minimized {
					m.FocusWindow(savedFocus)
					m.LogInfo("Restored focus to saved window (index: %d)", savedFocus)
					focusedSet = true
				}
			}
		}
	}

	if !focusedSet {
		for i, w := range m.Windows {
			if w.Workspace == workspace && !w.Minimized {
				m.FocusWindow(i)
				m.LogInfo("Focused first visible window (index: %d)", i)
				focusedSet = true
				break
			}
		}
	}

	if !focusedSet {
		m.FocusedWindow = -1
		m.LogInfo("No visible windows in workspace %d", workspace)
		if m.Mode == TerminalMode {
			m.ChooseMode(WindowManagementMode, ModeCauseNoPane)
			m.LogInfo("Switched to window management mode (empty workspace)")
		}
	} else {
		if m.TapeRecorder != nil && m.TapeRecorder.IsRecording() {
			if m.Mode == TerminalMode {
				m.TapeRecorder.RecordModeSwitch(tape.CommandTypeTerminalMode)
			} else {
				m.TapeRecorder.RecordModeSwitch(tape.CommandTypeWindowManagementMode)
			}
		}
	}

	if m.AutoTiling && (!m.WorkspaceHasCustom[workspace] || m.tiledLayoutStale()) {
		m.LogInfo("Auto-tiling workspace %d (no custom layout)", workspace)
		m.arriving = true
		m.TileVisibleWorkspaceWindows()
		m.arriving = false
	} else {
		m.settleBorderMode(workspace)
	}

	for _, w := range m.Windows {
		if w.Workspace == workspace {
			w.MarkPositionDirty()
		}
	}

	m.SyncStateToDaemon()

	if session.IsScratchWorkspace(workspace) || session.IsScratchWorkspace(oldWorkspace) {
		return
	}
	if layoutModeChanged {
		m.FireLayoutChanged()
	}
	focusedID, focusedName := "", ""
	if w := m.GetFocusedWindow(); w != nil {
		focusedID, focusedName = w.ID, w.Title()
	}
	m.FireHookContext(hooks.AfterWorkspaceSwitch, hooks.Context{
		WindowID:          focusedID,
		WindowName:        focusedName,
		Workspace:         workspace,
		PreviousWorkspace: oldWorkspace,
	})
}

func (m *OS) settleBorderMode(workspace int) {
	if !m.AutoTiling || m.UseScrollingLayout {
		return
	}
	if m.UseBSPLayout {
		if tree := m.WorkspaceTrees[workspace]; tree == nil || tree.IsEmpty() {
			return
		}
	}
	for _, w := range m.Windows {
		if w.Workspace != workspace || w.Minimized || w.IsFloating {
			continue
		}
		w.SetTiled(m.panesBorderless())
	}
}

// MoveWindowToWorkspace moves a window to the specified workspace without changing focus.
func (m *OS) MoveWindowToWorkspace(windowIndex int, workspace int) {
	if windowIndex < 0 || windowIndex >= len(m.Windows) {
		return
	}
	if isScratch(m.Windows[windowIndex]) || session.IsScratchWorkspace(m.Windows[windowIndex].Workspace) {
		m.ShowNotification("A scratch pane stays in its scratch group.", "info", m.Settings.NotificationDuration)
		return
	}
	if windowIndex < 0 || windowIndex >= len(m.Windows) {
		m.LogWarn("Cannot move window: invalid index %d", windowIndex)
		return
	}
	if workspace < 1 || workspace > m.NumWorkspaces {
		m.LogWarn("Cannot move window: workspace %d out of range (1-%d)", workspace, m.NumWorkspaces)
		return
	}

	window := m.Windows[windowIndex]
	oldWorkspace := window.Workspace

	if oldWorkspace == workspace {
		return
	}

	m.LogInfo("Moving window %s: workspace %d → %d", window.Title(), oldWorkspace, workspace)

	if m.IsDaemonSession && m.DaemonClient != nil && oldWorkspace == m.CurrentWorkspace {
		m.unsubscribeFromPTY(window)
	}

	window.Workspace = workspace
	window.MarkPositionDirty()

	if windowIndex == m.FocusedWindow {
		m.LogInfo("Moved focused window, finding next in workspace %d", m.CurrentWorkspace)
		m.FocusNextVisibleWindowInWorkspace()
	}

	if m.AutoTiling {
		m.LogInfo("Auto-tiling workspace %d after window move", m.CurrentWorkspace)
		m.TileVisibleWorkspaceWindows()
		m.SaveCurrentLayout()
		m.WorkspaceHasCustom[m.CurrentWorkspace] = false
	}
}

// MoveWindowToWorkspaceAndFollow moves a window to the specified workspace and switches to that workspace.
func (m *OS) MoveWindowToWorkspaceAndFollow(windowIndex int, workspace int) {
	m.settleSizes(func() { m.moveWindowToWorkspaceAndFollow(windowIndex, workspace) })
}

func (m *OS) moveWindowToWorkspaceAndFollow(windowIndex int, workspace int) {
	if windowIndex < 0 || windowIndex >= len(m.Windows) {
		return
	}
	if isScratch(m.Windows[windowIndex]) || session.IsScratchWorkspace(m.Windows[windowIndex].Workspace) {
		m.ShowNotification("A scratch pane stays in its scratch group.", "info", m.Settings.NotificationDuration)
		return
	}
	if windowIndex < 0 || windowIndex >= len(m.Windows) {
		return
	}
	if workspace < 1 || workspace > m.NumWorkspaces {
		return
	}

	window := m.Windows[windowIndex]
	oldWorkspace := window.Workspace

	if oldWorkspace == workspace {
		return
	}

	if m.IsDaemonSession && m.DaemonClient != nil && oldWorkspace == m.CurrentWorkspace {
		m.unsubscribeFromPTY(window)
	}

	window.Workspace = workspace
	window.MarkPositionDirty()

	if m.AutoTiling && m.CurrentWorkspace == oldWorkspace {
		m.TileVisibleWorkspaceWindows()
		m.SaveCurrentLayout()
		m.WorkspaceHasCustom[m.CurrentWorkspace] = false
	}

	m.SwitchToWorkspace(workspace)
	m.FocusWindow(windowIndex)

	if m.AutoTiling {
		m.TileVisibleWorkspaceWindows()
		m.SaveCurrentLayout()
		m.WorkspaceHasCustom[m.CurrentWorkspace] = false
	}
}

// FocusNextVisibleWindowInWorkspace focuses the next visible window in the workspace.
func (m *OS) FocusNextVisibleWindowInWorkspace() {
	for i := range m.Windows {
		w := m.Windows[i]
		if w.Workspace == m.CurrentWorkspace && !w.Minimized {
			m.FocusWindow(i)
			return
		}
	}

	m.FocusedWindow = -1
	if m.Mode == TerminalMode {
		m.ChooseMode(WindowManagementMode, ModeCauseNoPane)
	}
}

// GetVisibleWindows returns all visible windows in the current workspace.
func (m *OS) GetVisibleWindows() []*terminal.Window {
	visible := make([]*terminal.Window, 0)
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized {
			visible = append(visible, w)
		}
	}
	return visible
}

// GetWorkspaceWindowCount returns the number of windows in a workspace.
func (m *OS) GetWorkspaceWindowCount(workspace int) int {
	count := 0
	for _, w := range m.Windows {
		if w.Workspace == workspace && !isScratch(w) {
			count++
		}
	}
	return count
}

// TileVisibleWorkspaceWindows lays out the panes of the workspace on screen.
//
// It is TileAllWindows, which is the only tiler there is. A copy of any one
// layout's path here would fall behind everything the real one handles: the
// zoom camera, the skip for a pane the pointer is dragging, the open animation,
// and the deferred resize a live drag needs.
func (m *OS) TileVisibleWorkspaceWindows() {
	m.TileAllWindows()
}
