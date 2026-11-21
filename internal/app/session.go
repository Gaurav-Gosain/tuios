package app

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// BuildSessionState creates a serializable SessionState from the current OS state.
// This is called progressively during Update() to sync state to the daemon.
// For windows with active animations, it uses the final (target) positions
// so other clients see the end state immediately without animation jitter.
func (m *OS) BuildSessionState() *session.SessionState {
	currentWorkspace, autoTiling := m.CurrentWorkspace, m.AutoTiling
	if m.InScratchView() {
		currentWorkspace, autoTiling = max(m.scratchBase, 1), m.scratchBaseTiling
	}
	state := &session.SessionState{
		Name:             m.SessionName,
		CurrentWorkspace: currentWorkspace,
		MasterRatio:      m.MasterRatio,
		AutoTiling:       autoTiling,
		Width:            m.GetLayoutWidth(),
		Height:           m.GetLayoutHeight(),
		WorkspaceFocus:   make(map[int]string),
		FocusHistory:     make(map[int][]string),
		BaseVersion:      m.DaemonStateVersion,
	}
	state.PaneReportBg, state.PaneReportFg, state.PaneReportPalette = m.paneReportHex()

	windowAnimations := make(map[*terminal.Window]*ui.Animation)
	for _, anim := range m.Animations {
		if anim != nil && anim.Window != nil && !anim.Complete {
			windowAnimations[anim.Window] = anim
		}
	}

	state.Windows = make([]session.WindowState, len(m.Windows))
	for i, w := range m.Windows {
		x, y, width, height := w.X, w.Y, w.Width, w.Height

		if anim, hasAnim := windowAnimations[w]; hasAnim {
			x = anim.EndX
			y = anim.EndY
			width = anim.EndWidth
			height = anim.EndHeight
		}

		state.Windows[i] = session.WindowState{
			ID:           w.ID,
			Title:        session.ClampDisplayText(w.Title()),
			CustomName:   session.ClampDisplayText(w.CustomName),
			X:            x,
			Y:            y,
			Width:        width,
			Height:       height,
			Z:            w.Z,
			Workspace:    w.Workspace,
			Minimized:    w.Minimized,
			MinimizedAt:  w.MinimizedAt,
			PreMinimizeX: w.PreMinimizeX,
			PreMinimizeY: w.PreMinimizeY,
			PreMinimizeW: w.PreMinimizeWidth,
			PreMinimizeH: w.PreMinimizeHeight,
			PTYID:        w.PTYID,
			IsAltScreen:  w.IsAltScreen(),
			IsFloating:   w.IsFloating,
			Zoomed:       w.Zoomed,
			PreZoomX:     w.PreZoomX,
			PreZoomY:     w.PreZoomY,
			PreZoomW:     w.PreZoomWidth,
			PreZoomH:     w.PreZoomHeight,
			Popup:        w.IsPopup,
			PopupWidth:   w.PopupWidth,
			PopupHeight:  w.PopupHeight,
			PopupDim:     w.PopupDim,
			Scratch:      w.IsScratch,
			ScratchName:  w.ScratchName,
			Rail:         w.Rail,
			Stack:        w.Stack,
			StackIndex:   w.StackIndex,
			Kind:         w.Kind,
			URI:          w.URI,
		}
	}

	if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		state.FocusedWindowID = m.Windows[m.FocusedWindow].ID
	}

	for workspace, windowIdx := range m.WorkspaceFocus {
		if windowIdx >= 0 && windowIdx < len(m.Windows) {
			state.WorkspaceFocus[workspace] = m.Windows[windowIdx].ID
		}
	}
	for workspace, history := range m.FocusHistory {
		state.FocusHistory[workspace] = slices.Clone(history)
	}

	if m.WorkspaceTrees != nil && m.AutoTiling {
		state.WorkspaceTrees = make(map[int]*session.SerializedBSPTree)
		for ws, tree := range m.WorkspaceTrees {
			if tree != nil {
				serialized := tree.Serialize()
				if serialized != nil {
					state.WorkspaceTrees[ws] = &session.SerializedBSPTree{
						Root:         convertBSPNode(serialized.Root),
						AutoScheme:   serialized.AutoScheme,
						DefaultRatio: serialized.DefaultRatio,
					}
				}
			}
		}
	}
	if m.AutoTiling {
		state.WorkspaceScrollColumns = m.scrollColumnsState()
	}

	if m.WindowToBSPID != nil {
		state.WindowToBSPID = make(map[string]int)
		maps.Copy(state.WindowToBSPID, m.WindowToBSPID)
	}
	state.NextBSPWindowID = m.NextBSPWindowID
	state.TilingScheme = int(m.TilingScheme)
	state.LayoutMode = m.LayoutModeName()
	state.NumWorkspaces = m.NumWorkspaces
	state.WorkspaceMasterRatio = make(map[int]float64, len(m.WorkspaceMasterRatio)+1)
	maps.Copy(state.WorkspaceMasterRatio, m.WorkspaceMasterRatio)
	if m.AutoTiling {
		state.WorkspaceMasterRatio[m.CurrentWorkspace] = m.MasterRatio
	}
	if len(state.WorkspaceMasterRatio) == 0 {
		state.WorkspaceMasterRatio = nil
	}
	if len(m.WorkspaceStackRatio) > 0 {
		state.WorkspaceStackRatio = maps.Clone(m.WorkspaceStackRatio)
	}
	if len(m.WorkspaceMasterSplits) > 0 {
		state.WorkspaceMasterSplits = make(map[int]layout.MasterSplits, len(m.WorkspaceMasterSplits))
		for ws, sp := range m.WorkspaceMasterSplits {
			state.WorkspaceMasterSplits[ws] = sp.Clone()
		}
	}
	if len(m.WorkspaceLayoutMode) > 0 {
		state.WorkspaceLayoutModes = maps.Clone(m.WorkspaceLayoutMode)
	}
	if len(m.WorkspaceMasterLayout) > 0 {
		state.WorkspaceMasterLayout = maps.Clone(m.WorkspaceMasterLayout)
	}
	state.WorkspaceHasCustom = make(map[int]bool, len(m.WorkspaceHasCustom))
	maps.Copy(state.WorkspaceHasCustom, m.WorkspaceHasCustom)
	if len(state.WorkspaceHasCustom) == 0 {
		state.WorkspaceHasCustom = nil
	}
	state.SidebarWidth = m.SidebarWidthPref
	state.SidebarCollapsed = m.SidebarCollapsed
	sharedBorders := m.SharedBorders
	if m.OwnPaneBorders {
		sharedBorders = m.SessionSharedBorders
	}
	state.PaneGeometry = &session.PaneGeometryState{
		SharedBorders:     sharedBorders,
		PaneGap:           m.PaneGap,
		ScrollColumnWidth: m.ScrollColumnWidth,
	}
	state.ScrollStrip = m.ScrollStripState()
	state.LayoutGen = m.layoutGenApplied

	return state
}

func convertBSPNode(node *layout.SerializedNode) *session.SerializedBSPNode {
	if node == nil {
		return nil
	}
	return &session.SerializedBSPNode{
		WindowID:   node.WindowID,
		SplitType:  node.SplitType,
		SplitRatio: node.SplitRatio,
		Left:       convertBSPNode(node.Left),
		Right:      convertBSPNode(node.Right),
	}
}

func clampWorkspace(ws int) int {
	if ws < 1 {
		return 1
	}
	return ws
}

