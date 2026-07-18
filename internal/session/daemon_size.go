package session

// getSessionClientCount returns the number of TUI clients attached to a session.
func (d *Daemon) getSessionClientCount(sessionID string) int {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	count := 0
	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.sessionID == sessionID && cs.isTUIClient
		cs.mu.Unlock()
		if match {
			count++
		}
	}
	return count
}

// calculateEffectiveSize returns the minimum dimensions across all clients in a session.
// This is used for multi-client rendering where all clients need to see the same content.
func (d *Daemon) calculateEffectiveSize(sessionID string) (width, height int) {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	width, height = 0, 0
	first := true

	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.sessionID == sessionID && cs.isTUIClient
		cw, ch := cs.width, cs.height
		cs.mu.Unlock()
		if !match {
			continue
		}
		if cw == 0 || ch == 0 {
			continue
		}
		if first {
			width, height = cw, ch
			first = false
		} else {
			if cw < width {
				width = cw
			}
			if ch < height {
				height = ch
			}
		}
	}
	return width, height
}

// notifyClientJoined broadcasts a client join event to all other clients in the session.
func (d *Daemon) notifyClientJoined(sessionID string, joiningClient *connState) {
	clientCount := d.getSessionClientCount(sessionID)

	// width/height are guarded by cs.mu.
	joiningClient.mu.Lock()
	jw, jh := joiningClient.width, joiningClient.height
	joiningClient.mu.Unlock()

	payload := &ClientJoinedPayload{
		ClientID:    joiningClient.clientID,
		ClientCount: clientCount,
		Width:       jw,
		Height:      jh,
	}

	d.broadcastToSession(sessionID, MsgClientJoined, payload, joiningClient.clientID)

	// Recalculate effective size and broadcast if changed
	d.recalculateAndBroadcastSize(sessionID)
}

// notifyClientLeft broadcasts a client leave event to all other clients in the session.
func (d *Daemon) notifyClientLeft(sessionID string, leavingClientID string) {
	clientCount := d.getSessionClientCount(sessionID)

	payload := &ClientLeftPayload{
		ClientID:    leavingClientID,
		ClientCount: clientCount,
	}

	d.broadcastToSession(sessionID, MsgClientLeft, payload, leavingClientID)

	// Recalculate effective size and broadcast if changed
	if clientCount > 0 {
		d.recalculateAndBroadcastSize(sessionID)
	}
}

// recalculateAndBroadcastSize recalculates the effective session size and broadcasts if changed.
func (d *Daemon) recalculateAndBroadcastSize(sessionID string) {
	session := d.manager.GetSessionByID(sessionID)
	if session == nil {
		return
	}

	newWidth, newHeight := d.calculateEffectiveSize(sessionID)
	if newWidth == 0 || newHeight == 0 {
		return
	}

	oldWidth, oldHeight := session.Size()
	if newWidth != oldWidth || newHeight != oldHeight {
		session.Resize(newWidth, newHeight)

		payload := &SessionResizePayload{
			Width:       newWidth,
			Height:      newHeight,
			ClientCount: d.getSessionClientCount(sessionID),
		}
		d.broadcastToSession(sessionID, MsgSessionResize, payload, "")
		LogBasic("Session %s resized to %dx%d (min of %d clients)", session.Name, newWidth, newHeight, payload.ClientCount)
	}
}

// broadcastStateSync broadcasts a state update to all clients in a session.
func (d *Daemon) broadcastStateSync(sessionID string, state *SessionState, triggerType string, sourceClientID string) {
	payload := &StateSyncPayload{
		State:       state,
		TriggerType: triggerType,
		SourceID:    sourceClientID,
	}
	d.broadcastToSession(sessionID, MsgStateSync, payload, sourceClientID)
}

// syncPTYPixelDimensions sets pixel dimensions on all PTYs in a session.
// This is called when a client attaches with terminal graphics capabilities.
func (d *Daemon) syncPTYPixelDimensions(session *Session, cellWidth, cellHeight int) {
	if session == nil || cellWidth <= 0 || cellHeight <= 0 {
		return
	}

	for _, ptyID := range session.ListPTYIDs() {
		if pty := session.GetPTY(ptyID); pty != nil {
			if err := pty.UpdatePixelDimensions(cellWidth, cellHeight); err != nil {
				LogBasic("Failed to set PTY %s pixel size: %v", shortID(ptyID), err)
			}
		}
	}
}
