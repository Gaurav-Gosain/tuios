// Package app provides the core TUIOS application logic and window management.
package app

import (
	"bytes"
	"os"

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
		Mode:             int(m.Mode),
		WorkspaceFocus:   make(map[int]string),
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
			Title:        w.Title,
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
			IsAltScreen:  w.IsAltScreen, // Save alt screen state for mouse forwarding on restore
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
		for k, v := range m.WindowToBSPID {
			state.WindowToBSPID[k] = v
		}
	}
	state.NextBSPWindowID = m.NextBSPWindowID
	state.TilingScheme = int(m.TilingScheme)

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
	m.CurrentWorkspace = state.CurrentWorkspace
	m.MasterRatio = state.MasterRatio
	m.AutoTiling = state.AutoTiling
	m.Mode = Mode(state.Mode)

	// Set effective dimensions from state - this is the min of all connected clients
	// as calculated by the daemon. This ensures a new client joining respects
	// the existing effective size even before receiving a SessionResizeMsg.
	if state.Width > 0 && state.Height > 0 {
		m.EffectiveWidth = state.Width
		m.EffectiveHeight = state.Height
		m.LogInfo("[RESTORE] Set effective size from state: %dx%d", state.Width, state.Height)
	}

	// Clear existing windows
	for _, w := range m.Windows {
		w.Close()
	}
	m.Windows = nil

	// Create windows from state
	for i, ws := range state.Windows {
		m.LogInfo("[RESTORE] Creating window %d: ID=%s, PTYID=%s", i, ws.ID[:8], ws.PTYID[:8])
		window := terminal.NewDaemonWindow(
			ws.ID,
			ws.Title,
			ws.X, ws.Y,
			ws.Width, ws.Height,
			ws.Z,
			ws.PTYID,
		)
		if window == nil {
			m.LogError("Failed to create daemon window for %s", ws.ID[:8])
			continue
		}

		caps := GetHostCapabilities()
		if caps.CellWidth > 0 && caps.CellHeight > 0 {
			window.SetCellPixelDimensions(caps.CellWidth, caps.CellHeight)
		}

		window.CustomName = ws.CustomName
		window.Workspace = ws.Workspace
		window.Minimized = ws.Minimized
		window.PreMinimizeX = ws.PreMinimizeX
		window.PreMinimizeY = ws.PreMinimizeY
		window.PreMinimizeWidth = ws.PreMinimizeW
		window.PreMinimizeHeight = ws.PreMinimizeH
		window.IsAltScreen = ws.IsAltScreen // Restore alt screen state for mouse event forwarding

		// CRITICAL: Suppress callbacks during restoration to prevent race condition
		// where buffered PTY output overwrites the restored IsAltScreen state
		// Callbacks will be re-enabled in restoreTerminalContent() after state is fully restored
		window.DisableCallbacks()

		m.setupKittyPassthrough(window)
		m.setupSixelPassthrough(window)

		m.Windows = append(m.Windows, window)
		m.LogInfo("[RESTORE] Window %d created: DaemonMode=%v, PTYID=%s", i, window.DaemonMode, window.PTYID[:8])
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
			m.LogInfo("[RESTORE] WindowToBSPID: %s -> %d", k[:8], v)
		}
	}
	m.NextBSPWindowID = state.NextBSPWindowID
	m.TilingScheme = layout.AutoScheme(state.TilingScheme)
	m.LogInfo("[RESTORE] NextBSPWindowID=%d, TilingScheme=%d", m.NextBSPWindowID, m.TilingScheme)

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

	m.MarkAllDirty()
	m.LogInfo("[RESTORE] Restored session state: %d windows, FocusedWindow=%d, AutoTiling=%v", len(m.Windows), m.FocusedWindow, m.AutoTiling)

	// Mark that we restored from state - this prevents the first resize from retiling
	// and allows the layout to be preserved as the user left it
	m.RestoredFromState = true

	// If we have windows and a focused window, switch to terminal mode
	// This ensures mouse events are forwarded to terminals after restore
	if len(m.Windows) > 0 && m.FocusedWindow >= 0 {
		m.Mode = TerminalMode
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
			}
		}
	}

	// Close windows that were deleted by other client
	for _, w := range existingByID {
		m.closeWindowFromSync(w)
	}

	// Update window list
	m.Windows = newWindows

	// Update global state
	m.SessionName = state.Name
	m.CurrentWorkspace = state.CurrentWorkspace
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

	// Update BSP state
	if state.WindowToBSPID != nil {
		m.WindowToBSPID = make(map[string]int)
		for k, v := range state.WindowToBSPID {
			m.WindowToBSPID[k] = v
		}
	}
	m.NextBSPWindowID = state.NextBSPWindowID
	m.TilingScheme = layout.AutoScheme(state.TilingScheme)

	// Update BSP trees
	if state.WorkspaceTrees != nil && state.AutoTiling {
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

	// Sync mode from other client
	m.Mode = Mode(state.Mode)

	// If auto-tiling is enabled and the synced state has different dimensions,
	// retile to fit our effective render size. This handles the case where
	// a client with a smaller terminal joins and receives state from a larger client.
	if m.AutoTiling && len(m.Windows) > 0 {
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
	w.Title = ws.Title
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
	w.IsAltScreen = ws.IsAltScreen

	if sizeChanged {
		// Resize terminal emulator
		if w.Terminal != nil {
			termWidth := max(ws.Width-2, 1)
			termHeight := max(ws.Height-2, 1)
			w.Terminal.Resize(termWidth, termHeight)
		}

		// Resize PTY in daemon
		if w.DaemonResizeFunc != nil {
			termWidth := max(ws.Width-2, 1)
			termHeight := max(ws.Height-2, 1)
			_ = w.DaemonResizeFunc(termWidth, termHeight)
		}

		w.InvalidateCache()
		w.MarkContentDirty()
	}
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
	)
	if window == nil {
		return nil
	}

	caps := GetHostCapabilities()
	if caps.CellWidth > 0 && caps.CellHeight > 0 {
		window.SetCellPixelDimensions(caps.CellWidth, caps.CellHeight)
	}

	window.CustomName = ws.CustomName
	window.Workspace = ws.Workspace
	window.Minimized = ws.Minimized
	window.PreMinimizeX = ws.PreMinimizeX
	window.PreMinimizeY = ws.PreMinimizeY
	window.PreMinimizeWidth = ws.PreMinimizeW
	window.PreMinimizeHeight = ws.PreMinimizeH
	window.IsAltScreen = ws.IsAltScreen

	m.setupKittyPassthrough(window)
	m.setupSixelPassthrough(window)

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

		// Subscribe to PTY output
		err := m.DaemonClient.SubscribePTY(ptyID, func(data []byte) {
			passThroughCursorStyle(data)
			window.WriteOutputAsync(data)
		})
		if err != nil {
			// Log but continue - window is still usable
			m.LogError("Failed to subscribe to PTY: %v", err)
		}

		// Register exit handler
		windowID := window.ID
		m.DaemonClient.OnPTYClosed(ptyID, func() {
			if m.WindowExitChan != nil {
				m.WindowExitChan <- windowID
			}
		})

		// Get terminal content
		termState, err := m.DaemonClient.GetTerminalState(ptyID, true)
		if err == nil && termState != nil {
			m.restoreTerminalContent(window, termState)
		}

		window.EnableCallbacks()
	}

	return window
}