// RestoreFromState restores the OS state from a SessionState.
// This is called when attaching to an existing session.
// The caller must set up PTY output handlers after calling this.
func (m *OS) RestoreFromState(state *session.SessionState) error {
	if state == nil {
		m.LogInfo("[RESTORE] RestoreFromState: state is nil")
		return nil
	}
	defer m.noteFocusGiven(state)

	m.LogInfo("[RESTORE] RestoreFromState: restoring %d windows", len(state.Windows))

	m.sessionUnarranged = stateUnarranged(state)

	m.forgetSyncedState()

	m.SessionName = state.Name
	m.adoptSessionLabels(state)
	m.DaemonStateVersion = state.Version
	m.workspaceMarkSet = nil
	m.pendingWorkspaceOrder = nil
	m.workspaceMoveInFlight = false
	m.CurrentWorkspace = clampWorkspace(state.CurrentWorkspace)
	m.MasterRatio = state.MasterRatio
	m.AutoTiling = state.AutoTiling
	m.WorkspaceMasterRatio = make(map[int]float64, len(state.WorkspaceMasterRatio))
	m.adoptWorkspaceMasterRatio(state)
	m.WorkspaceStackRatio = make(map[int]float64, len(state.WorkspaceStackRatio))
	m.adoptWorkspaceStackRatio(state)
	m.WorkspaceMasterSplits = make(map[int]layout.MasterSplits, len(state.WorkspaceMasterSplits))
	m.adoptWorkspaceMasterSplits(state)
	m.WorkspaceLayoutMode = make(map[int]string, len(state.WorkspaceLayoutModes))
	maps.Copy(m.WorkspaceLayoutMode, state.WorkspaceLayoutModes)
	m.sessionLayoutMode = ""
	m.adoptSessionLayoutMode(state)
	m.WorkspaceMasterLayout = maps.Clone(state.WorkspaceMasterLayout)
	m.masterSeeded = nil
	m.WorkspaceHasCustom = make(map[int]bool, len(state.WorkspaceHasCustom))
	m.adoptWorkspaceHasCustom(state)
	m.adoptSidebarState(state)
	m.joinSession(state)
	m.adoptPaneGeometry(state)

	if state.Width > 0 && state.Height > 0 {
		m.EffectiveWidth = state.Width
		m.EffectiveHeight = state.Height
		m.Width = state.Width
		m.Height = state.Height
		m.LogInfo("[RESTORE] Set size from state: %dx%d", state.Width, state.Height)
	}

	if m.DaemonClient != nil {
		m.SessionReserve = m.DaemonClient.SessionLayoutReserve()
		m.layoutGenApplied = m.DaemonClient.SessionLayoutGeneration()
	}

	for _, w := range m.Windows {
		w.Close()
	}
	m.Windows = nil
	m.pip = pipState{occluder: m.pip.occluder[:0]}

	for i, ws := range state.Windows {
		m.LogInfo("[RESTORE] Creating window %d: ID=%s, PTYID=%s", i, shortID(ws.ID), shortID(ws.PTYID))
		window := m.newWindowFromState(&ws)

		window.DisableCallbacks()

		m.Windows = append(m.Windows, window)
		m.LogInfo("[RESTORE] Window %d created: DaemonMode=%v, PTYID=%s", i, window.DaemonMode, shortID(window.PTYID))
	}

	m.FocusedWindow = -1
	if state.FocusedWindowID != "" {
		for i, w := range m.Windows {
			if w.ID == state.FocusedWindowID {
				m.FocusedWindow = i
				break
			}
		}
	}
	if m.FocusedWindow < 0 && len(m.Windows) > 0 {
		for i, w := range m.Windows {
			if w.Workspace == m.CurrentWorkspace && !w.Minimized {
				m.FocusedWindow = i
				break
			}
		}
	}

	m.WorkspaceFocus = make(map[int]int)
	for workspace, windowID := range state.WorkspaceFocus {
		for i, w := range m.Windows {
			if w.ID == windowID {
				m.WorkspaceFocus[workspace] = i
				break
			}
		}
	}
	m.FocusHistory = session.CloneFocusHistory(state.FocusHistory)

	if state.WindowToBSPID != nil {
		m.WindowToBSPID = make(map[string]int)
		for k, v := range state.WindowToBSPID {
			m.WindowToBSPID[k] = v
			m.LogInfo("[RESTORE] WindowToBSPID: %s -> %d", shortID(k), v)
		}
	}
	m.NextBSPWindowID = state.NextBSPWindowID
	m.TilingScheme = layout.AutoScheme(state.TilingScheme)
	m.pendingScrollColumns = nil
	if state.AutoTiling {
		m.adoptScrollColumns(state.WorkspaceScrollColumns)
	}
	m.ApplyLayoutModeName(state.LayoutMode)
	m.LogInfo("[RESTORE] NextBSPWindowID=%d, TilingScheme=%d, LayoutMode=%s", m.NextBSPWindowID, m.TilingScheme, m.LayoutModeName())

	if state.WorkspaceTrees != nil && state.AutoTiling {
		m.WorkspaceTrees = make(map[int]*layout.BSPTree)
		for ws, serialized := range state.WorkspaceTrees {
			if serialized != nil {
				layoutSerialized := &layout.SerializedBSPTree{
					Root:         convertSessionBSPNode(serialized.Root),
					AutoScheme:   serialized.AutoScheme,
					DefaultRatio: serialized.DefaultRatio,
				}
				tree := layoutSerialized.Deserialize()
				m.WorkspaceTrees[ws] = tree
				if tree != nil {
					ids := tree.GetAllWindowIDs()
					m.LogInfo("[RESTORE] BSP tree for workspace %d restored with %d windows: %v", ws, len(ids), ids)
				}
			}
		}
	}
	m.noteSessionTrees()

	if m.AutoTiling && m.UseScrollingLayout && state.ScrollStrip != nil {
		m.GetOrCreateScrollingLayout().ViewportX = state.ScrollStrip.ViewportX
	}

	m.placeUnplacedWindows(state, m.Windows)

	if zw := m.zoomedWindow(); zw != nil {
		if m.zoomUsesLayout(zw) {
			m.zoomRelayout = true
			m.TileAllWindows()
		} else {
			m.applyZoomRect(zw, false)
		}
	}

	m.applyPopupRects(false)

	m.MarkAllDirty()
	m.LogInfo("[RESTORE] Restored session state: %d windows, FocusedWindow=%d, AutoTiling=%v, Workspace=%d", len(m.Windows), m.FocusedWindow, m.AutoTiling, m.CurrentWorkspace)

	m.RestoredFromState = true

	if len(m.Windows) > 0 && m.FocusedWindow >= 0 {
		m.ChooseMode(TerminalMode, ModeCauseRestore)
	}

	return nil
}

func stateUnarranged(state *session.SessionState) bool {
	for i := range state.Windows {
		if !state.Windows[i].Unplaced {
			return false
		}
	}
	return true
}

// ApplyStateSync applies a state the daemon sent on its own account: a
// mutation of its own, or the reconcile answer to this client's push. A peer's
// push goes through ApplyStateSyncFrom, which is told whose push it is.
func (m *OS) ApplyStateSync(state *session.SessionState) error {
	return m.ApplyStateSyncFrom(state, "")
}

