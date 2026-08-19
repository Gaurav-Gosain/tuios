// Package app provides the core TUIOS application logic and window management.
package app

import (
	"bytes"
	"maps"
	"os"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// passThroughCursorStyle detects DECSCUSR (cursor style) sequences in the data
// and writes them directly to stdout to pass through to the parent terminal.
// The VT emulator absorbs these sequences, so we need to re-emit them.
// DECSCUSR format: CSI Ps SP q (ESC [ Ps SPACE q) where Ps is optional (0-6)
func passThroughCursorStyle(data []byte) {
	// Look for DECSCUSR pattern: \x1b[N q where N is 0-6 (or no digit)
	idx := 0
	for idx < len(data) {
		// Find ESC [
		escIdx := bytes.Index(data[idx:], []byte("\x1b["))
		if escIdx == -1 {
			break
		}
		escIdx += idx

		// Check if this could be DECSCUSR
		// Need at least ESC [ SP q (4 bytes from escIdx)
		if escIdx+4 > len(data) {
			idx = escIdx + 1
			continue
		}

		// Check for pattern: optional digit(s) followed by space and 'q'
		numEnd := escIdx + 2
		for numEnd < len(data) && data[numEnd] >= '0' && data[numEnd] <= '9' {
			numEnd++
		}

		// Check if followed by " q" (space then q)
		if numEnd+1 < len(data) && data[numEnd] == ' ' && data[numEnd+1] == 'q' {
			// Found DECSCUSR sequence - write it to stdout
			seq := data[escIdx : numEnd+2]
			_, _ = os.Stdout.Write(seq)
			idx = numEnd + 2
			continue
		}

		idx = escIdx + 1
	}
}

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

	m.SessionName = state.Name
	m.adoptSessionLabels(state)
	m.DaemonStateVersion = state.Version
	// Clamp to a valid workspace: SwitchToWorkspace rejects workspace < 1, so a
	// state carrying 0 (legacy, or a freshly created session with no windows)
	// would strand every subsequently created window on an unreachable workspace.
	m.CurrentWorkspace = clampWorkspace(state.CurrentWorkspace)
	m.MasterRatio = state.MasterRatio
	m.AutoTiling = state.AutoTiling

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
		)
		if window == nil {
			m.LogError("Failed to create daemon window for %s", shortID(ws.ID))
			continue
		}

		caps := GetHostCapabilities()
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

	// A window created while nothing was attached has never been placed by
	// anyone, and RestoredFromState below suppresses the first retile, so without
	// this it would render as a full-size box over the restored layout.
	m.placeUnplacedWindows(state)

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

