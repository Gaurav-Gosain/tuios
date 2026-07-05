package session

import (
	"fmt"
	"strings"
	"time"
)

// handleExecuteCommand routes a tape command to the TUI client attached to the session.
func (d *Daemon) handleExecuteCommand(cs *connState, msg *Message) error {
	var payload ExecuteCommandPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid execute command payload: %w", err)
	}

	LogBasic("Received execute command: %s (session=%s, args=%v)", payload.CommandType, payload.SessionName, payload.Args)

	// Find the target session
	session := d.findTargetSession(payload.SessionName)
	if session == nil {
		LogBasic("Execute command: session not found")
		return d.sendCommandResult(cs, payload.RequestID, false, "session not found")
	}
	LogBasic("Execute command: found session %s (ID=%s)", session.Name, session.ID)

	// Find the TUI client attached to this session
	tuiClient := d.findTUIClient(session.ID)
	if tuiClient == nil {
		LogBasic("Execute command: no TUI client found for session %s", session.ID)
		return d.sendCommandResult(cs, payload.RequestID, false, "no TUI client attached to session")
	}
	LogBasic("Execute command: found TUI client %s", tuiClient.clientID)

	// Forward the command to the TUI client
	var remoteCmd *RemoteCommandPayload
	if payload.TapeScript != "" {
		// Execute a full tape script
		remoteCmd = &RemoteCommandPayload{
			RequestID:   payload.RequestID,
			CommandType: "tape_script",
			TapeScript:  payload.TapeScript,
		}
	} else {
		// Execute a single tape command
		remoteCmd = &RemoteCommandPayload{
			RequestID:   payload.RequestID,
			CommandType: "tape_command",
			TapeCommand: payload.CommandType,
			TapeArgs:    payload.Args,
		}
	}

	if err := d.sendMessage(tuiClient, MsgRemoteCommand, remoteCmd); err != nil {
		return d.sendCommandResult(cs, payload.RequestID, false, fmt.Sprintf("failed to send to TUI: %v", err))
	}

	// Track this request so we can route the result back to the original client
	if cs.clientID != tuiClient.clientID {
		d.pendingRequestsMu.Lock()
		d.pendingRequests[payload.RequestID] = &pendingRequest{requester: cs, created: time.Now()}
		d.pendingRequestsMu.Unlock()
	}

	// Don't send response here - wait for TUI to send result via handleCommandResult
	return nil
}

// handleSendKeys routes keystrokes to the TUI client attached to the session.
func (d *Daemon) handleSendKeys(cs *connState, msg *Message) error {
	var payload SendKeysPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid send keys payload: %w", err)
	}

	// Find the target session
	session := d.findTargetSession(payload.SessionName)
	if session == nil {
		return d.sendCommandResult(cs, payload.RequestID, false, "session not found")
	}

	// Find the TUI client attached to this session
	tuiClient := d.findTUIClient(session.ID)
	if tuiClient == nil {
		return d.sendCommandResult(cs, payload.RequestID, false, "no TUI client attached to session")
	}

	// Forward the command to the TUI client
	remoteCmd := &RemoteCommandPayload{
		RequestID:    payload.RequestID,
		CommandType:  "send_keys",
		Keys:         payload.Keys,
		Literal:      payload.Literal,
		Raw:          payload.Raw,
		WindowTarget: payload.WindowTarget,
	}

	if err := d.sendMessage(tuiClient, MsgRemoteCommand, remoteCmd); err != nil {
		return d.sendCommandResult(cs, payload.RequestID, false, fmt.Sprintf("failed to send to TUI: %v", err))
	}

	// Track this request so we can route the result back to the original client
	if cs.clientID != tuiClient.clientID {
		d.pendingRequestsMu.Lock()
		d.pendingRequests[payload.RequestID] = &pendingRequest{requester: cs, created: time.Now()}
		d.pendingRequestsMu.Unlock()
	}

	// Don't send response here - wait for TUI to send result via handleCommandResult
	return nil
}