// ApplyStateSyncFrom applies a state update, from the daemon or from the peer
// named by sourceID. This handles window creation, deletion, and property
// updates.
//
// The origin decides one thing: whether the tiling topology in the state is
// news. The daemon never computes a tree, so a tree arrives either because a
// peer built it, or because the daemon is echoing what some client last
// pushed, this client included. See adoptTopology below.
func (m *OS) ApplyStateSyncFrom(state *session.SessionState, sourceID string) error {
	defer m.adoptFocusReport()
	if state == nil {
		return nil
	}
	if m.DaemonClient != nil && !m.DaemonClient.AcceptState(state) {
		return nil
	}
	fromPeer := sourceID != ""
	m.adoptWorkspaceRenumbering(state)
	if to := clampWorkspace(state.CurrentWorkspace); to != m.CurrentWorkspace {
		m.beginWorkspaceSlide(m.CurrentWorkspace, to)
	}

	m.forgetSyncedState()

	m.daemonWindowIntent = false

	m.applyingPeerSync = true
	m.syncAnswerOwed = false
	m.turnsWithinSync = nil
	treeOps := m.treeOpsOn()
	var unsentBefore map[int]bool
	if treeOps {
		unsentBefore = m.unsentTrees()
	}
	defer func() {
		m.applyingPeerSync = false
		m.treeAnswerBase, m.treeAnswerUser = state.Version, unsentBefore
		defer func() { m.treeAnswerBase, m.treeAnswerUser = 0, nil }()
		if m.syncAnswerOwed {
			m.syncAnswerOwed = false
			m.SyncStateToDaemon()
		}
		if treeOps {
			m.noteDerivedTrees(unsentBefore)
			m.sendTreeOps()
		}
	}()
	defer m.noteFocusGiven(state)

	focusBefore := ""
	if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		focusBefore = m.Windows[m.FocusedWindow].ID
	}
	held := m.heldPanes()

	incomingByID := make(map[string]*session.WindowState)
	for i := range state.Windows {
		ws := &state.Windows[i]
		incomingByID[ws.ID] = ws
	}

	existingByID := make(map[string]*terminal.Window)
	for _, w := range m.Windows {
		existingByID[w.ID] = w
	}

	newWindows := make([]*terminal.Window, 0, len(state.Windows))
	var created []*terminal.Window
	var floated, unfloated []*terminal.Window
	var unzoomed []*terminal.Window

	for _, ws := range state.Windows {
		if existingWindow, exists := existingByID[ws.ID]; exists {
			wasFloating := existingWindow.IsFloating
			wasZoomed := existingWindow.Zoomed
			m.updateWindowFromState(existingWindow, &ws)
			if existingWindow.IsFloating != wasFloating {
				if existingWindow.IsFloating {
					floated = append(floated, existingWindow)
				} else {
					unfloated = append(unfloated, existingWindow)
				}
			}
			if wasZoomed && !existingWindow.Zoomed {
				unzoomed = append(unzoomed, existingWindow)
			}
			newWindows = append(newWindows, existingWindow)
			delete(existingByID, ws.ID)
		} else {
			newWindow := m.createWindowFromSync(&ws)
			if newWindow != nil {
				newWindows = append(newWindows, newWindow)
				created = append(created, newWindow)
			}
		}
	}

	var removed []int
	for _, w := range existingByID {
		removed = append(removed, m.GetWindowIntID(w.ID))
		m.closeWindowFromSync(w)
	}

	m.Windows = newWindows
	if !m.followHeldPanes(held) {
		defer m.endLostGesture()
	}

	if len(m.MultifocusSet) > 0 {
		present := make(map[string]bool, len(m.Windows))
		for _, w := range m.Windows {
			present[w.ID] = true
		}
		for id := range m.MultifocusSet {
			if !present[id] {
				delete(m.MultifocusSet, id)
			}
		}
		if len(m.MultifocusSet) == 0 {
			m.MultifocusSet = nil
		}
	}

	newerState := state.Version > m.DaemonStateVersion
	adoptTopology := newerState || fromPeer

	renamed := state.Name != "" && m.SessionName != "" && state.Name != m.SessionName
	m.SessionName = state.Name
	if renamed {
		m.SyncDockContext()
	}
	m.adoptSessionLabels(state)
	m.DaemonStateVersion = state.Version
	previousWorkspace := m.CurrentWorkspace
	m.CurrentWorkspace = clampWorkspace(state.CurrentWorkspace)
	m.MasterRatio = state.MasterRatio
	tilingWasOn := m.AutoTiling
	m.AutoTiling = state.AutoTiling
	if session.IsScratchWorkspace(previousWorkspace) {
		if m.CurrentWorkspace == max(m.scratchBase, 1) {
			m.scratchBaseTiling = m.AutoTiling
			m.CurrentWorkspace, m.AutoTiling = previousWorkspace, true
		} else {
			m.scratchBaseTiling = m.AutoTiling
			m.leaveScratchWorkspace()
		}
	}
	workspaceChanged := previousWorkspace != m.CurrentWorkspace
	m.adoptWorkspaceMasterRatio(state)
	m.adoptWorkspaceStackRatio(state)
	m.adoptWorkspaceMasterSplits(state)
	m.adoptWorkspaceLayoutMode(state)
	m.adoptWorkspaceHasCustom(state)
	masterRetile := m.adoptWorkspaceMasterLayout(state)

	m.FocusedWindow = -1
	if state.FocusedWindowID != "" {
		for i, w := range m.Windows {
			if w.ID == state.FocusedWindowID {
				m.FocusedWindow = i
				break
			}
		}
	}
	focusAfter := ""
	if m.FocusedWindow >= 0 {
		focusAfter = m.Windows[m.FocusedWindow].ID
	}
	focusChanged := focusAfter != focusBefore

	for _, w := range m.turnsWithinSync {
		if m.windowByID(w.ID) == w {
			m.noteAgentTurnWithin(w)
		}
	}
	m.turnsWithinSync = nil

	if m.FocusedWindow < 0 && m.Mode == TerminalMode {
		m.ChooseMode(WindowManagementMode, ModeCauseNoPane)
	}

	m.WorkspaceFocus = make(map[int]int)
	for workspace, windowID := range state.WorkspaceFocus {
		for i, w := range m.Windows {
			if w.ID == windowID {
				m.WorkspaceFocus[workspace] = i
				break
			}
		}
	}
	m.FocusHistory = session.CloneFocusHistory(state.FocusHistory)

	if !treeOps && adoptTopology && state.WindowToBSPID != nil {
		if m.WindowToBSPID == nil {
			m.WindowToBSPID = make(map[string]int, len(state.WindowToBSPID))
		}
		for id, intID := range state.WindowToBSPID {
			if _, ok := m.WindowToBSPID[id]; !ok {
				m.WindowToBSPID[id] = intID
			}
		}
		m.BSPIDToWindowID = make(map[int]string, len(m.WindowToBSPID))
		for id, intID := range m.WindowToBSPID {
			m.BSPIDToWindowID[intID] = id
		}
	}
	m.NextBSPWindowID = max(m.NextBSPWindowID, state.NextBSPWindowID)
	m.TilingScheme = layout.AutoScheme(state.TilingScheme)
	m.ApplyLayoutModeName(state.LayoutMode)
	m.adoptSidebarState(state)
	sidebarRetile := m.adoptSidebarVisibility(state)
	geometryChanged := m.adoptPaneGeometry(state)

	treeRetile := false
	if treeOps {
		treeRetile = m.adoptSessionTrees(state) && m.UseBSPLayout && !m.UseScrollingLayout
	} else if adoptTopology && state.WorkspaceTrees != nil && state.AutoTiling {
		m.WorkspaceTrees = make(map[int]*layout.BSPTree)
		for ws, serialized := range state.WorkspaceTrees {
			if serialized != nil {
				layoutSerialized := &layout.SerializedBSPTree{
					Root:         convertSessionBSPNode(serialized.Root),
					AutoScheme:   serialized.AutoScheme,
					DefaultRatio: serialized.DefaultRatio,
				}
				m.WorkspaceTrees[ws] = layoutSerialized.Deserialize()
			}
		}
	}
	if adoptTopology && state.WorkspaceScrollColumns != nil && state.AutoTiling {
		m.adoptScrollColumns(state.WorkspaceScrollColumns)
	}

	for _, w := range floated {
		intID := m.GetWindowIntID(w.ID)
		for _, tree := range m.WorkspaceTrees {
			if tree != nil {
				tree.RemoveWindow(intID)
			}
		}
		if m.UseScrollingLayout {
			m.ScrollingOnWindowRemoved(intID)
		}
	}
	for _, w := range unfloated {
		if w.Workspace != m.CurrentWorkspace || !m.AutoTiling {
			continue
		}
		if m.UseScrollingLayout {
			m.ScrollingOnWindowAdded(w)
			continue
		}
		if !m.UseBSPLayout {
			continue
		}
		if tree := m.WorkspaceTrees[m.CurrentWorkspace]; tree != nil {
			intID := m.GetWindowIntID(w.ID)
			if !slices.Contains(tree.GetAllWindowIDs(), intID) {
				m.AddWindowToBSPTree(w)
			}
		}
	}

	placedWindows := m.placeUnplacedWindows(state, created)
	placed := len(placedWindows) > 0
	if m.AutoTiling {
		for _, w := range placedWindows {
			w.Opening = true
		}
	}

	m.seedAdoptedWindows(created)

	switch {
	case m.AutoTiling && (len(created) > 0 || len(removed) > 0 || placed):
		m.adoptSyncedWindows(created, removed, placed, focusBefore)
	case placed:
		m.RaiseNewSurfaces(placedWindows)
		m.SyncStateToDaemon()
	}

	if placed {
		for _, w := range placedWindows {
			if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) && m.Windows[m.FocusedWindow] == w {
				m.ZoomFollowsFocus(m.FocusedWindow)
				break
			}
		}
	}

	m.adoptScrollStrip(state.ScrollStrip, focusChanged, focusChanged && slices.ContainsFunc(created, func(w *terminal.Window) bool {
		return w.ID == focusAfter
	}))

	workspaceRetile := workspaceChanged
	if workspaceRetile && m.AutoTiling && m.WorkspaceHasCustom[m.CurrentWorkspace] {
		workspaceRetile = false
		m.settleBorderMode(m.CurrentWorkspace)
	}

	zoomRetile := false
	m.settleSizes(func() {
		m.applyPopupRects(false)
		zoomRetile = m.applyZoomState(unzoomed)
		switch {
		case tilingWasOn && !m.AutoTiling:
			for _, w := range m.Windows {
				w.SetTiled(false)
			}
		case !tilingWasOn && m.AutoTiling:
			m.settleBorderMode(m.CurrentWorkspace)
		}
		if m.AutoTiling && len(m.Windows) > 0 && len(created) == 0 && len(removed) == 0 &&
			(geometryChanged || workspaceRetile || zoomRetile || treeRetile || masterRetile || sidebarRetile ||
				m.tiledLayoutStale() || (treeOps && m.bspRectsOffTree())) {
			m.TileAllWindows()
		}
		if sidebarRetile && !m.AutoTiling {
			m.ClampWindowsToView()
		}
	})

	if state.DaemonLayout > m.daemonLayoutSeen {
		m.daemonLayoutSeen = state.DaemonLayout
		if treeRetile || zoomRetile {
			m.SyncStateToDaemon()
		}
	}

	m.settlePaneRequests()

	m.reconcilePaneStreams()

	if focusChanged {
		m.NotifyDockEvent(string(hooks.AfterFocusChange))
	}

	m.MarkAllDirty()
	return nil
}