// ApplyStateSync applies a state update from another client.
// This handles window creation, deletion, and property updates.
func (m *OS) ApplyStateSync(state *session.SessionState) error {
	if state == nil {
		return nil
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

	for _, ws := range state.Windows {
		if existingWindow, exists := existingByID[ws.ID]; exists {
			// Update existing window
			m.updateWindowFromState(existingWindow, &ws)
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
	m.CurrentWorkspace = clampWorkspace(state.CurrentWorkspace)
	m.MasterRatio = state.MasterRatio
	m.AutoTiling = state.AutoTiling

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

	// Input mode is not synced: it is per-viewer. Applying another client's mode
	// here used to yank this client between window-management and terminal mode
	// whenever anyone else switched.

	// A window the daemon created carries a nominal box, not a position: the
	// daemon has no viewport and says so with Unplaced rather than guessing.
	// Placing it is this client's job and has to happen whether or not tiling is
	// on, because with tiling off nothing else will ever move it and it would
	// render full-screen over everything.
	placed := m.placeUnplacedWindows(state)

	// A sync that changes which windows exist also has to be absorbed by the
	// layout, not just by the window list: a new window needs a slot in the
	// tiling structure and a closed one leaves its tile behind. Retiling is what
	// turns a daemon-side lifecycle change into something the renderer has
	// actually absorbed.
	//
	// placed matters here even when no window was created or removed. The daemon
	// broadcasts the creation state (the window still marked Unplaced) more than
	// once around a single creation: a mutation that follows it, a focus change
	// or a PTY resize, re-emits canonical state that still carries the Unplaced
	// flag until this client's placing push has landed. A later such broadcast is
	// processed after this client already placed and tiled the window, and it
	// re-runs placeUnplacedWindows, knocking the window out of its tile back to
	// the raw placement box. Under tiling that has to be folded straight back
	// into the layout; otherwise the window is left floating over the tiled panes
	// even though the daemon's own geometry for it is already correct (which is
	// how it looked on screen: a full-size window over an otherwise clean split).
	switch {
	case m.AutoTiling && (len(created) > 0 || len(removed) > 0 || placed):
		m.adoptSyncedWindows(created, removed, placed)
	case placed:
		// Untiled, so there is nothing to retile, but the geometry this client
		// just chose is news to the daemon.
		m.RecalcZOrder()
		m.SyncStateToDaemon()
	}

	// If auto-tiling is enabled and the synced state has different dimensions,
	// retile to fit our effective render size. This handles the case where
	// a client with a smaller terminal joins and receives state from a larger client.
	if m.AutoTiling && len(m.Windows) > 0 && len(created) == 0 && len(removed) == 0 {
		renderWidth := m.GetRenderWidth()
		renderHeight := m.GetRenderHeight()
		// Check if any window extends beyond our render bounds
		needsRetile := false
		for _, w := range m.Windows {
			if w.Workspace == m.CurrentWorkspace && !w.Minimized {
				if w.X+w.Width > renderWidth || w.Y+w.Height > renderHeight+m.GetTopMargin() {
					needsRetile = true
					break
				}
			}
		}
		if needsRetile {
			m.TileAllWindows()
		}
	}

	m.MarkAllDirty()
	return nil
}

// updateWindowFromState updates an existing window with state from sync
func (m *OS) updateWindowFromState(w *terminal.Window, ws *session.WindowState) {
	// Check if size changed
	sizeChanged := w.Width != ws.Width || w.Height != ws.Height

	// Update all properties
	w.SetTitle(ws.Title)
	w.CustomName = ws.CustomName
	w.X = ws.X
	w.Y = ws.Y
	w.Width = ws.Width
	w.Height = ws.Height
	w.Z = ws.Z
	w.Workspace = ws.Workspace
	w.Minimized = ws.Minimized
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
	)
	if window == nil {
		return nil
	}

	caps := GetHostCapabilities()
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
			if m.WindowExitChan != nil {
				m.WindowExitChan <- windowID
			}
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

// placeUnplacedWindows gives a position and size to every window in state that
// the daemon marked Unplaced, and reports whether it moved any.
//
// The daemon creates windows but has no viewport to place them in, so it hands
// over a nominal box and says the box is not a decision. Only a client can turn
// that into a position, and it does so with exactly the rule it uses for a window
// it was asked for directly. The flag is cleared implicitly: the client's next
// sync never sets Unplaced, so placing a window and pushing the result is what
// tells the daemon the question has been answered.
func (m *OS) placeUnplacedWindows(state *session.SessionState) bool {
	byID := make(map[string]*terminal.Window, len(m.Windows))
	for _, w := range m.Windows {
		byID[w.ID] = w
	}

	placed := false
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
		placed = true
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
				if m.WindowExitChan != nil {
					m.WindowExitChan <- windowID
				}
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
		passThroughCursorStyle(data)
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
func (m *OS) SyncStateToDaemon() {
	if m.DaemonClient == nil || !m.IsDaemonSession {
		return
	}

	state := m.BuildSessionState()
	if err := m.DaemonClient.UpdateState(state); err != nil {
		m.LogError("Failed to sync state to daemon: %v", err)
	}
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