// handleCapturePane routes a capture-pane request to the TUI client.
func (d *Daemon) handleCapturePane(cs *connState, msg *Message) error {
	var payload CapturePanePayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid capture pane payload: %w", err)
	}

	session := d.findTargetSession(payload.SessionName)
	if session == nil {
		return d.sendCommandResult(cs, payload.RequestID, false, "session not found")
	}

	tuiClient := d.findTUIClient(session.ID)
	if tuiClient == nil {
		return d.sendCommandResult(cs, payload.RequestID, false, "no TUI client attached to session")
	}

	remoteCmd := &RemoteCommandPayload{
		RequestID:    payload.RequestID,
		CommandType:  "capture_pane",
		WindowTarget: payload.WindowTarget,
	}
	// Pack options into Keys field as a simple flag string
	var flags []string
	if payload.Scrollback {
		flags = append(flags, "scrollback")
	}
	if payload.ANSI {
		flags = append(flags, "ansi")
	}
	if len(flags) > 0 {
		remoteCmd.Keys = strings.Join(flags, ",")
	}

	if err := d.sendMessage(tuiClient, MsgRemoteCommand, remoteCmd); err != nil {
		return d.sendCommandResult(cs, payload.RequestID, false, fmt.Sprintf("failed to send to TUI: %v", err))
	}

	if cs.clientID != tuiClient.clientID {
		d.pendingRequestsMu.Lock()
		d.pendingRequests[payload.RequestID] = &pendingRequest{requester: cs, created: time.Now()}
		d.pendingRequestsMu.Unlock()
	}

	return nil
}

// handleSetConfig routes a config change to the TUI client attached to the session.
func (d *Daemon) handleSetConfig(cs *connState, msg *Message) error {
	var payload SetConfigPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid set config payload: %w", err)
	}

	// Find the target session
	session := d.findTargetSession(payload.SessionName)
	if session == nil {
		return d.sendCommandResult(cs, payload.RequestID, false, "session not found")
	}

	// Find the TUI client attached to this session
	tuiClient := d.findTUIClient(session.ID)
	if tuiClient == nil {
		return d.sendCommandResult(cs, payload.RequestID, false, "no TUI client attached to session")
	}

	// Forward the command to the TUI client
	remoteCmd := &RemoteCommandPayload{
		RequestID:   payload.RequestID,
		CommandType: "set_config",
		ConfigPath:  payload.Path,
		ConfigValue: payload.Value,
	}

	if err := d.sendMessage(tuiClient, MsgRemoteCommand, remoteCmd); err != nil {
		return d.sendCommandResult(cs, payload.RequestID, false, fmt.Sprintf("failed to send to TUI: %v", err))
	}

	// Track this request so we can route the result back to the original client
	if cs.clientID != tuiClient.clientID {
		d.pendingRequestsMu.Lock()
		d.pendingRequests[payload.RequestID] = &pendingRequest{requester: cs, created: time.Now()}
		d.pendingRequestsMu.Unlock()
	}

	// Don't send response here - wait for TUI to send result via handleCommandResult
	return nil
}

// handleCommandResult handles command results from TUI clients.
// Forwards results back to the original requester if there's a pending request.
func (d *Daemon) handleCommandResult(cs *connState, msg *Message) error {
	var payload CommandResultPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid command result payload: %w", err)
	}

	if payload.Success {
		LogBasic("Command %s succeeded: %s (data keys: %d)", payload.RequestID, payload.Message, len(payload.Data))
		for k, v := range payload.Data {
			LogBasic("  Data[%s] = %v", k, v)
		}
	} else {
		LogBasic("Command %s failed: %s", payload.RequestID, payload.Message)
	}

	// Check if there's a pending request from another client waiting for this result
	d.pendingRequestsMu.Lock()
	pending, found := d.pendingRequests[payload.RequestID]
	if found {
		delete(d.pendingRequests, payload.RequestID)
	}
	d.pendingRequestsMu.Unlock()

	// Forward the result to the original requester
	if found && pending != nil && pending.requester != nil {
		requester := pending.requester
		LogBasic("Forwarding result to original requester %s", requester.clientID)
		return d.sendMessage(requester, MsgCommandResult, &payload)
	}

	return nil
}

// findTargetSession finds a session by name, or returns the most recently active session.
func (d *Daemon) findTargetSession(sessionName string) *Session {
	if sessionName != "" {
		return d.manager.GetSession(sessionName)
	}

	// Find the most recently active session
	sessions := d.manager.ListSessions()
	if len(sessions) == 0 {
		return nil
	}

	var mostRecent *Session
	var mostRecentTime int64 = 0

	for _, info := range sessions {
		if info.LastActive > mostRecentTime {
			mostRecentTime = info.LastActive
			mostRecent = d.manager.GetSession(info.Name)
		}
	}

	return mostRecent
}

// findTUIClient finds the TUI client attached to a session.
func (d *Daemon) findTUIClient(sessionID string) *connState {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.sessionID == sessionID && cs.isTUIClient
		cs.mu.Unlock()
		if match {
			return cs
		}
	}

	return nil
}

// sendCommandResult sends a command result to a client.
func (d *Daemon) sendCommandResult(cs *connState, requestID string, success bool, message string) error {
	return d.sendMessage(cs, MsgCommandResult, &CommandResultPayload{
		RequestID: requestID,
		Success:   success,
		Message:   message,
	})
}