func (m *OS) adoptWorkspaceMasterRatio(state *session.SessionState) {
	if m.WorkspaceMasterRatio == nil {
		m.WorkspaceMasterRatio = make(map[int]float64, len(state.WorkspaceMasterRatio))
	}
	maps.Copy(m.WorkspaceMasterRatio, state.WorkspaceMasterRatio)
}

func (m *OS) adoptWorkspaceStackRatio(state *session.SessionState) {
	if len(state.WorkspaceStackRatio) == 0 {
		return
	}
	if m.WorkspaceStackRatio == nil {
		m.WorkspaceStackRatio = make(map[int]float64, len(state.WorkspaceStackRatio))
	}
	maps.Copy(m.WorkspaceStackRatio, state.WorkspaceStackRatio)
}

func (m *OS) adoptWorkspaceMasterSplits(state *session.SessionState) {
	if len(state.WorkspaceMasterSplits) == 0 {
		return
	}
	if m.WorkspaceMasterSplits == nil {
		m.WorkspaceMasterSplits = make(map[int]layout.MasterSplits, len(state.WorkspaceMasterSplits))
	}
	for ws, sp := range state.WorkspaceMasterSplits {
		m.WorkspaceMasterSplits[ws] = sp.Clone()
	}
}

func (m *OS) adoptWorkspaceLayoutMode(state *session.SessionState) {
	if len(state.WorkspaceLayoutModes) == 0 {
		return
	}
	if m.WorkspaceLayoutMode == nil {
		m.WorkspaceLayoutMode = make(map[int]string, len(state.WorkspaceLayoutModes))
	}
	maps.Copy(m.WorkspaceLayoutMode, state.WorkspaceLayoutModes)
}

func (m *OS) adoptWorkspaceHasCustom(state *session.SessionState) {
	if m.WorkspaceHasCustom == nil {
		m.WorkspaceHasCustom = make(map[int]bool, len(state.WorkspaceHasCustom))
	}
	maps.Copy(m.WorkspaceHasCustom, state.WorkspaceHasCustom)
}

func (m *OS) adoptSidebarState(state *session.SessionState) {
	m.SidebarCollapsed = state.SidebarCollapsed
}