// closeWindowFromSync closes a window that was deleted by another client
func (m *OS) closeWindowFromSync(w *terminal.Window) {
	if m.DaemonClient != nil && w.PTYID != "" {
		m.DaemonClient.UnsubscribePTY(w.PTYID)
	}
	w.Close()
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
			state, err := m.DaemonClient.GetTerminalState(w.PTYID, true)
			if err != nil {
				m.LogError("Failed to get terminal state for PTY %s: %v", w.PTYID[:8], err)
				continue
			}

			if state != nil && w.Terminal != nil {
				// Restore IsAltScreen flag and emulator state
				m.restoreTerminalContent(w, state)
				m.LogInfo("Restored terminal state for window %s (%dx%d, %d scrollback lines)",
					w.ID[:8], state.Width, state.Height, state.ScrollbackLen)

				// Note: Resize to trigger redraw is done in TriggerAltScreenRedraws()
				// which is called AFTER SetupPTYOutputHandlers sets up DaemonResizeFunc
			}
		}
	}

	return nil
}

// TriggerAltScreenRedraws forces alt screen apps to redraw.
// This must be called AFTER SetupPTYOutputHandlers so that DaemonResizeFunc is available.
// For alt screen apps (vim, htop, etc.), this invalidates caches and triggers re-render.
func (m *OS) TriggerAltScreenRedraws() {
	for _, w := range m.Windows {
		if w.DaemonMode && w.IsAltScreen {
			termWidth := max(w.Width-2, 1)
			termHeight := max(w.Height-2, 1)

			// Ensure local VT emulator dimensions match
			if w.Terminal != nil {
				w.Terminal.Resize(termWidth, termHeight)
			}

			// Invalidate all caches to force re-render from fresh state
			w.InvalidateCache()
			w.MarkContentDirty()

			m.LogInfo("Invalidated caches for alt screen window %s (%dx%d)",
				w.ID[:8], termWidth, termHeight)
		}
	}

	// Mark all windows dirty to force full redraw
	m.MarkAllDirty()
}

