// Package app provides the core TUIOS application logic and window management.
package app

import (
	"fmt"
	"maps"
	"slices"
	"time"

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
	state := &session.SessionState{
		Name:             m.SessionName,
		CurrentWorkspace: m.CurrentWorkspace,
		MasterRatio:      m.MasterRatio,
		AutoTiling:       m.AutoTiling,
		Width:            m.GetRenderWidth(),
		Height:           m.GetRenderHeight(),
		WorkspaceFocus:   make(map[int]string),
		// Tell the daemon which of its versions this snapshot was built from, so
		// it can reconcile rather than let a stale push undo its own mutations.
		BaseVersion: m.DaemonStateVersion,
	}

	// Build map of window -> animation for quick lookup
	windowAnimations := make(map[*terminal.Window]*ui.Animation)
	for _, anim := range m.Animations {
		if anim != nil && anim.Window != nil && !anim.Complete {
			windowAnimations[anim.Window] = anim
		}
	}

	// Build window states
	state.Windows = make([]session.WindowState, len(m.Windows))
	for i, w := range m.Windows {
		// Start with current values
		x, y, width, height := w.X, w.Y, w.Width, w.Height

		// If window has an active animation, use the final (end) position
		// This ensures other clients see the target state immediately
		if anim, hasAnim := windowAnimations[w]; hasAnim {
			x = anim.EndX
			y = anim.EndY
			width = anim.EndWidth
			height = anim.EndHeight
		}

		state.Windows[i] = session.WindowState{
			ID:           w.ID,
			Title:        w.Title(),
			CustomName:   w.CustomName,
			X:            x,
			Y:            y,
			Width:        width,
			Height:       height,
			Z:            w.Z,
			Workspace:    w.Workspace,
			Minimized:    w.Minimized,
			PreMinimizeX: w.PreMinimizeX,
			PreMinimizeY: w.PreMinimizeY,
			PreMinimizeW: w.PreMinimizeWidth,
			PreMinimizeH: w.PreMinimizeHeight,
			PTYID:        w.PTYID,
			IsAltScreen:  w.IsAltScreen(), // Save alt screen state for mouse forwarding on restore
			IsFloating:   w.IsFloating,
		}
	}

	// Set focused window ID
	if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		state.FocusedWindowID = m.Windows[m.FocusedWindow].ID
	}

	// Build workspace focus map (window index -> window ID)
	for workspace, windowIdx := range m.WorkspaceFocus {
		if windowIdx >= 0 && windowIdx < len(m.Windows) {
			state.WorkspaceFocus[workspace] = m.Windows[windowIdx].ID
		}
	}

	// Serialize BSP trees for each workspace
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

	// Save window to BSP ID mapping
	if m.WindowToBSPID != nil {
		state.WindowToBSPID = make(map[string]int)
		maps.Copy(state.WindowToBSPID, m.WindowToBSPID)
	}
	state.NextBSPWindowID = m.NextBSPWindowID
	state.TilingScheme = int(m.TilingScheme)
	// The layout mode travels with the topology it selects between. Without it a
	// scrolling session reattached as a BSP one: the tree survived and the mode
	// that reads it did not.
	state.LayoutMode = m.LayoutModeName()
	state.NumWorkspaces = m.NumWorkspaces
	// The master ratio for every workspace this client holds one for, so a peer
	// that has never visited a workspace lays it out at the session's ratio rather
	// than at its own config's and pushes that back over everyone else's. The
	// workspace on screen is folded in from the live value: SaveCurrentLayout only
	// flushes that into the map on the way out of a workspace, so the map alone
	// would be one tune behind for exactly the workspace being tuned.
	state.WorkspaceMasterRatio = make(map[int]float64, len(m.WorkspaceMasterRatio)+1)
	maps.Copy(state.WorkspaceMasterRatio, m.WorkspaceMasterRatio)
	if m.AutoTiling {
		state.WorkspaceMasterRatio[m.CurrentWorkspace] = m.MasterRatio
	}
	if len(state.WorkspaceMasterRatio) == 0 {
		state.WorkspaceMasterRatio = nil
	}
	// The rail travels with the session it is drawn beside. See
	// SessionState.SidebarWidth.
	state.SidebarWidth = m.SidebarWidthPref
	state.SidebarCollapsed = m.SidebarCollapsed
	// The pane geometry inputs travel with the session because they are inputs
	// to every client's layout arithmetic. Always sent, never nil: nil on the
	// wire means a client too old to say, and this one can.
	state.PaneGeometry = &session.PaneGeometryState{
		SharedBorders:     m.SharedBorders,
		PaneGap:           m.PaneGap,
		ScrollColumnWidth: m.ScrollColumnWidth,
	}
	// Where the strip is scrolled to, on the workspace this state names. Shared
	// for the reason the focus is: the strip is one row of columns and this is a
	// place on it, so two clients holding one offset are looking at the same
	// place. See SessionState.ScrollStrip.
	state.ScrollStrip = m.ScrollStripState()

	return state
}

// convertBSPNode converts layout.SerializedNode to session.SerializedBSPNode
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