func (m *OS) updateWindowFromState(w *terminal.Window, ws *session.WindowState) {
	adoptGeometry := !ws.Unplaced && !ws.Zoomed && !ws.Popup
	if adoptGeometry && w == m.LiveWindowDrag() {
		adoptGeometry = false
	}

	if adoptGeometry {
		if anim := m.snapInFlight(w); anim != nil {
			anim.Retarget(ws.X, ws.Y, ws.Width, ws.Height, 0, m.layoutClock())
			adoptGeometry = false
		}
	}

	sizeChanged := adoptGeometry && (w.Width != ws.Width || w.Height != ws.Height)

	if !w.DaemonMode || w.PTYID == "" || !m.SubscribedPTYs[w.PTYID] {
		w.SetTitle(ws.Title)
	}
	adoptWindowHost(w, ws.Host)
	m.takeDaemonCwd(w, ws)
	m.takeDaemonDnD(w, ws)
	m.noteHostEnded(ws)
	w.HostLink = ws.HostLink
	w.ScreenStopped = ws.ScreenStopped
	w.Recording = ws.Recording
	w.CustomName = ws.CustomName
	if adoptGeometry {
		w.X = ws.X
		w.Y = ws.Y
		w.Width = ws.Width
		w.Height = ws.Height
	}
	w.Z = ws.Z
	w.Workspace = ws.Workspace
	w.Minimized = ws.Minimized
	w.MinimizedAt = ws.MinimizedAt
	w.IsFloating = ws.IsFloating
	w.Zoomed = ws.Zoomed
	w.IsPopup = ws.Popup
	w.IsScratch = ws.Scratch
	w.ScratchName = ws.ScratchName
	w.Rail = ws.Rail
	w.Stack, w.StackIndex = ws.Stack, ws.StackIndex
	w.Kind, w.URI = ws.Kind, ws.URI
	w.PopupWidth = ws.PopupWidth
	w.PopupHeight = ws.PopupHeight
	w.PopupDim = ws.PopupDim
	w.PreZoomX = ws.PreZoomX
	w.PreZoomY = ws.PreZoomY
	w.PreZoomWidth = ws.PreZoomW
	w.PreZoomHeight = ws.PreZoomH
	w.PreMinimizeX = ws.PreMinimizeX
	w.PreMinimizeY = ws.PreMinimizeY
	w.PreMinimizeWidth = ws.PreMinimizeW
	w.PreMinimizeHeight = ws.PreMinimizeH
	if !m.SubscribedPTYs[w.PTYID] {
		w.SetAltScreen(ws.IsAltScreen)
	}
	fromState, fromQuiet, fromKind := w.AgentState, w.AgentQuiet, w.AgentKind
	prevSeq := w.AgentCompletionSeq
	copyAgentWire(w, ws)
	shown := string(ws.ShownAgentState())
	if shown == w.AgentState && ws.CompletionSeq > prevSeq {
		if m.applyingPeerSync {
			m.turnsWithinSync = append(m.turnsWithinSync, w)
		} else {
			m.noteAgentTurnWithin(w)
		}
	}
	m.noteAgentState(w, shown)
	m.noteQuietLifted(w, fromState, fromQuiet, fromKind)
	m.markHostProgramStatus()
	w.ForegroundCmd = ws.ForegroundCmd
	m.noteMachineOffer(w, ws.SSHOffer)
	if w.PTYID != "" {
		w.ShellPgid = ws.ShellPID
	}

	if renderTraceEnabled && !sizeChanged {
		traceSync(w, ws.IsAltScreen, false, w.Width, w.Height, "SetAltScreen; no resize")
	}

	if sizeChanged {
		w.Resize(ws.Width, ws.Height)

		if m.ScriptMode && w.DaemonMode && w.PTYID != "" && m.DaemonClient != nil {
			if state, err := m.DaemonClient.GetTerminalState(w.PTYID, m.SnapshotScrollback, m.snapshotHave(w)); err == nil && state != nil {
				m.restoreTerminalContent(w, state)
			}
			w.HasNewOutput.Store(true)
		}

		w.InvalidateCache()
		w.MarkContentDirty()

		if renderTraceEnabled {
			traceSync(w, ws.IsAltScreen, true, w.ContentWidth(), w.ContentHeight(),
				"SetAltScreen; Terminal.Resize under LockIO; cache invalidated")
		}
	}
}

func adoptWindowState(window *terminal.Window, ws session.WindowState) {
	window.CustomName = ws.CustomName
	window.Workspace = ws.Workspace
	window.Minimized = ws.Minimized
	window.MinimizedAt = ws.MinimizedAt
	window.IsFloating = ws.IsFloating
	window.Zoomed = ws.Zoomed
	window.IsPopup = ws.Popup
	window.IsScratch = ws.Scratch
	window.ScratchName = ws.ScratchName
	window.Rail = ws.Rail
	window.Stack, window.StackIndex = ws.Stack, ws.StackIndex
	window.Kind, window.URI = ws.Kind, ws.URI
	window.PopupWidth = ws.PopupWidth
	window.PopupHeight = ws.PopupHeight
	window.PopupDim = ws.PopupDim
	window.PreZoomX = ws.PreZoomX
	window.PreZoomY = ws.PreZoomY
	window.PreZoomWidth = ws.PreZoomW
	window.PreZoomHeight = ws.PreZoomH
	window.PreMinimizeX = ws.PreMinimizeX
	window.PreMinimizeY = ws.PreMinimizeY
	window.PreMinimizeWidth = ws.PreMinimizeW
	window.PreMinimizeHeight = ws.PreMinimizeH
	window.SetAltScreen(ws.IsAltScreen)
	window.AgentState = string(ws.ShownAgentState())
	copyAgentWire(window, &ws)
	window.ForegroundCmd = ws.ForegroundCmd
	if window.PTYID != "" {
		window.ShellPgid = ws.ShellPID
	}
}

func (m *OS) newWindowFromState(ws *session.WindowState) *terminal.Window {
	window := terminal.NewDaemonWindow(
		ws.ID,
		ws.Title,
		ws.X, ws.Y,
		ws.Width, ws.Height,
		ws.Z,
		ws.PTYID,
		m.PTYDataChan,
		m.Settings.ScrollbackLines,
	)
	if m.streamTap != nil {
		window.DiscardScrollback()
		window.KeepModesOnly()
	}
	adoptWindowHost(window, ws.Host)
	m.takeDaemonCwd(window, ws)
	m.takeDaemonDnD(window, ws)
	m.noteHostEnded(ws)
	window.HostLink = ws.HostLink
	window.ScreenStopped = ws.ScreenStopped
	window.Recording = ws.Recording

	caps := m.hostCaps()
	if caps.CellWidth > 0 && caps.CellHeight > 0 {
		window.SetCellPixelDimensions(caps.CellWidth, caps.CellHeight)
	}

	adoptWindowState(window, *ws)
	m.noteMachineOffer(window, ws.SSHOffer)

	m.installPassthroughs(window)
	m.setupCwdWatch(window)
	return window
}

func (m *OS) createWindowFromSync(ws *session.WindowState) *terminal.Window {
	if ws.ID == "" || ws.PTYID == "" {
		return nil
	}

	window := m.newWindowFromState(ws)

	if m.DaemonClient != nil {
		m.wireDaemonPTY(window, func() { m.primePaneFromDaemon(window) })
		window.EnableCallbacks()
	}

	m.FireHook(hooks.AfterNewWindow, window.ID, window.Title())

	return window
}

func (m *OS) closeWindowFromSync(w *terminal.Window) {
	m.clearNvimNavigation(w.ID)
	m.dndWindowClosed(w.ID)
	m.pipSourceClosed(w)
	if m.DaemonClient != nil && w.PTYID != "" {
		m.unsubscribeFromPTY(w)
	}

	if m.WindowToBSPID != nil {
		intID := m.GetWindowIntID(w.ID)
		delete(m.WindowToBSPID, w.ID)
		if m.BSPIDToWindowID != nil {
			delete(m.BSPIDToWindowID, intID)
		}
	}

	if m.SixelPassthrough != nil {
		m.SixelPassthrough.ClearWindow(w.ID)
	}
	m.kittySymbols.forget(w.ID)
	if m.KittyPassthrough != nil {
		m.KittyPassthrough.OnWindowClose(w.ID)
		if data := m.KittyPassthrough.FlushPending(); len(data) > 0 {
			m.KittyPassthrough.WriteToHost(data)
		}
	}

	if len(m.Animations) > 0 {
		kept := make([]*ui.Animation, 0, len(m.Animations))
		for _, anim := range m.Animations {
			if anim.Window != w {
				kept = append(kept, anim)
			}
		}
		m.Animations = kept
	}

	w.Close()
}

func (m *OS) placeUnplacedWindows(state *session.SessionState, firstSeen []*terminal.Window) []*terminal.Window {
	byID := make(map[string]*terminal.Window, len(firstSeen))
	for _, w := range firstSeen {
		byID[w.ID] = w
	}

	var placed []*terminal.Window
	for i := range state.Windows {
		if !state.Windows[i].Unplaced {
			continue
		}
		w := byID[state.Windows[i].ID]
		if w == nil {
			continue
		}
		x, y, width, height := m.NewWindowPlacement()
		if w.IsPopup {
			x, y, width, height = m.popupRect(w)
		} else if bw, bh, tiled, ok := m.newPaneBox(w); ok && w.Workspace == m.CurrentWorkspace && !w.IsFloating {
			width, height = bw, bh
			w.Tiled = tiled
		}
		w.X, w.Y = x, y
		w.Resize(width, height)
		w.InvalidateCache()
		placed = append(placed, w)
	}
	return placed
}