// restoreTerminalContent populates a window's terminal with content from daemon state.
func (m *OS) restoreTerminalContent(w *terminal.Window, state *session.TerminalState) {
	if w.Terminal == nil || state == nil {
		return
	}

	// CRITICAL FIX: Use RestoreAltScreenMode instead of sending escape sequences
	// Sending ESC[?1049h triggers setAltScreenMode() which CLEARS the screen buffer!
	// RestoreAltScreenMode() just switches the buffer pointer without clearing.
	if state.IsAltScreen {
		w.Terminal.RestoreAltScreenMode(true)
		m.LogInfo("Restored alt screen mode for window %s", w.ID[:8])
	}

	// CRITICAL: Restore terminal modes (mouse tracking, bracketed paste, etc.)
	// This must happen AFTER RestoreAltScreenMode so the modes map is properly updated
	// These modes are essential for apps like vim/htop to receive mouse events
	if len(state.Modes) > 0 {
		w.Terminal.RestoreModes(state.Modes)
		m.LogInfo("Restored %d terminal modes for window %s", len(state.Modes), w.ID[:8])
	}

	// Set the window's IsAltScreen flag for mouse event forwarding
	w.IsAltScreen = state.IsAltScreen
	m.LogInfo("Set window IsAltScreen=%v for window %s", state.IsAltScreen, w.ID[:8])

	// For alt screen apps (vim, htop, etc.), DON'T restore cell content manually.
	// Instead, rely on SIGWINCH (triggered by resize in RestoreTerminalStates) to make
	// the app redraw itself. This is cleaner and avoids ANSI leakage issues.
	// Only restore cell content for non-alt-screen terminals (normal shell).
	if !state.IsAltScreen && state.Screen != nil && len(state.Screen) > 0 {
		cellsRestored := 0
		for y := 0; y < len(state.Screen) && y < state.Height; y++ {
			if state.Screen[y] == nil {
				continue
			}
			for x := 0; x < len(state.Screen[y]) && x < state.Width; x++ {
				cellState := state.Screen[y][x]
				// Only restore non-empty cells
				if cellState.Content != "" {
					cell := session.StateToCell(cellState)
					if cell != nil {
						w.Terminal.SetCell(x, y, cell)
						cellsRestored++
					}
				}
			}
		}
		m.LogInfo("Restored %d cells for window %s", cellsRestored, w.ID[:8])
	}

	// Mark content as dirty to trigger rendering
	w.MarkContentDirty()

	// DON'T re-enable callbacks here - they will be enabled after buffered output settles
	// See EnableCallbacksMsg which is sent after 500ms delay
}