// clampWorkspace returns a valid workspace index. Workspaces are 1-based and
// SwitchToWorkspace refuses anything below 1, so a persisted or synced value of
// 0 must be normalized to 1 to keep every workspace reachable.
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

	m.LogInfo("[RESTORE] RestoreFromState: restoring %d windows", len(state.Windows))

	// Recorded before placeUnplacedWindows below, which is what makes every
	// window in the session look placed from here on.
	m.sessionUnarranged = stateUnarranged(state)

	// Adopting a whole session says nothing about what this client last pushed
	// to a daemon, and on a session switch it is a different session entirely.
	m.forgetSyncedState()

	m.SessionName = state.Name
	m.adoptSessionLabels(state)
	m.DaemonStateVersion = state.Version
	// Clamp to a valid workspace: SwitchToWorkspace rejects workspace < 1, so a
	// state carrying 0 (legacy, or a freshly created session with no windows)
	// would strand every subsequently created window on an unreachable workspace.
	m.CurrentWorkspace = clampWorkspace(state.CurrentWorkspace)
	m.MasterRatio = state.MasterRatio
	m.AutoTiling = state.AutoTiling
	// A whole session is being adopted, and on a session switch it is a different
	// session, so the ratios this client remembers are cleared rather than merged
	// with: they belong to the session being left.
	m.WorkspaceMasterRatio = make(map[int]float64, len(state.WorkspaceMasterRatio))
	m.adoptWorkspaceMasterRatio(state)
	m.adoptSidebarState(state)
	// The pane geometry inputs are the session's, adopted before the layout
	// below is computed so a joining client tiles with the session's arithmetic
	// rather than its own config's.
	m.adoptPaneGeometry(state)

	// Set effective dimensions from state - this is the min of all connected clients
	// as calculated by the daemon. This ensures a new client joining respects
	// the existing effective size even before receiving a SessionResizeMsg.
	// Also set Width/Height so that window scaling works correctly when the terminal
	// size changes - without this, oldWidth/oldHeight would be 0 and windows
	// would be clamped instead of scaled proportionally.
	if state.Width > 0 && state.Height > 0 {
		m.EffectiveWidth = state.Width
		m.EffectiveHeight = state.Height
		m.Width = state.Width
		m.Height = state.Height
		m.LogInfo("[RESTORE] Set size from state: %dx%d", state.Width, state.Height)
	}

	// The chrome reserve the session has agreed on, for the same reason and at
	// the same moment as the size: it is the other half of the box the panes go
	// in, and the layout below is computed against it. It comes off the client
	// rather than off the state because it is the daemon's answer, settled in
	// the attach reply, not anything a peer pushed.
	if m.DaemonClient != nil {
		m.SessionReserve = m.DaemonClient.SessionLayoutReserve()
	}

	// Clear existing windows
	for _, w := range m.Windows {
		w.Close()
	}
	m.Windows = nil

	// Create windows from state
	for i, ws := range state.Windows {
		m.LogInfo("[RESTORE] Creating window %d: ID=%s, PTYID=%s", i, shortID(ws.ID), shortID(ws.PTYID))
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
		if window == nil {
			m.LogError("Failed to create daemon window for %s", shortID(ws.ID))
			continue
		}

		caps := m.hostCaps()
		if caps.CellWidth > 0 && caps.CellHeight > 0 {
			window.SetCellPixelDimensions(caps.CellWidth, caps.CellHeight)
		}

		adoptWindowState(window, ws)

		// CRITICAL: Suppress callbacks during restoration to prevent race condition
		// where buffered PTY output overwrites the restored IsAltScreen state
		// Callbacks will be re-enabled in restoreTerminalContent() after state is fully restored
		window.DisableCallbacks()

		m.installPassthroughs(window)
		m.setupCwdWatch(window)

		m.Windows = append(m.Windows, window)
		m.LogInfo("[RESTORE] Window %d created: DaemonMode=%v, PTYID=%s", i, window.DaemonMode, shortID(window.PTYID))
	}

	// Restore focused window
	m.FocusedWindow = -1
	if state.FocusedWindowID != "" {
		for i, w := range m.Windows {
			if w.ID == state.FocusedWindowID {
				m.FocusedWindow = i
				break
			}
		}
	}
	// If no focused window matched, focus the first visible window in current workspace
	if m.FocusedWindow < 0 && len(m.Windows) > 0 {
		for i, w := range m.Windows {
			if w.Workspace == m.CurrentWorkspace && !w.Minimized {
				m.FocusedWindow = i
				break
			}
		}
	}

	// Start in terminal mode so input goes to the focused terminal immediately
	// (previously stayed in WM mode, causing typing to not work until click)
	if m.FocusedWindow >= 0 {
		m.Mode = TerminalMode
	}

	// Restore workspace focus (window ID -> window index)
	m.WorkspaceFocus = make(map[int]int)
	for workspace, windowID := range state.WorkspaceFocus {
		for i, w := range m.Windows {
			if w.ID == windowID {
				m.WorkspaceFocus[workspace] = i
				break
			}
		}
	}

	// Restore window to BSP ID mapping FIRST (before BSP trees)
	// This ensures getWindowIntID() returns correct IDs when we deserialize trees
	if state.WindowToBSPID != nil {
		m.WindowToBSPID = make(map[string]int)
		for k, v := range state.WindowToBSPID {
			m.WindowToBSPID[k] = v
			m.LogInfo("[RESTORE] WindowToBSPID: %s -> %d", shortID(k), v)
		}
	}
	m.NextBSPWindowID = state.NextBSPWindowID
	m.TilingScheme = layout.AutoScheme(state.TilingScheme)
	// Reattaching is the case this field exists for: the mode is what a user
	// most obviously notices losing, and it was the one part of the tiling state
	// that did not survive.
	m.ApplyLayoutModeName(state.LayoutMode)
	m.LogInfo("[RESTORE] NextBSPWindowID=%d, TilingScheme=%d, LayoutMode=%s", m.NextBSPWindowID, m.TilingScheme, m.LayoutModeName())

	// Restore BSP trees
	if state.WorkspaceTrees != nil && state.AutoTiling {
		m.WorkspaceTrees = make(map[int]*layout.BSPTree)
		for ws, serialized := range state.WorkspaceTrees {
			if serialized != nil {
				// Convert session.SerializedBSPTree to layout.SerializedBSPTree
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

	// Restore current workspace
	if state.CurrentWorkspace > 0 {
		m.CurrentWorkspace = state.CurrentWorkspace
	}

	// A client joining a scrolling session starts where the session is looking,
	// not at the left end of the strip. The strip is built here rather than left
	// to the first retile because that retile only ever reveals the focused
	// column, which is a different place from wherever the session has actually
	// scrolled to.
	if m.AutoTiling && m.UseScrollingLayout && state.ScrollStrip != nil {
		m.GetOrCreateScrollingLayout().ViewportX = state.ScrollStrip.ViewportX
	}

	// A window created while nothing was attached has never been placed by
	// anyone, and RestoredFromState below suppresses the first retile, so without
	// this it would render as a full-size box over the restored layout.
	m.placeUnplacedWindows(state, m.Windows)

	m.MarkAllDirty()
	m.LogInfo("[RESTORE] Restored session state: %d windows, FocusedWindow=%d, AutoTiling=%v, Workspace=%d", len(m.Windows), m.FocusedWindow, m.AutoTiling, m.CurrentWorkspace)

	// Mark that we restored from state - this prevents the first resize from retiling
	// and allows the layout to be preserved as the user left it
	m.RestoredFromState = true

	// If we have windows and a focused window, switch to terminal mode
	// This ensures mouse events are forwarded to terminals after restore
	if len(m.Windows) > 0 && m.FocusedWindow >= 0 {
		m.Mode = TerminalMode
		m.TerminalModeEnteredAt = time.Now()
	}

	return nil
}

// stateUnarranged reports whether anybody has ever laid this session out.
//
// The daemon has no viewport, so a window it creates on its own carries a
// placeholder box and says so with Unplaced. The first client push replaces
// that box with real coordinates and clears the flag. A session whose every
// window is still Unplaced has therefore never been arranged by a client, which
// is what tells a fresh session the daemon pre-populated apart from a session
// the user built. An empty session is unarranged for the same reason.
func stateUnarranged(state *session.SessionState) bool {
	for i := range state.Windows {
		if !state.Windows[i].Unplaced {
			return false
		}
	}
	return true
}

// ApplyStateSync applies a state update from another client.
// This handles window creation, deletion, and property updates.
func (m *OS) ApplyStateSync(state *session.SessionState) error {
	if state == nil {
		return nil
	}

	// The daemon now holds what arrived here, which is not necessarily what
	// this client last pushed, so the record of that push no longer describes
	// the daemon and must not be allowed to suppress the next one.
	m.forgetSyncedState()

	// Nothing may be pushed while this is being applied. See applyingPeerSync:
	// a layout worked out here because this client disagreed with the arriving
	// one is a local display decision, not news, and sending it is the edge that
	// would close the loop.
	m.applyingPeerSync = true
	m.syncAnswerOwed = false
	defer func() {
		m.applyingPeerSync = false
		if m.syncAnswerOwed {
			m.syncAnswerOwed = false
			// One push for the whole sync, after it has been applied, so the
			// answer that goes out is the settled one rather than a rectangle
			// from the middle of the fold.
			m.SyncStateToDaemon()
		}
	}()

	// Which window was focused before any of this was applied. It is read here
	// rather than beside the focus adoption below because the window list is
	// rebuilt in between, which leaves the old index pointing at whatever
	// happens to sit there now.
	focusBefore := ""
	if m.FocusedWindow >= 0 && m.FocusedWindow < len(m.Windows) {
		focusBefore = m.Windows[m.FocusedWindow].ID
	}

	// Build maps for efficient lookup
	incomingByID := make(map[string]*session.WindowState)
	for i := range state.Windows {
		ws := &state.Windows[i]
		incomingByID[ws.ID] = ws
	}

	existingByID := make(map[string]*terminal.Window)
	for _, w := range m.Windows {
		existingByID[w.ID] = w
	}

	// Build new window list in the order specified by incoming state
	newWindows := make([]*terminal.Window, 0, len(state.Windows))
	var created []*terminal.Window
	// Windows whose float state this sync moved. A float is a structural layout
	// change and tiling topology is not adopted from a client push (see
	// newerState below), so the structural half of the peer's toggle has to be
	// mirrored here once the whole list has been applied.
	var floated, unfloated []*terminal.Window

	for _, ws := range state.Windows {
		if existingWindow, exists := existingByID[ws.ID]; exists {
			// Update existing window
			wasFloating := existingWindow.IsFloating
			m.updateWindowFromState(existingWindow, &ws)
			if existingWindow.IsFloating != wasFloating {
				if existingWindow.IsFloating {
					floated = append(floated, existingWindow)
				} else {
					unfloated = append(unfloated, existingWindow)
				}
			}
			newWindows = append(newWindows, existingWindow)
			delete(existingByID, ws.ID) // Mark as handled
		} else {
			// Create new window from another client
			newWindow := m.createWindowFromSync(&ws)
			if newWindow != nil {
				newWindows = append(newWindows, newWindow)
				created = append(created, newWindow)
			}
		}
	}

	// Close windows that were deleted by other client
	var removed []int
	for _, w := range existingByID {
		removed = append(removed, m.getWindowIntID(w.ID))
		m.closeWindowFromSync(w)
	}

	// Update window list
	m.Windows = newWindows

	// MultifocusSet is keyed by window ID and survives the rebuild for windows
	// that still exist; prune IDs no longer present in the synced window list.
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

	// The BSP tree, the window->int-ID map and the split scheme are computed by
	// clients, never by the daemon's own mutations (AddDaemonWindow and friends do
	// not touch them). The daemon only stores what a client last synced and echoes
	// it back, so a sync that is not strictly newer than the one this client
	// already applied carries this client's own tiling state, often lagging a
	// mutation this client has since made. Adopting that echo wipes the fresh tree
	// and reassigns int IDs, which rebuilds the whole layout from scratch and drops
	// a forced split direction (ctrl+b | / -). Version counts daemon-side
	// mutations only, so it is exactly the right gate: adopt tiling topology only
	// when the daemon has advanced past what this client last saw.
	newerState := state.Version > m.DaemonStateVersion

	// Update global state
	m.SessionName = state.Name
	m.adoptSessionLabels(state)
	m.DaemonStateVersion = state.Version
	// A workspace switch adopted from a sync moves which panes are laid out, and
	// the panes it brings on screen were last laid out whenever their workspace
	// was last shown here - under whatever shared-borders setting was in force
	// then. The rectangles in the sync are right, so nothing reads as stale, and
	// the only thing wrong is the border allowance each pane keeps: two rows and
	// two columns of every guest on the workspace, on this client alone. It is
	// the same shape as geometryChanged below, so it is folded into the same
	// retile.
	previousWorkspace := m.CurrentWorkspace
	m.CurrentWorkspace = clampWorkspace(state.CurrentWorkspace)
	workspaceChanged := previousWorkspace != m.CurrentWorkspace
	m.MasterRatio = state.MasterRatio
	m.AutoTiling = state.AutoTiling
	m.adoptWorkspaceMasterRatio(state)

	// Update focused window index
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

	// Terminal mode with nothing focused is a dead end: keystrokes have no
	// terminal to reach. Closing the last window used to drop back to window
	// management as part of the local close; now that closing is the daemon's,
	// this is where that happens.
	if m.FocusedWindow < 0 && m.Mode == TerminalMode {
		m.Mode = WindowManagementMode
	}

	// Update workspace focus map
	m.WorkspaceFocus = make(map[int]int)
	for workspace, windowID := range state.WorkspaceFocus {
		for i, w := range m.Windows {
			if w.ID == windowID {
				m.WorkspaceFocus[workspace] = i
				break
			}
		}
	}

	// Update BSP state. Adopt the daemon's window->int-ID map only from a strictly
	// newer sync, and even then merge rather than replace: a window this client has
	// already mapped keeps its int ID, so a stale echo that omits it (or an already
	// applied one) cannot strip the mapping and force getWindowIntID to hand out a
	// fresh number. A churned int ID orphans the window's node in the tree, which
	// TileAllWindows then rebuilds from scratch with the spiral scheme, discarding
	// any forced split direction.
	if newerState && state.WindowToBSPID != nil {
		if m.WindowToBSPID == nil {
			m.WindowToBSPID = make(map[string]int, len(state.WindowToBSPID))
		}
		for id, intID := range state.WindowToBSPID {
			if _, ok := m.WindowToBSPID[id]; !ok {
				m.WindowToBSPID[id] = intID
			}
		}
		// Keep the reverse map consistent with the merge; getWindowByIntID trusts
		// it as a fast path before falling back to a linear scan.
		m.BSPIDToWindowID = make(map[int]string, len(m.WindowToBSPID))
		for id, intID := range m.WindowToBSPID {
			m.BSPIDToWindowID[intID] = id
		}
	}
	// Never rewind the BSP ID allocator. The counter only has to be unique
	// locally, and taking a lower value from a sync hands the next window an int
	// ID an existing window already holds, which silently merges the two in every
	// tree and layout keyed by it. That is reachable now that a window can appear
	// through a sync rather than only through local creation.
	m.NextBSPWindowID = max(m.NextBSPWindowID, state.NextBSPWindowID)
	m.TilingScheme = layout.AutoScheme(state.TilingScheme)
	m.ApplyLayoutModeName(state.LayoutMode)
	m.adoptSidebarState(state)
	// Adopted after the layout mode it is folded together with, and before the
	// staleness check below, which has to run against the inputs the session
	// agrees on rather than the ones this client walked in with.
	geometryChanged := m.adoptPaneGeometry(state)

	// Update BSP trees, again only from a strictly newer sync so a lagging echo
	// cannot clobber the tree this client just computed (see newerState above).
	if newerState && state.WorkspaceTrees != nil && state.AutoTiling {
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

	// Mirror the structural half of a float toggled elsewhere. The peer that
	// floated a pane removed it from its own tiling structure and retiled, but
	// a client push never carries the tree (newerState gates it), so this
	// client's tree still holds the leaf; left there, the tiled panes never
	// fill the box again and every sync reads as a stale layout. RemoveWindow
	// ignores an id a tree does not hold, so every tree is asked, the way
	// adoptSyncedWindows handles a closed pane. A pane tiled again is re-added
	// where this client would add one of its own; on another workspace the
	// tree catches up when that workspace next tiles.
	for _, w := range floated {
		intID := m.getWindowIntID(w.ID)
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
		if tree := m.WorkspaceTrees[m.CurrentWorkspace]; tree != nil {
			intID := m.getWindowIntID(w.ID)
			if !slices.Contains(tree.GetAllWindowIDs(), intID) {
				m.AddWindowToBSPTree(w)
			}
		}
	}

	// Input mode is not synced: it is per-viewer. Applying another client's mode
	// here used to yank this client between window-management and terminal mode
	// whenever anyone else switched.

	// A window the daemon created carries a nominal box, not a position: the
	// daemon has no viewport and says so with Unplaced rather than guessing.
	// Placing it is this client's job and has to happen whether or not tiling is
	// on, because with tiling off nothing else will ever move it and it would
	// render full-screen over everything.
	placedWindows := m.placeUnplacedWindows(state, created)
	placed := len(placedWindows) > 0
	if m.AutoTiling {
		// A pane the client is placing for the first time is a pane appearing, and
		// the layout below is what decides where it appears from. Set only under
		// tiling, and only here rather than inside the placing loop, so the restore
		// path - which adopts a whole session at once and suppresses the retile
		// that would consume these - cannot leave the flag on every pane for
		// whenever the next retile happens to run.
		for _, w := range placedWindows {
			w.Opening = true
		}
	}

	// A pane arriving is the event the launcher's type-it-out path waits on, so
	// it is answered here rather than polled for. It is answered outside the
	// switch below and not inside it: which layout the user has running decides
	// whether a new pane needs retiling, and has nothing to say about whether a
	// command line was waiting for it. Seeding from inside the tiling branch
	// meant Tab typed nothing at all for anyone with tiling off.
	m.seedAdoptedWindows(created)

	// A sync that changes which windows exist also has to be absorbed by the
	// layout, not just by the window list: a new window needs a slot in the
	// tiling structure and a closed one leaves its tile behind. Retiling is what
	// turns a daemon-side lifecycle change into something the renderer has
	// actually absorbed.
	//
	// placed is still consulted separately from created and removed, because the
	// two are not the same question: a window can arrive in this client's list on
	// a sync that places it and a sync that does not, and it is the placing that
	// the layout has to absorb. It no longer fires on the daemon's repeat
	// broadcasts of a creation, which placeUnplacedWindows now declines to act on;
	// before it did, and this branch existed to fold a pane the repeat had
	// teleported back out of its tile into the layout again.
	switch {
	case m.AutoTiling && (len(created) > 0 || len(removed) > 0 || placed):
		m.adoptSyncedWindows(created, removed, placed)
	case placed:
		// Untiled, so there is nothing to retile, but the geometry this client
		// just chose is news to the daemon.
		m.RecalcZOrder()
		m.SyncStateToDaemon()
	}

	// The strip as the session has it, taken before the retile below rather than
	// after it: that retile lays the strip out from the offset and the focused
	// column, so giving it the session's answers first is one pass over the
	// panes instead of two - and on a workspace switch, which is the case that
	// retiles, the strip it lays out is then the one this sync named. A sync
	// that moved neither leaves it alone.
	m.adoptScrollStrip(state.ScrollStrip, focusChanged)

	// A sync carries the peer's pane rectangles, and a rectangle is not shared
	// state: it is what the peer's own render size and the shared tree came to
	// between them. Adopting it is right for a pane this client has never
	// placed, and wrong the moment the two clients are not the same size.
	//
	// So the adopted geometry is checked against the box tiling is supposed to
	// fill, in both directions. The check used to ask only whether a pane
	// overflowed the viewport, which catches a peer that is larger and misses a
	// peer that is smaller entirely: 30-column panes sit happily inside a
	// 120-column screen, so a client whose session had just grown back adopted
	// the departing peer's cramped layout and kept it. On screen that is a
	// full-width dock with the panes still huddled in the corner, which is what
	// "the borders don't come back" looks like.
	// geometryChanged forces the retile the staleness check cannot always see:
	// flipping shared borders with the gap pinned changes every tiled pane's
	// guest grid without moving a single rectangle.
	if m.AutoTiling && len(m.Windows) > 0 && len(created) == 0 && len(removed) == 0 &&
		(geometryChanged || workspaceChanged || m.tiledLayoutStale()) {
		m.TileAllWindows()
	}

	m.MarkAllDirty()
	return nil
}

// adoptWorkspaceMasterRatio takes the session's per-workspace master ratios onto
// this client.
//
// Entries are merged rather than replacing the map. Nothing ever removes one, so
// a merge loses nothing, and a sync that lags a ratio this client has just moved
// cannot take that ratio away again. A state that says nothing - a peer too old
// to send the field, or a client that had tiling off - leaves what this client
// holds alone, which is what makes the field additive: MasterRatio still carries
// the ratio in force on the workspace the state names, and RestoreWorkspaceLayout
// still falls back to the configured ratio for a workspace nobody has a value
// for, exactly as it did before this existed.
func (m *OS) adoptWorkspaceMasterRatio(state *session.SessionState) {
	if m.WorkspaceMasterRatio == nil {
		m.WorkspaceMasterRatio = make(map[int]float64, len(state.WorkspaceMasterRatio))
	}
	maps.Copy(m.WorkspaceMasterRatio, state.WorkspaceMasterRatio)
}

// adoptSidebarState takes the rail as the session has it. A zero width is a
// client with no preference of its own rather than a request to narrow the
// rail, so it is left alone; the collapsed flag is adopted as it stands.
//
// Adopting it can change what this client keeps for its own chrome, which the
// session's reserve is settled from. Announcing that is the caller's job and
// not this one's: nothing may be sent from inside a sync, so the announce
// happens once the sync has been applied - see the StateSyncMsg case in
// update.go.
func (m *OS) adoptSidebarState(state *session.SessionState) {
	if state.SidebarWidth > 0 {
		m.SidebarWidthPref = state.SidebarWidth
	}
	m.SidebarCollapsed = state.SidebarCollapsed
}

// updateWindowFromState updates an existing window with state from sync
func (m *OS) updateWindowFromState(w *terminal.Window, ws *session.WindowState) {
	// An unplaced box is the daemon saying it had to write a number somewhere,
	// not a position anyone chose (see placeUnplacedWindows), and this client has
	// already answered that question: a window that reaches this function exists
	// here, so it was placed when it arrived. The answer simply has not reached
	// the daemon yet, because the daemon re-broadcasts the creating state after
	// any mutation that follows it, and that broadcast still carries the flag.
	//
	// So the geometry in it is not news, it is the question being asked again.
	// Adopting it drops a full-size pane on top of an otherwise clean split a few
	// frames after the pane opened. That used to be invisible because
	// placeUnplacedWindows ran straight afterwards and placed the window a second
	// time, which repaired the geometry at the cost of throwing the pane back to
	// the raw placement box mid-animation. Declining the box here is what makes
	// placing once correct.
	adoptGeometry := !ws.Unplaced

	// Check if size changed
	sizeChanged := adoptGeometry && (w.Width != ws.Width || w.Height != ws.Height)

	// Update all properties
	w.SetTitle(ws.Title)
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
	// A float is layout intent: without it this client tiles the pane a peer
	// floated back into the box and destroys the float everywhere.
	w.IsFloating = ws.IsFloating
	w.PreMinimizeX = ws.PreMinimizeX
	w.PreMinimizeY = ws.PreMinimizeY
	w.PreMinimizeWidth = ws.PreMinimizeW
	w.PreMinimizeHeight = ws.PreMinimizeH
	w.SetAltScreen(ws.IsAltScreen)
	w.AgentMessage = ws.AgentMessage
	w.AgentHarness = ws.AgentHarness
	w.AgentStateAt = ws.AgentStateAt
	// Last, and it adopts AgentState itself: an alert raised from here reads the
	// message and harness above, which have to be the ones that arrived with the
	// state rather than the ones it replaced.
	m.noteAgentState(w, string(ws.AgentState))
	w.ForegroundCmd = ws.ForegroundCmd

	if renderTraceEnabled && !sizeChanged {
		traceSync(w, ws.IsAltScreen, false, w.Width, w.Height, "SetAltScreen; no resize")
	}

	if sizeChanged {
		// Resize the emulator and tell the daemon, through the one function that
		// does both and records what the PTY was told.
		//
		// Doing the two halves by hand here is what broke a new pane's size: this
		// path resized the real PTY without touching the announcement record, so
		// the record still named the size the pane had before. The retile that
		// followed asked for that same size again, Window.Resize saw its own
		// record already matching and announced nothing, and the emulator went to
		// the pane's size while the shell stayed at the size this line had just
		// given it. A whole-screen pane then ran an 80x24 shell.
		//
		// Window.Resize also holds the window's I/O lock across the emulator
		// resize, which this path needs and which is easy to drop when the call
		// is spelled out by hand: Terminal has no lock of its own, the daemon
		// outputWriter goroutine writes the cell buffer under ioMu and the
		// renderer reads it under RLockIO, and resizing reallocates every line in
		// that buffer. An unlocked resize tears the buffer out from under a
		// concurrent write or render and the pane composites as empty cells,
		// which renderTerminal then caches; an idle shell emits nothing to
		// re-dirty it, so the pane stays blank.
		//
		// This path is reached on every daemon state sync, and any input in a
		// daemon session syncs state, so a focus change is the common trigger.
		w.Resize(ws.Width, ws.Height)

		// While a tape is building a layout, re-fetch the pane's content from the
		// daemon after the resize. Resizing the local emulator reflows whatever
		// cells the client already held, but those can be stale or empty: when a
		// pane is split, the SOURCE pane shrinks and re-syncs here, and if its
		// output landed while the client was mid-build (so the render tick dropped
		// it) the local buffer is blank and the reflow keeps it blank, with an
		// idle shell emitting nothing to re-dirty it. The daemon holds the
		// authoritative screen, so pull it. Gated to script playback so an
		// interactive resize drag (which syncs sizes rapidly) never pays for a
		// per-motion round-trip.
		if m.ScriptMode && w.DaemonMode && w.PTYID != "" && m.DaemonClient != nil {
			if state, err := m.DaemonClient.GetTerminalState(w.PTYID, 0, w.ScrollbackLenSync()); err == nil && state != nil {
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

// adoptWindowState copies the daemon's view of a window onto a freshly built
// live one. Restoring a session and adopting a window the daemon pushed are the
// same copy, and it lives here so the two cannot drift: the agents section went
// empty for the session you were attached to because the restore path knew
// about the layout fields and not the agent ones, which are the only way agent
// state reaches the session this client owns.
func adoptWindowState(window *terminal.Window, ws session.WindowState) {
	window.CustomName = ws.CustomName
	window.Workspace = ws.Workspace
	window.Minimized = ws.Minimized
	// A float is layout intent, not a rectangle: a peer that does not know a
	// pane floats tiles it back into the box. See WindowState.IsFloating.
	window.IsFloating = ws.IsFloating
	window.PreMinimizeX = ws.PreMinimizeX
	window.PreMinimizeY = ws.PreMinimizeY
	window.PreMinimizeWidth = ws.PreMinimizeW
	window.PreMinimizeHeight = ws.PreMinimizeH
	window.SetAltScreen(ws.IsAltScreen) // also drives mouse event forwarding
	window.AgentState = string(ws.AgentState)
	window.AgentMessage = ws.AgentMessage
	window.AgentHarness = ws.AgentHarness
	window.AgentStateAt = ws.AgentStateAt
	window.ForegroundCmd = ws.ForegroundCmd
}

// createWindowFromSync creates a new window from sync state
func (m *OS) createWindowFromSync(ws *session.WindowState) *terminal.Window {
	// Safety check for empty IDs
	if ws.ID == "" || ws.PTYID == "" {
		return nil
	}

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
	if window == nil {
		return nil
	}

	caps := m.hostCaps()
	if caps.CellWidth > 0 && caps.CellHeight > 0 {
		window.SetCellPixelDimensions(caps.CellWidth, caps.CellHeight)
	}

	adoptWindowState(window, *ws)

	m.installPassthroughs(window)
	m.setupCwdWatch(window)

	// Set up PTY handlers if we have a daemon client
	if m.DaemonClient != nil {
		ptyID := ws.PTYID

		window.DaemonWriteFunc = func(data []byte) error {
			return m.DaemonClient.WritePTY(ptyID, data)
		}

		window.DaemonResizeFunc = func(width, height int) error {
			return m.DaemonClient.ResizePTY(ptyID, width, height)
		}

		window.StartDaemonResponseReader()

		// Only subscribe to PTY output if window is in current workspace
		// Windows in other workspaces will be subscribed when switching to them
		if ws.Workspace == m.CurrentWorkspace {
			m.primePaneFromDaemon(window)
		}

		// Register exit handler (always needed regardless of workspace)
		windowID := window.ID
		m.DaemonClient.OnPTYClosed(ptyID, func() {
			m.queueWindowExit(windowID)
		})

		window.EnableCallbacks()
	}

	// Every window a daemon session shows now arrives through here, including the
	// ones this user asked for, so this is where the new-window hook belongs.
	m.FireHook(hooks.AfterNewWindow, window.ID, window.Title())

	return window
}

// closeWindowFromSync tears down a window the daemon has removed from the window
// set. This is now the only teardown a daemon session performs, including for a
// close the user asked for, so it has to release everything the window is
// referenced from and not just its PTY subscription: an animation still holding
// the pointer keeps the whole window alive and keeps animating a window that is
// gone, and a stale BSP id mapping hands a later window an id this one still
// owns.
func (m *OS) closeWindowFromSync(w *terminal.Window) {
	if m.DaemonClient != nil && w.PTYID != "" {
		m.unsubscribeFromPTY(w)
	}

	if m.WindowToBSPID != nil {
		intID := m.getWindowIntID(w.ID)
		delete(m.WindowToBSPID, w.ID)
		if m.BSPIDToWindowID != nil {
			delete(m.BSPIDToWindowID, intID)
		}
	}

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

// placeUnplacedWindows gives a position and size to every window in firstSeen
// that the daemon marked Unplaced, and returns the ones it placed.
//
// The daemon creates windows but has no viewport to place them in, so it hands
// over a nominal box and says the box is not a decision. Only a client can turn
// that into a position, and it does so with exactly the rule it uses for a window
// it was asked for directly. The flag is cleared implicitly: the client's next
// sync never sets Unplaced, so placing a window and pushing the result is what
// tells the daemon the question has been answered.
//
// firstSeen is the set of windows this client has only just learned about, and
// it is a restriction, not a hint: a window outside it is left alone even while
// the daemon still calls it unplaced. The daemon re-broadcasts the creating
// state after a following mutation, a focus change or a PTY resize, and that
// broadcast still carries Unplaced until this client's placing push has landed.
// Placing again on that echo teleports a pane the client has already placed and
// tiled back to the raw placement box, and it does it a few frames in - which is
// what tore a newly opened pane out of its open animation and restarted it from
// the middle of the screen. Answering the question once is also all the daemon
// ever asked for.
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
		w.X, w.Y = x, y
		// Resize rather than assigning the size and telling the daemon by hand:
		// it resizes the emulator and announces the same number downstream, and
		// it records that number as the size the PTY now has. Announcing without
		// recording is what left a new pane's shell at the placement box: this
		// loop runs again on a repeat of the creating sync, shrinking the real
		// PTY back down, and the retile that follows asked for a size the stale
		// record already claimed to have sent, so nothing reached the shell.
		//
		// Resize also holds the window's I/O lock across the emulator resize,
		// which this path needs: the emulator has no lock of its own, the daemon
		// outputWriter goroutine writes its cell buffer under ioMu and the
		// renderer reads it under RLockIO, and resizing reallocates every line in
		// that buffer. Resizing unlocked tore the buffer out from under an
		// in-flight write and the pane composited as empty cells, which
		// renderTerminal then cached; an idle shell emits nothing to re-dirty it,
		// so the pane stayed blank.
		//
		// Every window this loop touches is newly created by the daemon and is
		// already subscribed by the time the placing sync arrives, so a pane
		// that has printed anything at all (a shell prompt is enough) has output
		// in flight here.
		w.Resize(width, height)
		w.InvalidateCache()
		placed = append(placed, w)
	}
	return placed
}

// adoptSyncedWindows brings the tiling layout in line with a window set that a
// state sync just changed, then retiles.
//
// The BSP path needs no help placing a window it has not seen: TileAllWindows
// already inserts windows missing from the tree and rebuilds a tree still
// holding windows that are gone. The scrolling layout has no such repair, so its
// columns are added and removed here before the retile.
//
// The resulting geometry is this client's, derived from its own viewport, so it
// is pushed back: the daemon's copy of the layout is whatever a client last told
// it, and after a daemon-side lifecycle change that copy is a layout for a
// different set of windows.
//
// placed is true when this sync re-placed a window the daemon still had marked
// Unplaced. It forces a retile on its own because a re-placed window has been
// knocked out of the tiling layout back to a raw placement box, which the tree
// path cannot detect from created/removed alone (the window already exists in
// the tree); only re-running the layout folds it back in.
func (m *OS) adoptSyncedWindows(created []*terminal.Window, removed []int, placed bool) {
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
	} else {
		// A pane closed on the daemon has to leave the tree here, the way the
		// scrolling layout above is told. Leaving it in meant the tree held a leaf
		// for a window that no longer existed, and TileAllWindows, finding an id it
		// cannot place, throws the entire tree away and chain-inserts every window
		// at 0.5 under the spiral scheme: closing one pane reshuffled every other
		// pane on the workspace and lost every ratio the user had dragged. Which
		// tree holds the leaf is the closed window's own workspace, not
		// necessarily the current one, and RemoveWindow ignores an id it does not
		// have, so this asks all of them.
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

		if m.pendingSplitDir != layout.PreselectionNone {
			// A forced-direction split (ctrl+b | / -) asked the daemon for this pane
			// and stashed the direction for exactly this moment. Insert it on the
			// chosen side of its target before TileAllWindows runs, otherwise the
			// spiral scheme places it and the direction is lost. Only the single-window
			// case is a split; anything else clears the request and falls back.
			if len(created) == 1 {
				m.applyPendingForcedSplit(created[0])
			} else if len(created) > 1 {
				m.pendingSplitDir = layout.PreselectionNone
				m.pendingSplitTarget = ""
			}
		}
	}

	m.TileAllWindows()
	m.SyncStateToDaemon()
}

// applyPendingForcedSplit inserts a daemon-created pane into the BSP tree on the
// side recorded by a forced-direction split, so ctrl+b | / - keep their meaning
// across the round trip that created the window. The pending request is cleared
// whether or not it applies. TileAllWindows runs afterwards and, finding every
// window already in the tree, only re-applies the layout.
func (m *OS) applyPendingForcedSplit(win *terminal.Window) {
	dir := m.pendingSplitDir
	targetID := m.pendingSplitTarget
	m.pendingSplitDir = layout.PreselectionNone
	m.pendingSplitTarget = ""

	if dir == layout.PreselectionNone || win == nil {
		return
	}

	tree := m.GetOrCreateBSPTree()
	windowIntID := m.getWindowIntID(win.ID)
	if tree.HasWindow(windowIntID) {
		return // already in the tree; nothing to force
	}
	targetIntID := m.getWindowIntID(targetID)
	tree.InsertWindowWithPreselection(windowIntID, targetIntID, dir, m.GetBSPBounds(), m.separatorGap())
}

// convertSessionBSPNode converts session.SerializedBSPNode to layout.SerializedNode
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
			state, err := m.DaemonClient.GetTerminalState(w.PTYID, 0, w.ScrollbackLenSync())
			if err != nil {
				m.LogError("Failed to get terminal state for PTY %s: %v", shortID(w.PTYID), err)
				continue
			}

			if state != nil && w.Terminal != nil {
				// Restore IsAltScreen flag and emulator state
				m.restoreTerminalContent(w, state)
				// Remembered for the subscribe that SetupPTYOutputHandlers is
				// about to make, so the stream resumes where this snapshot
				// ends. The two halves are in different functions because the
				// attach sequence restores every window before wiring any of
				// them, and the position has to survive the gap.
				if m.RestoredStreamSeq == nil {
					m.RestoredStreamSeq = make(map[string]int64)
				}
				m.RestoredStreamSeq[w.PTYID] = state.Seq
				m.LogInfo("Restored terminal state for window %s (%dx%d, %d scrollback lines)",
					shortID(w.ID), state.Width, state.Height, state.ScrollbackLen)

				// The daemon PTY is already this size. Seed it as announced so a
				// same-size retile does not re-announce and SIGWINCH the shell,
				// which would repaint its prompt over the screen just restored.
				w.SeedAnnouncedSize(state.Width, state.Height)

				// Note: Resize to trigger redraw is done in TriggerAltScreenRedraws()
				// which is called AFTER SetupPTYOutputHandlers sets up DaemonResizeFunc
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

// syncDaemonPTYDimensions is SyncDaemonPTYDimensions with the announcements already held.
func (m *OS) syncDaemonPTYDimensions() {
	for _, w := range m.Windows {
		if w.DaemonMode && w.DaemonResizeFunc != nil {
			termWidth := w.ContentWidth()
			termHeight := w.ContentHeight()

			// The daemon PTY already carries the announced size (seeded on restore,
			// updated by any retile above). Re-sending it resizes the real PTY,
			// which SIGWINCHes the shell into repainting its prompt. A switch that
			// does not change a pane's size must send zero of those.
			if aw, ah := w.AnnouncedSize(); termWidth == aw && termHeight == ah {
				continue
			}

			// Resize daemon PTY to match window dimensions
			if err := w.DaemonResizeFunc(termWidth, termHeight); err != nil {
				m.LogWarn("Failed to sync PTY dimensions for window %s: %v", shortID(w.ID), err)
			} else {
				w.SeedAnnouncedSize(termWidth, termHeight)
				m.LogInfo("Synced daemon PTY dimensions for window %s (%dx%d)",
					shortID(w.ID), termWidth, termHeight)
			}

			// Ensure local VT emulator dimensions also match. Same rule as
			// updateWindowFromState: the emulator buffer is shared with the
			// output goroutine and the renderer, so a resize needs ioMu.
			//
			// A subscribed pane is sized by its stream instead, so that the
			// bytes the daemon produced before it heard this are laid out at
			// the width the daemon laid them out at. See Window.Resize.
			if w.Terminal != nil && !w.StreamOwnsSize() {
				w.LockIO()
				// Re-check under the lock; Close() nils Terminal while holding it.
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
			// Invalidate all caches to force re-render from fresh state
			w.InvalidateCache()
			w.MarkContentDirty()

			m.LogInfo("Invalidated caches for alt screen window %s", shortID(w.ID))
		}
	}

	// Mark all windows dirty to force full redraw
	m.MarkAllDirty()
}

// restoreTerminalContent populates a window's terminal with content from daemon
// state. What it does to the emulator is session.ApplyTerminalState, which is
// the reading half of the wire contract and lives beside the writing half; what
// is left here is the window around it.
//
// Everything it does to the emulator happens under the window's I/O lock. The
// emulator has no lock of its own; the daemon outputWriter goroutine writes its
// cell buffer under ioMu and the renderer reads it under RLockIO. Restoring is
// a mode switch plus a blit of roughly a screenful of cells, and on the paths
// that reach it with the pane already subscribed (an in-flight resize during
// tape playback, and every attach before the subscribe order below was fixed)
// that ran straight into live output: torn cells on screen, and a RestoreModes
// racing a mode change from the guest leaving mouse tracking or bracketed paste
// set from whichever side landed last.
//
// Ordering against a live subscription is a separate matter from the lock and
// is handled by the callers, which restore before they subscribe.
func (m *OS) restoreTerminalContent(w *terminal.Window, state *session.TerminalState) {
	if w.Terminal == nil || state == nil {
		return
	}

	// Anything still queued for this pane's emulator was produced before the
	// snapshot about to be applied, so applying it afterwards paints it twice.
	// A pane coming back from a workspace switch had a batch in flight from the
	// subscription it had already left, and the line at the seam came back
	// duplicated.
	w.DiscardPendingOutput()

	w.LockIO()
	// Re-check under the lock; Close() nils Terminal while holding it.
	session.ApplyTerminalState(w.Terminal, state)
	w.UnlockIO()

	if state.IsAltScreen {
		m.LogInfo("Restored alt screen mode for window %s", shortID(w.ID))
	}
	if len(state.Modes) > 0 {
		m.LogInfo("Restored %d terminal modes for window %s", len(state.Modes), shortID(w.ID))
	}

	// Set the window's IsAltScreen flag for mouse event forwarding
	w.SetAltScreen(state.IsAltScreen)
	m.LogInfo("Set window IsAltScreen=%v for window %s", state.IsAltScreen, shortID(w.ID))

	if renderTraceEnabled {
		note := "restore: SetAltScreen only"
		if state.IsAltScreen {
			note = "restore: RestoreAltScreenMode(true) + SetAltScreen"
		}
		traceSync(w, state.IsAltScreen, false, state.Width, state.Height, note)
	}

	// Mark content as dirty to trigger rendering
	w.MarkContentDirty()

	// DON'T re-enable callbacks here - they will be enabled after buffered output settles
	// See EnableCallbacksMsg which is sent after 500ms delay
}

// SetupPTYOutputHandlers sets up PTY output handlers for all daemon-mode windows.
// This should be called after RestoreFromState() when attaching to a session.
// Only subscribes to PTYs for windows in the current workspace (visibility optimization).
func (m *OS) SetupPTYOutputHandlers() error {
	if m.DaemonClient == nil {
		m.LogInfo("[SETUP] SetupPTYOutputHandlers: no daemon client")
		return nil
	}

	// Always reset subscribed PTYs to prevent stale entries from previous sessions
	m.SubscribedPTYs = make(map[string]bool)

	m.LogInfo("[SETUP] SetupPTYOutputHandlers: setting up handlers for %d windows", len(m.Windows))

	for i, w := range m.Windows {
		m.LogInfo("[SETUP] Window %d: DaemonMode=%v, PTYID=%s, Workspace=%d", i, w.DaemonMode, w.PTYID, w.Workspace)
		if w.DaemonMode && w.PTYID != "" {
			// Capture window and ptyID for closures
			window := w
			ptyID := w.PTYID

			// Set up the daemon write function for input
			window.DaemonWriteFunc = func(data []byte) error {
				return m.DaemonClient.WritePTY(ptyID, data)
			}

			// Set up the daemon resize function
			window.DaemonResizeFunc = func(width, height int) error {
				return m.DaemonClient.ResizePTY(ptyID, width, height)
			}

			// Start the response reader to handle DA queries and other terminal responses
			window.StartDaemonResponseReader()

			// Only subscribe to PTYs for windows in the current workspace
			// Windows in other workspaces will be subscribed when switching to them
			if w.Workspace == m.CurrentWorkspace {
				m.subscribeToPTY(window, m.RestoredStreamSeq[ptyID])
			}

			// Register handler for when PTY process exits
			windowID := window.ID
			m.DaemonClient.OnPTYClosed(ptyID, func() {
				m.queueWindowExit(windowID)
			})
		}
	}

	return nil
}

// primePaneFromDaemon fills a pane's local emulator with the daemon's copy of
// the screen and then starts the live stream, in that order.
//
// The order is the point of the function existing. Subscribing first meant the
// output goroutine was already writing the emulator while the snapshot was
// blitted into it on the UI goroutine: a torn buffer, and a pane showing a
// mixture of stale snapshot and live output, since the blit writes cells that
// are by definition older than anything arriving live. Restoring first costs
// only the output emitted between the state request and the subscribe, which is
// one round trip and cannot be interleaved into the wrong frame.
//
// Both call sites route through here so the ordering is stated once.
func (m *OS) primePaneFromDaemon(window *terminal.Window) {
	if m.DaemonClient == nil || window.PTYID == "" {
		return
	}

	// Everything already queued for this emulator is applied before the
	// snapshot is fetched, not thrown away. The pane is unsubscribed by the
	// time it is primed, so the queue is finite and this returns. Discarding
	// it looked safe because the snapshot is newer than anything queued, but a
	// snapshot carries a bounded scrollback window and the queue can hold far
	// more than that: a pane that outpaced its client came back with its
	// history frozen at wherever the client had got to, a hole down to the
	// snapshot's window, and the screen at the end.
	window.DrainPendingOutput()

	state, err := m.DaemonClient.GetTerminalState(window.PTYID, 0, window.ScrollbackLenSync())
	if err != nil || state == nil {
		m.subscribeToPTY(window, 0)
		return
	}

	// The pane may have been resized while it was hidden, by another client or
	// by the daemon. Window.Resize measures against what this client last
	// announced, which still says the size this client gave the pane, so it
	// sees nothing to do and the pane comes back at a size the daemon is not
	// at. The snapshot carries what the daemon actually is, so seed the record
	// from that and let the resize happen before the snapshot is taken for
	// real: reconciling after would blit cells laid out at one width into an
	// emulator about to reflow at another.
	if state.Width != window.ContentWidth() || state.Height != window.ContentHeight() {
		window.SeedAnnouncedSize(state.Width, state.Height)
		window.Resize(window.Width, window.Height)
		if fresh, err := m.DaemonClient.GetTerminalState(window.PTYID, 0, window.ScrollbackLenSync()); err == nil && fresh != nil {
			state = fresh
		}
	}

	// The snapshot's own bounds, before any of it is written. The reconcile
	// above only fires when the daemon disagrees with this client's layout; an
	// emulator can be at a third size, because a resize the stream carried is
	// dropped along with the output a restore discards, and nothing else brings
	// a streamed pane's grid back down.
	window.ResizeEmulatorToSnapshot(state.Width, state.Height)

	m.restoreTerminalContent(window, state)
	m.subscribeToPTY(window, state.Seq)
}

// subscribeToPTY subscribes to PTY output for a window. fromSeq is the stream
// position the window's emulator has just been restored to, so the daemon sends
// what came after the snapshot rather than history the snapshot already shows.
// Safe to call multiple times - will not double-subscribe.
func (m *OS) subscribeToPTY(window *terminal.Window, fromSeq int64) {
	if m.DaemonClient == nil || window.PTYID == "" {
		return
	}

	ptyID := window.PTYID

	// Check if already subscribed
	if m.SubscribedPTYs[ptyID] {
		return
	}

	m.LogInfo("[SUBSCRIBE] Subscribing to PTY %s for window %s", shortID(ptyID), shortID(window.ID))
	// Registered before the subscribe, so the first resize the daemon announces
	// cannot arrive with nothing listening for it.
	m.DaemonClient.OnPTYResized(ptyID, window.ResizeFromStream)
	window.SetStreamOwnsSize(true)
	err := m.DaemonClient.SubscribePTY(ptyID, fromSeq, func(data []byte) {
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

// unsubscribeFromPTY unsubscribes from PTY output for a window.
func (m *OS) unsubscribeFromPTY(window *terminal.Window) {
	if m.DaemonClient == nil || window.PTYID == "" {
		return
	}

	ptyID := window.PTYID

	// Check if actually subscribed
	if !m.SubscribedPTYs[ptyID] {
		return
	}

	m.LogInfo("[UNSUBSCRIBE] Unsubscribing from PTY %s for window %s", shortID(ptyID), shortID(window.ID))
	m.DaemonClient.UnsubscribePTY(ptyID)
	delete(m.SubscribedPTYs, ptyID)
	// With no stream to be ordered against, the layout sizes the pane again, as
	// it does for a pane that has never been subscribed.
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
			// Only subscribe if not already subscribed
			if !m.SubscribedPTYs[w.PTYID] {
				m.primePaneFromDaemon(w)
			}
		}
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
// unconditional on purpose - the input handler syncs after every keystroke,
// every click and every wheel event, so that nothing a user does can go
// unrecorded - and the great majority of those events change nothing the
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

	// A sync being applied is not a moment to speak. Whatever wanted to be sent
	// was worked out from a peer's state, and sending it back is what turns two
	// clients that disagree into two clients that argue. The one thing inside a
	// sync that genuinely has news - a window this client placed because the
	// daemon could not - says so here and is sent once the sync is applied.
	if m.applyingPeerSync {
		m.syncAnswerOwed = true
		return
	}

	state := m.BuildSessionState()
	fp := session.StateFingerprint(state)
	if m.syncedFPSet && m.syncedFP == fp {
		return
	}

	if err := m.DaemonClient.UpdateState(state); err != nil {
		m.LogError("Failed to sync state to daemon: %v", err)
		// Not recorded: the daemon does not hold what could not reach it, so
		// the next sync must be free to send the same thing again.
		return
	}
	m.syncedFP, m.syncedFPSet = fp, true
}

// warnOnBuildMismatch says so when the daemon is running a different build of
// tuios from this client. The two still speak, so this is a note and not a
// refusal - but it is the difference between "the fix does not work" and "the
// fix is not installed on both sides", and nothing else says it out loud.
func (m *OS) warnOnBuildMismatch() {
	if m.DaemonClient == nil || !m.IsDaemonSession {
		return
	}
	clientBuild, daemonBuild := m.DaemonClient.BuildMismatch()
	if clientBuild == "" {
		return
	}
	m.LogWarn("Build mismatch: client %s, daemon %s", clientBuild, daemonBuild)
	m.ShowNotification(fmt.Sprintf(
		"The daemon is version %s and this window is version %s. To use one version in both, run 'tuios kill-server', then start tuios again.",
		daemonBuild, clientBuild), "warning", 0)
}

// AnnounceLayoutReserve tells the daemon what this client keeps for its own
// chrome, so the session can settle on a reserve that fits every client. It
// sends only when the answer has moved, and it says it through the message that
// already carries this client's viewport, so the two halves of "what box do the
// panes go in" cannot disagree for a frame.
//
// It is called from the paths that can change the chrome - a viewport resize,
// which moves the sidebar's breakpoint; a config reload; and any input, which is
// how the rail is folded, dragged or turned off. Nothing polls for it: the
// answer is a pure function of this client's own state, so a call that finds it
// unmoved costs one comparison and sends nothing.
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

// forgetSyncedState drops the record of what was last pushed, so the next sync
// is sent whatever it says. Anything that can leave the daemon holding a state
// this client did not put there calls it: a state arriving from elsewhere, or a
// reconnect to a daemon that never saw the last push.
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

// ResizeDaemonPTY resizes a daemon-managed PTY.
func (m *OS) ResizeDaemonPTY(window *terminal.Window, width, height int) error {
	if m.DaemonClient == nil || !window.DaemonMode {
		return nil
	}

	// Account for borders
	termWidth := max(width-2, 1)
	termHeight := max(height-2, 1)

	return m.DaemonClient.ResizePTY(window.PTYID, termWidth, termHeight)
}