func (m *OS) adoptSyncedWindows(created []*terminal.Window, removed []int, placed bool, focusBefore string) {
	if len(created) == 0 && len(removed) == 0 && !placed {
		return
	}

	if m.UseScrollingLayout {
		for _, intID := range removed {
			m.ScrollingOnWindowRemoved(intID)
		}
		for _, w := range created {
			if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.IsFloating {
				m.ScrollingOnWindowAdded(w)
			}
		}
		if len(created) > 0 {
			m.CancelPendingSplit()
		}
	} else {
		for _, tree := range m.WorkspaceTrees {
			if tree == nil {
				continue
			}
			for _, intID := range removed {
				tree.RemoveWindow(intID)
			}
		}
		for ws, tree := range m.WorkspaceTrees {
			if tree != nil && tree.IsEmpty() {
				m.WorkspaceTrees[ws] = nil
			}
		}

		if len(created) == 1 {
			m.insertSyncedWindow(created[0], focusBefore)
		} else if len(created) > 1 {
			m.CancelPendingSplit()
		}
	}

	m.TileAllWindows()
	m.SyncStateToDaemon()
}

func (m *OS) insertSyncedWindow(win *terminal.Window, focusBefore string) {
	dir := m.pendingSplitDir
	targetID := m.pendingSplitTarget
	m.CancelPendingSplit()
	fromPreselection := false
	if targetID == "" && dir == layout.PreselectionNone && m.PreselectionDir != layout.PreselectionNone {
		dir = m.PreselectionDir
		fromPreselection = true
	}
	if targetID == "" {
		targetID = focusBefore
	}

	if win == nil || targetID == "" || !m.UseBSPLayout || m.UseScrollingLayout {
		return
	}
	if win.Workspace != m.CurrentWorkspace || win.Minimized || win.IsFloating {
		return
	}
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil || tree.IsEmpty() {
		return
	}
	windowIntID := m.GetWindowIntID(win.ID)
	if tree.HasWindow(windowIntID) {
		return
	}
	targetIntID := m.GetWindowIntID(targetID)
	if !tree.HasWindow(targetIntID) {
		return
	}
	if dir != layout.PreselectionNone {
		tree.InsertWindowWithPreselection(windowIntID, targetIntID, dir, m.GetBSPBounds(), m.separatorGap())
		if fromPreselection {
			m.PreselectionDir = layout.PreselectionNone
		}
		return
	}
	tree.InsertWindow(windowIntID, targetIntID, layout.SplitNone, 0.5, m.GetBSPBounds(), m.separatorGap())
}

func convertSessionBSPNode(node *session.SerializedBSPNode) *layout.SerializedNode {
	if node == nil {
		return nil
	}
	return &layout.SerializedNode{
		WindowID:   node.WindowID,
		SplitType:  node.SplitType,
		SplitRatio: node.SplitRatio,
		Left:       convertSessionBSPNode(node.Left),
		Right:      convertSessionBSPNode(node.Right),
	}
}

// RestoreTerminalStates fetches and restores terminal content (screen + scrollback)
// from the daemon for all windows. This should be called after RestoreFromState().
func (m *OS) RestoreTerminalStates() error {
	if m.DaemonClient == nil {
		return nil
	}

	for _, w := range m.Windows {
		if w.DaemonMode && w.PTYID != "" {
			state, err := m.DaemonClient.GetTerminalState(w.PTYID, m.SnapshotScrollback, m.snapshotHave(w))
			if err != nil {
				m.LogError("Failed to get terminal state for PTY %s: %v", shortID(w.PTYID), err)
				continue
			}

			if state != nil && w.Terminal != nil {
				w.ResizeEmulatorToSnapshot(state.Width, state.Height)
				m.restoreTerminalContent(w, state)
				if m.RestoredStreamSeq == nil {
					m.RestoredStreamSeq = make(map[string]int64)
				}
				m.RestoredStreamSeq[w.PTYID] = state.Seq
				m.LogInfo("Restored terminal state for window %s (%dx%d, %d scrollback lines)",
					shortID(w.ID), state.Width, state.Height, state.ScrollbackLen)

				w.SeedAnnouncedSize(state.Width, state.Height)
			}
		}
	}

	return nil
}

// SyncDaemonPTYDimensions ensures all daemon PTYs are resized to match their window dimensions.
// This must be called AFTER SetupPTYOutputHandlers so that DaemonResizeFunc is available.
// This fixes the issue where PTY dimensions become out of sync after detach/reattach.
func (m *OS) SyncDaemonPTYDimensions() {
	m.settleSizes(func() { m.syncDaemonPTYDimensions() })
}

func (m *OS) syncDaemonPTYDimensions() {
	for _, w := range m.Windows {
		if w.DaemonMode && w.DaemonResizeFunc != nil && m.snapInFlight(w) == nil {
			termWidth := w.ContentWidth()
			termHeight := w.ContentHeight()

			if aw, ah := w.AnnouncedSize(); termWidth == aw && termHeight == ah {
				continue
			}

			if err := w.DaemonResizeFunc(termWidth, termHeight); err != nil {
				m.LogWarn("Failed to sync PTY dimensions for window %s: %v", shortID(w.ID), err)
			} else {
				w.SeedAnnouncedSize(termWidth, termHeight)
				m.LogInfo("Synced daemon PTY dimensions for window %s (%dx%d)",
					shortID(w.ID), termWidth, termHeight)
			}

			if w.Terminal != nil && !w.StreamOwnsSize() {
				w.LockIO()
				if w.Terminal != nil {
					w.Terminal.Resize(termWidth, termHeight)
				}
				w.UnlockIO()
			}
		}
	}
}

// TriggerAltScreenRedraws forces alt screen apps to redraw.
// This must be called AFTER SetupPTYOutputHandlers so that DaemonResizeFunc is available.
// For alt screen apps (vim, htop, etc.), this invalidates caches and triggers re-render.
func (m *OS) TriggerAltScreenRedraws() {
	for _, w := range m.Windows {
		if w.DaemonMode && w.IsAltScreen() {
			w.InvalidateCache()
			w.MarkContentDirty()

			m.LogInfo("Invalidated caches for alt screen window %s", shortID(w.ID))
		}
	}

	m.MarkAllDirty()
}

func (m *OS) restoreTerminalContent(w *terminal.Window, state *session.TerminalState) {
	if w.Terminal == nil || state == nil {
		return
	}

	w.DiscardPendingOutput()

	m.tapSnapshot(w.PTYID, state)

	w.LockIO()
	session.ApplyTerminalState(w.Terminal, state)
	w.UnlockIO()

	if state.IsAltScreen {
		m.LogInfo("Restored alt screen mode for window %s", shortID(w.ID))
	}
	if len(state.Modes) > 0 {
		m.LogInfo("Restored %d terminal modes for window %s", len(state.Modes), shortID(w.ID))
	}

	w.SetAltScreen(state.IsAltScreen)
	m.LogInfo("Set window IsAltScreen=%v for window %s", state.IsAltScreen, shortID(w.ID))

	if renderTraceEnabled {
		note := "restore: SetAltScreen only"
		if state.IsAltScreen {
			note = "restore: RestoreAltScreenMode(true) + SetAltScreen"
		}
		traceSync(w, state.IsAltScreen, false, state.Width, state.Height, note)
	}

	w.MarkContentDirty()
}

