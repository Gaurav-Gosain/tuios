package session

import (
	"fmt"
	"time"
)

// daemonOwnedCommands are the commands the daemon executes itself whether or not
// a client is attached, because their entire effect is a change to a field the
// daemon owns and no renderer has to be consulted to make it. Routing one of
// these to the client instead is what made a remote rename report success while
// list-windows kept reporting the old name: the client renamed its own copy and
// the daemon, which every read verb answers from, never heard about it.
//
// Commands leave this set only when the daemon cannot produce the same result a
// renderer would. Everything still absent from it is routed as before.
var daemonOwnedCommands = map[string]bool{
	"RenameWindow": true,
	// Closing a window is removing it from the window set and killing its PTY,
	// both of which the daemon owns outright. The renderer has nothing to
	// contribute: it learns the window is gone from the state push and gives the
	// space back.
	"CloseWindow": true,
	// Creating one is the same trade in reverse. The daemon spawns the PTY and
	// adds the window; the one thing it cannot supply is where the window goes,
	// because it has no viewport. It says so with WindowState.Unplaced instead of
	// guessing, and the client that receives the push places it.
	"NewWindow": true,
}

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

	// Find the TUI client attached to this session. When one is present most
	// commands are routed to it (unchanged behavior). With no client attached,
	// structural verbs execute directly against daemon-owned state.
	tuiClient := d.findTUIClient(session.ID)
	if tuiClient == nil || daemonOwnedCommands[payload.CommandType] {
		if payload.TapeScript != "" {
			return d.sendCommandResult(cs, payload.RequestID, false,
				"tape scripts need an attached client. A headless daemon has no renderer to run them")
		}
		onExit := func(ptyID string) { d.notifyPTYClosed(session.ID, ptyID) }
		data, err := d.executeDaemonCommand(session, payload.CommandType, payload.Args, onExit)
		if err != nil {
			return d.sendCommandResult(cs, payload.RequestID, false, err.Error())
		}
		// The attached client, if any, has already been told: the mutation went
		// through Session.mutateState, whose state sink broadcasts to it.
		return d.sendMessage(cs, MsgCommandResult, &CommandResultPayload{
			RequestID: payload.RequestID,
			Success:   true,
			Message:   "command executed",
			Data:      data,
		})
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

	// Deliver to a JSON verb handler blocked in routeToTUISync, if any.
	if found && pending != nil && pending.resultCh != nil {
		result := payload
		select {
		case pending.resultCh <- &result:
		default:
			// The waiter already gave up (timeout/disconnect); drop the result.
		}
		return nil
	}

	// Forward the result to the original requester
	if found && pending != nil && pending.requester != nil {
		requester := pending.requester
		LogBasic("Forwarding result to original requester %s", requester.clientID)
		return d.sendMessage(requester, MsgCommandResult, &payload)
	}

	return nil
}

// routeToTUISync sends a remote command to an attached TUI and blocks until the
// TUI replies with its result, a timeout elapses, or the daemon shuts down. It
// is the synchronous bridge the JSON verb front-end uses so a control verb that
// must be handled by the live renderer (WM keys, structural changes, live config)
// still returns a single request/response over the JSON connection. requestID
// must be unique per in-flight call.
func (d *Daemon) routeToTUISync(tui *connState, requestID string, cmd *RemoteCommandPayload, timeout time.Duration) (*CommandResultPayload, error) {
	// Stamp the request ID onto the outgoing command so the TUI echoes it back on
	// its result and handleCommandResult can match it to this pending waiter.
	cmd.RequestID = requestID

	ch := make(chan *CommandResultPayload, 1)

	d.pendingRequestsMu.Lock()
	d.pendingRequests[requestID] = &pendingRequest{resultCh: ch, created: time.Now()}
	d.pendingRequestsMu.Unlock()

	clearPending := func() {
		d.pendingRequestsMu.Lock()
		delete(d.pendingRequests, requestID)
		d.pendingRequestsMu.Unlock()
	}

	if err := d.sendMessage(tui, MsgRemoteCommand, cmd); err != nil {
		clearPending()
		return nil, fmt.Errorf("failed to reach the attached client: %w", err)
	}

	select {
	case res := <-ch:
		return res, nil
	case <-time.After(timeout):
		clearPending()
		return nil, fmt.Errorf("timed out waiting for the attached client")
	case <-d.ctx.Done():
		clearPending()
		return nil, fmt.Errorf("daemon shutting down")
	}
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

// findTUIClient finds the TUI client attached to a session, and never one that
// is still attaching: a routed command is an unsolicited message, and a client
// inside its attach call has no read loop to tell one from its own reply. See
// connState.attached.
func (d *Daemon) findTUIClient(sessionID string) *connState {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.sessionID == sessionID && cs.isTUIClient && cs.attached
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