// handleGetLogs retrieves recent log entries from the daemon's log buffer.
func (d *Daemon) handleGetLogs(cs *connState, msg *Message) error {
	var payload GetLogsPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid get logs payload: %w", err)
	}

	entries := GetLogEntries(payload.Count)

	if payload.Clear {
		ClearLogBuffer()
	}

	return d.sendMessage(cs, MsgLogsData, &LogsDataPayload{
		Entries: entries,
	})
}

// handleQueryWindows returns window list from session state.
// Works even without a TUI client attached by using the daemon's stored state.
func (d *Daemon) handleQueryWindows(cs *connState, msg *Message) error {
	var payload QueryWindowsPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid query windows payload: %w", err)
	}

	// Find the target session
	session := d.findTargetSession(payload.SessionName)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	// Get state from session (daemon stores this)
	state := session.GetState()

	// Build window list from state
	windows := make([]map[string]any, 0, len(state.Windows))
	for i, w := range state.Windows {
		displayName := w.Title
		if w.CustomName != "" {
			displayName = w.CustomName
		}

		winInfo := map[string]any{
			"window_id":    w.ID,
			"index":        i,
			"title":        w.Title,
			"display_name": displayName,
			"workspace":    w.Workspace,
			"minimized":    w.Minimized,
			"focused":      w.ID == state.FocusedWindowID,
			"x":            w.X,
			"y":            w.Y,
			"width":        w.Width,
			"height":       w.Height,
		}
		if w.CustomName != "" {
			winInfo["custom_name"] = w.CustomName
		}
		windows = append(windows, winInfo)
	}

	// Count windows per workspace
	workspaceWindows := make([]int, 9) // Assume 9 workspaces
	for _, w := range state.Windows {
		if w.Workspace >= 1 && w.Workspace <= 9 {
			workspaceWindows[w.Workspace-1]++
		}
	}

	// Find focused index
	focusedIndex := -1
	for i, w := range state.Windows {
		if w.ID == state.FocusedWindowID {
			focusedIndex = i
			break
		}
	}

	resultData := map[string]any{
		"windows":           windows,
		"total":             len(state.Windows),
		"focused_index":     focusedIndex,
		"focused_window_id": state.FocusedWindowID,
		"current_workspace": state.CurrentWorkspace,
		"workspace_windows": workspaceWindows,
	}

	return d.sendMessage(cs, MsgCommandResult, &CommandResultPayload{
		RequestID: payload.RequestID,
		Success:   true,
		Message:   "command executed",
		Data:      resultData,
	})
}

// handleQuerySession returns session info from daemon's stored state.
// Works even without a TUI client attached.
func (d *Daemon) handleQuerySession(cs *connState, msg *Message) error {
	var payload QuerySessionPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid query session payload: %w", err)
	}

	// Find the target session
	session := d.findTargetSession(payload.SessionName)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	// Get state from session
	state := session.GetState()

	// Check if TUI is attached
	tuiClient := d.findTUIClient(session.ID)
	hasClient := tuiClient != nil

	// Build session info from state
	tilingMode := "floating"
	if state.AutoTiling {
		tilingMode = "tiling"
	}

	resultData := map[string]any{
		"session_name":      state.Name,
		"session_id":        session.ID,
		"mode":              "unknown", // Can't know mode without TUI
		"current_workspace": state.CurrentWorkspace,
		"num_workspaces":    9, // Default
		"window_count":      len(state.Windows),
		"tiling_mode":       tilingMode,
		"master_ratio":      state.MasterRatio,
		"width":             state.Width,
		"height":            state.Height,
		"tui_attached":      hasClient,
	}

	return d.sendMessage(cs, MsgCommandResult, &CommandResultPayload{
		RequestID: payload.RequestID,
		Success:   true,
		Message:   "command executed",
		Data:      resultData,
	})
}

// handleWindowListResponse handles window list from TUI and forwards to requesting client.
func (d *Daemon) handleWindowListResponse(cs *connState, msg *Message) error {
	var payload WindowListPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid window list payload: %w", err)
	}

	// For now, the daemon just logs this. In a full implementation,
	// we would route this back to the original requesting client.
	LogBasic("Received window list: %d windows", payload.Total)

	return nil
}

// handleSessionInfoResponse handles session info from TUI.
func (d *Daemon) handleSessionInfoResponse(cs *connState, msg *Message) error {
	var payload SessionInfoPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid session info payload: %w", err)
	}

	LogBasic("Received session info: %s, %d windows", payload.SessionName, payload.TotalWindows)

	return nil
}