// SetupPTYOutputHandlers sets up PTY output handlers for all daemon-mode windows.
// This should be called after RestoreFromState() when attaching to a session.
// Only subscribes to PTYs for windows in the current workspace (visibility optimization).
func (m *OS) SetupPTYOutputHandlers() error {
	if m.DaemonClient == nil {
		m.LogInfo("[SETUP] SetupPTYOutputHandlers: no daemon client")
		return nil
	}

	m.SubscribedPTYs = make(map[string]bool)
	m.paneRequests = nil

	m.LogInfo("[SETUP] SetupPTYOutputHandlers: setting up handlers for %d windows", len(m.Windows))

	for i, w := range m.Windows {
		m.LogInfo("[SETUP] Window %d: DaemonMode=%v, PTYID=%s, Workspace=%d", i, w.DaemonMode, w.PTYID, w.Workspace)
		if w.DaemonMode && w.PTYID != "" {
			m.wireDaemonPTY(w, func() { m.subscribeToPTY(w, m.RestoredStreamSeq[w.PTYID]) })
		}
	}

	return nil
}

func (m *OS) wireDaemonPTY(window *terminal.Window, subscribe func()) {
	ptyID := window.PTYID

	window.DaemonWriteFunc = func(data []byte) error {
		if m.daemonWaiting.Load() {
			return nil
		}
		return m.DaemonClient.WritePTY(ptyID, data)
	}
	window.DaemonResizeFunc = func(width, height int) error {
		return m.DaemonClient.ResizePTY(ptyID, width, height)
	}

	if window.Terminal != nil {
		window.Terminal.SetKittyUploadAnswers(false)
	}
	window.StartDaemonResponseReader()

	if window.Workspace == m.CurrentWorkspace {
		subscribe()
	}

	windowID := window.ID
	m.DaemonClient.OnPTYClosed(ptyID, func() {
		m.queueWindowExit(windowID)
	})
}

func (m *OS) primePaneFromDaemon(window *terminal.Window) {
	if m.DaemonClient == nil || window.PTYID == "" {
		return
	}

	window.DrainPendingOutput()

	state, err := m.DaemonClient.GetTerminalState(window.PTYID, m.SnapshotScrollback, m.snapshotHave(window))
	if err != nil || state == nil {
		m.subscribeToPTY(window, 0)
		return
	}

	if state.Width != window.ContentWidth() || state.Height != window.ContentHeight() {
		window.SeedAnnouncedSize(state.Width, state.Height)
		window.Resize(window.Width, window.Height)
		if fresh, err := m.DaemonClient.GetTerminalState(window.PTYID, m.SnapshotScrollback, m.snapshotHave(window)); err == nil && fresh != nil {
			state = fresh
		}
	}

	window.ResizeEmulatorToSnapshot(state.Width, state.Height)

	m.restoreTerminalContent(window, state)
	m.subscribeToPTY(window, state.Seq)
}

func (m *OS) subscribeToPTY(window *terminal.Window, fromSeq int64) {
	if m.DaemonClient == nil || window.PTYID == "" {
		return
	}

	ptyID := window.PTYID

	if m.SubscribedPTYs[ptyID] {
		return
	}

	m.LogInfo("[SUBSCRIBE] Subscribing to PTY %s for window %s", shortID(ptyID), shortID(window.ID))
	tap := m.streamTap
	if tap == nil {
		m.DaemonClient.OnPTYResized(ptyID, window.ResizeFromStream)
	} else {
		m.DaemonClient.OnPTYResized(ptyID, func(w, h int) {
			tap.Resized(ptyID, w, h)
			window.ResizeFromStream(w, h)
		})
		if fromSeq == 0 {
			tap.Snapshot(ptyID, nil)
		}
	}
	window.SetStreamOwnsSize(true)
	from, skip := fromSeq, int64(0)
	if r, ok := tap.(StreamResumer); ok && fromSeq > 0 {
		if at := r.ResumeFrom(ptyID, fromSeq); at > 0 && at < fromSeq {
			from, skip = at, fromSeq-at
		}
	}
	err := m.DaemonClient.SubscribePTY(ptyID, from, from > 0, func(data []byte) {
		if tap != nil {
			tap.Output(ptyID, data)
		}
		if skip > 0 {
			n := min(skip, int64(len(data)))
			skip -= n
			data = data[n:]
			if len(data) == 0 {
				return
			}
		}
		window.WriteOutputAsync(data)
	})
	if err != nil {
		window.SetStreamOwnsSize(false)
		m.LogError("Failed to subscribe to PTY %s: %v", shortID(ptyID), err)
	} else {
		m.SubscribedPTYs[ptyID] = true
		m.LogInfo("[SUBSCRIBE] Successfully subscribed to PTY %s", shortID(ptyID))
	}

}

func (m *OS) unsubscribeFromPTY(window *terminal.Window) {
	if m.DaemonClient == nil || window.PTYID == "" {
		return
	}

	ptyID := window.PTYID

	if !m.SubscribedPTYs[ptyID] {
		return
	}
	if m.pipKeepsStream(window) || m.cockpitKeepsStream(window) || m.railPaneKeepsStream(window) {
		return
	}

	m.LogInfo("[UNSUBSCRIBE] Unsubscribing from PTY %s for window %s", shortID(ptyID), shortID(window.ID))
	m.DaemonClient.UnsubscribePTY(ptyID)
	delete(m.SubscribedPTYs, ptyID)
	window.SetStreamOwnsSize(false)
}

// SubscribeWorkspaceWindows subscribes to PTY output for all windows in the specified workspace.
// Also fetches terminal state for windows that need to be populated.
func (m *OS) SubscribeWorkspaceWindows(workspace int) {
	if m.DaemonClient == nil {
		return
	}

	m.LogInfo("[WORKSPACE] Subscribing to windows in workspace %d", workspace)

	for _, w := range m.Windows {
		if w.DaemonMode && w.PTYID != "" && w.Workspace == workspace {
			if !m.SubscribedPTYs[w.PTYID] {
				m.primePaneFromDaemon(w)
			}
		}
	}
}

func (m *OS) reconcilePaneStreams() {
	if m.DaemonClient == nil {
		return
	}
	for _, w := range m.Windows {
		if w == nil || !w.DaemonMode || w.PTYID == "" {
			continue
		}
		if w.Workspace == m.CurrentWorkspace {
			if !m.SubscribedPTYs[w.PTYID] {
				m.primePaneFromDaemon(w)
			}
			continue
		}
		m.unsubscribeFromPTY(w)
	}
}

// UnsubscribeWorkspaceWindows unsubscribes from PTY output for all windows in the specified workspace.
func (m *OS) UnsubscribeWorkspaceWindows(workspace int) {
	if m.DaemonClient == nil {
		return
	}

	m.LogInfo("[WORKSPACE] Unsubscribing from windows in workspace %d", workspace)

	for _, w := range m.Windows {
		if w.DaemonMode && w.PTYID != "" && w.Workspace == workspace {
			m.unsubscribeFromPTY(w)
		}
	}
}