// SetupPTYOutputHandlers sets up PTY output handlers for all daemon-mode windows.
// This should be called after RestoreFromState() when attaching to a session.
func (m *OS) SetupPTYOutputHandlers() error {
	if m.DaemonClient == nil {
		m.LogInfo("[SETUP] SetupPTYOutputHandlers: no daemon client")
		return nil
	}

	m.LogInfo("[SETUP] SetupPTYOutputHandlers: setting up handlers for %d windows", len(m.Windows))

	for i, w := range m.Windows {
		m.LogInfo("[SETUP] Window %d: DaemonMode=%v, PTYID=%s", i, w.DaemonMode, w.PTYID)
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

			// Subscribe to PTY output - use async to avoid blocking readLoop
			m.LogInfo("[SETUP] Subscribing to PTY %s for window %d", ptyID[:8], i)
			err := m.DaemonClient.SubscribePTY(ptyID, func(data []byte) {
				m.LogInfo("[OUTPUT] Received %d bytes for PTY %s", len(data), ptyID[:8])
				// Pass through cursor style sequences directly to parent terminal
				// since the VT emulator absorbs them
				passThroughCursorStyle(data)
				window.WriteOutputAsync(data)
			})
			if err != nil {
				m.LogError("Failed to subscribe to PTY %s: %v", ptyID[:8], err)
			} else {
				m.LogInfo("[SETUP] Successfully subscribed to PTY %s", ptyID[:8])
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

// AddDaemonWindow creates a new window using a daemon-managed PTY.
// This is the daemon-mode equivalent of AddWindow.
func (m *OS) AddDaemonWindow(title string) *OS {
	m.LogInfo("[DAEMON] AddDaemonWindow called, DaemonClient=%v", m.DaemonClient != nil)

	if m.DaemonClient == nil {
		m.LogError("Cannot add daemon window: not connected to daemon")
		return m
	}

	newID := createID()
	if title == "" {
		title = "Terminal " + newID[:8]
	}

	m.LogInfo("[DAEMON] Creating new daemon window: %s (workspace %d)", title, m.CurrentWorkspace)

	// Handle case where screen dimensions aren't available yet
	screenWidth := m.GetRenderWidth()
	screenHeight := m.GetUsableHeight()

	if screenWidth == 0 || screenHeight == 0 {
		screenWidth = 80
		screenHeight = 24
		m.LogWarn("Screen dimensions unknown, using defaults (%dx%d)", screenWidth, screenHeight)
	}

	width := screenWidth / 2
	height := screenHeight / 2

	// Calculate position
	var x, y int
	if !m.AutoTiling && m.LastMouseX > 0 && m.LastMouseY > 0 {
		x = m.LastMouseX
		y = m.LastMouseY
		if x+width > screenWidth {
			x = screenWidth - width
		}
		if y+height > screenHeight {
			y = screenHeight - height
		}
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
	} else {
		x = screenWidth / 4
		y = screenHeight / 4
	}

	// Calculate terminal dimensions (accounting for borders)
	termWidth := max(width-2, 1)
	termHeight := max(height-2, 1)

	// Create PTY in daemon
	m.LogInfo("[DAEMON] Calling CreatePTY(%s, %d, %d)", title, termWidth, termHeight)
	ptyID, err := m.DaemonClient.CreatePTY(title, termWidth, termHeight)
	if err != nil {
		m.LogError("[DAEMON] Failed to create PTY in daemon: %v", err)
		return m
	}
	m.LogInfo("[DAEMON] PTY created with ID: %s", ptyID)

	window := terminal.NewDaemonWindow(newID, title, x, y, width, height, len(m.Windows), ptyID)
	if window == nil {
		m.LogError("Failed to create daemon window %s", title)
		_ = m.DaemonClient.ClosePTY(ptyID)
		return m
	}

	caps := GetHostCapabilities()
	if caps.CellWidth > 0 && caps.CellHeight > 0 {
		window.SetCellPixelDimensions(caps.CellWidth, caps.CellHeight)
	}

	window.Workspace = m.CurrentWorkspace

	m.setupKittyPassthrough(window)
	m.setupSixelPassthrough(window)

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

	// Subscribe to PTY output - use async to avoid blocking readLoop
	err = m.DaemonClient.SubscribePTY(ptyID, func(data []byte) {
		// Pass through cursor style sequences directly to parent terminal
		passThroughCursorStyle(data)
		window.WriteOutputAsync(data)
	})
	if err != nil {
		m.LogError("Failed to subscribe to PTY: %v", err)
	}

	// Register handler for when PTY process exits (e.g., Ctrl+D)
	windowID := window.ID
	m.DaemonClient.OnPTYClosed(ptyID, func() {
		// Send window exit notification through the channel
		if m.WindowExitChan != nil {
			m.WindowExitChan <- windowID
		}
	})

	m.Windows = append(m.Windows, window)
	m.LogInfo("Daemon window created: %s (PTY: %s)", title, ptyID[:8])

	// Focus the new window
	m.FocusWindow(len(m.Windows) - 1)

	// Auto-tile if in tiling mode
	if m.AutoTiling {
		m.LogInfo("Auto-tiling triggered for new window")
		tree := m.GetOrCreateBSPTree()
		if tree != nil {
			m.AddWindowToBSPTree(window)
		} else {
			m.TileAllWindows()
		}
	}

	// Sync state to daemon
	m.SyncStateToDaemon()

	return m
}

// DeleteDaemonWindow removes a daemon-mode window and cleans up its PTY.
func (m *OS) DeleteDaemonWindow(i int) *OS {
	if len(m.Windows) == 0 || i < 0 || i >= len(m.Windows) {
		m.LogWarn("Cannot delete window: invalid index %d", i)
		return m
	}

	window := m.Windows[i]

	// Close PTY in daemon if this is a daemon window
	if window.DaemonMode && window.PTYID != "" && m.DaemonClient != nil {
		m.DaemonClient.UnsubscribePTY(window.PTYID)
		if err := m.DaemonClient.ClosePTY(window.PTYID); err != nil {
			m.LogError("Failed to close PTY in daemon: %v", err)
		}
	}

	// Use existing DeleteWindow logic for the rest
	return m.DeleteWindow(i)
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