// SyncStateToDaemon sends the current state to the daemon.
// This should be called after state-changing operations.
//
// A state that says exactly what the last one said is not sent. The callers are
// unconditional on purpose (the input handler syncs after every keystroke,
// every click and every wheel event, so that nothing a user does can go
// unrecorded), and the great majority of those events change nothing the
// daemon holds. Each one that is sent costs the daemon a merge and every other
// attached client a full state application and a redraw, which is what made
// typing on one client visibly disturb another.
//
// Suppressed at the source rather than filtered at each consumer: the daemon
// has its own guard for the same reason, but a message not sent is the only one
// that costs nothing anywhere.
func (m *OS) SyncStateToDaemon() {
	if m.DaemonClient == nil || !m.IsDaemonSession {
		return
	}

	if m.applyingPeerSync {
		m.syncAnswerOwed = true
		return
	}

	if m.daemonWindowIntent {
		m.daemonWindowIntent = false
		return
	}

	m.seedMasterLayout()
	m.seedSidebar()

	state := m.BuildSessionState()
	if m.treeOpsOn() {
		if !m.sendTreeOps() {
			return
		}
		state.WorkspaceTrees = nil
		state.WindowToBSPID = nil
		state.NextBSPWindowID = 0
	}
	fp := session.StateFingerprint(state)
	if m.syncedFPSet && m.syncedFP == fp {
		return
	}
	key := focusKey(state)
	state.FocusUnmoved = m.focusGivenSet && key == m.focusGiven
	state.FocusByCommand = m.commandDriving || m.commandFocusMoved

	if err := m.DaemonClient.UpdateState(state); err != nil {
		m.LogError("Failed to sync state to daemon: %v", err)
		return
	}
	m.syncedFP, m.syncedFPSet = fp, true
	m.layoutGenPushed = state.LayoutGen
	m.focusGiven, m.focusGivenSet = key, true
	m.commandFocusMoved = false
}

func focusKey(state *session.SessionState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d|%s", state.CurrentWorkspace, state.FocusedWindowID)
	for _, w := range slices.Sorted(maps.Keys(state.WorkspaceFocus)) {
		fmt.Fprintf(&b, "|%d=%s", w, state.WorkspaceFocus[w])
	}
	if s := state.ScrollStrip; s != nil {
		fmt.Fprintf(&b, "|x%d", s.ViewportX)
	}
	return b.String()
}

func (m *OS) noteFocusGiven(state *session.SessionState) {
	m.focusGiven, m.focusGivenSet = focusKey(state), true
}

func (m *OS) warnOnBuildMismatch() {
	if m.DaemonClient == nil || !m.IsDaemonSession || m.restartLater {
		return
	}
	clientBuild, daemonBuild := m.DaemonClient.BuildMismatch()
	if clientBuild == "" {
		return
	}
	m.LogWarn("Build mismatch: client %s, daemon %s", clientBuild, daemonBuild)
	if !m.DaemonRestartAvailable() {
		m.ShowNotification("The daemon runs "+daemonBuild+" and this window runs "+clientBuild+
			". Run 'tuios restart-server' on the machine that runs the daemon.", "warning", 0)
		return
	}
	how := "Click it to restart it."
	if key := contextMenuHint(m.KeybindRegistry, "prefix_jump_notif", &m.Settings, m.Mode); key != "" {
		how = key + " restarts it."
	}
	m.ShowNotificationFrom("The daemon runs "+daemonBuild+". "+how, "warning", 0, NotifTarget{DaemonRestart: true})
	if n := len(m.Notifications); n > 0 && isRestartNotice(m.Notifications[n-1].Target) {
		m.Notifications[n-1].Sticky = true
	}
}

func (m *OS) announceRestart() {
	if !m.afterRestart || m.DaemonClient == nil {
		return
	}
	m.afterRestart = false
	m.ShowNotification("The daemon restarted on "+m.DaemonClient.DaemonVersion()+". Your session is back.",
		"success", 2*m.Settings.NotificationDuration)
}

// AnnounceLayoutReserve tells the daemon what this client keeps for its own
// chrome, so the session can settle on a reserve that fits every client. It
// sends only when the answer has moved, and it says it through the message that
// already carries this client's viewport, so the two halves of "what box do the
// panes go in" cannot disagree for a frame.
//
// settleChrome calls it after every message, so every path that can move the
// chrome is covered: a key, a click, a palette entry, a config reload, a tape,
// a set-config from the command line, and a peer showing or hiding the rail.
// The answer is a pure function of this client's own state, so a call that
// finds it unmoved costs one comparison and sends nothing.
//
// The daemon answers with a session resize, and the layout this client works
// out from that answer is pushed. See the SessionResizeMsg case in update.go.
func (m *OS) AnnounceLayoutReserve() {
	if m.DaemonClient == nil || !m.IsDaemonSession {
		return
	}
	if !m.DaemonClient.SetOwnLayoutReserve(m.OwnLayoutReserve()) {
		return
	}
	if err := m.DaemonClient.NotifyTerminalSize(m.Width, m.Height); err != nil {
		m.LogError("Failed to announce layout reserve: %v", err)
	}
}

func (m *OS) settleChrome() {
	m.syncSidebarToSession()
	r := m.OwnLayoutReserve()
	w, h := m.GetRenderWidth(), m.GetRenderHeight()
	seen := m.chromeSeen
	m.chromeSeen.reserve, m.chromeSeen.width, m.chromeSeen.height = r, w, h
	if r == seen.reserve {
		return
	}
	if w == seen.width && h == seen.height && w > 0 && len(m.Windows) > 0 {
		if !m.AutoTiling {
			m.ClampWindowsToView()
		} else if m.tiledLayoutStale() {
			m.TileAllWindows()
		}
	}
	m.AnnounceLayoutReserve()
}

func (m *OS) forgetSyncedState() {
	m.syncedFPSet = false
}

// SendInputToDaemon sends input to a daemon-managed PTY.
func (m *OS) SendInputToDaemon(window *terminal.Window, data []byte) error {
	if m.DaemonClient == nil || !window.DaemonMode {
		return nil
	}

	return m.DaemonClient.WritePTY(window.PTYID, data)
}

func adoptWindowHost(w *terminal.Window, host string) {
	if w == nil {
		return
	}
	w.SetHost(host)
}

func startedByFromWire(ref *session.PaneRef) (sessionName, window string) {
	if ref == nil {
		return "", ""
	}
	return ref.Session, ref.Window
}

func copyAgentWire(w *terminal.Window, ws *session.WindowState) {
	w.AgentMessage = ws.AgentMessage
	w.AgentReason = ws.AgentReason
	w.AgentKind = ws.AgentKind
	w.AgentHarness = ws.AgentHarness
	w.StartedBySession, w.StartedByWindow = startedByFromWire(ws.StartedBy)
	w.AgentChild = ws.AgentLink != nil
	w.AgentMeta = agentMetaFromWire(w.AgentMeta, ws.AgentMeta)
	w.AgentQueued = ws.AgentQueued
	w.AgentDraftHeld = ws.AgentDraftHeld
	w.AgentSettled = ws.AgentSettled
	w.AgentQuiet = ws.AgentQuiet
	w.AgentSubagents = ws.AgentSubagents
	w.AgentSubagentList = subagentsFromWire(w.AgentSubagentList, ws.AgentSubagentList)
	w.ProgramStatus = programStatusFromWire(w.ProgramStatus, ws.ProgramStatus)
	w.AgentStateAt = ws.AgentStateAt
	w.AgentCompletionSeq = ws.CompletionSeq
}

func (m *OS) focusedWindowID() string {
	if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		return m.Windows[m.FocusedWindow].ID
	}
	return ""
}

func (m *OS) noteHostEnded(ws *session.WindowState) {
	if ws.HostEnded == "" || m.hostEndedTold[ws.ID] {
		return
	}
	if m.hostEndedTold == nil {
		m.hostEndedTold = make(map[string]bool)
	}
	m.hostEndedTold[ws.ID] = true
	name := ws.CustomName
	if name == "" {
		name = ws.Title
	}
	if name == "" {
		name = "A pane"
	}
	m.ShowNotification(printableTitle(name)+" on "+printableTitle(ws.Host)+" ended: "+ws.HostEnded+".", "warning", m.Settings.NotificationDuration)
}
