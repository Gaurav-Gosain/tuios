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

// calculateSessionReserve returns the chrome reserve that fits every client of
// the session: the largest each edge is asked for. It is deliberately not an
// average or a minimum. A client cannot draw its rail in fewer columns than the
// rail is, and it must not take those columns off the panes, so the only
// reserve every client can honour at once is the biggest one. A client with
// less chrome than that leaves the difference blank.
func (d *Daemon) calculateSessionReserve(sessionID string) LayoutReserve {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	var agreed LayoutReserve
	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.sessionID == sessionID && cs.isTUIClient
		r := cs.reserve
		cs.mu.Unlock()
		if !match {
			continue
		}
		agreed = agreed.Max(r)
	}
	return agreed
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

	// Recalculate effective size and broadcast if changed. The joining client is
	// left out: it is still inside its attach call, reading the one reply it
	// asked for, and an unsolicited message arriving first fails the attach. It
	// gets the size in that reply instead.
	d.recalculateAndBroadcastSize(sessionID, joiningClient.clientID)
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
		d.recalculateAndBroadcastSize(sessionID, leavingClientID)
	}
}

// recalculateAndBroadcastSize recalculates the effective session size and
// broadcasts if changed. excludeClientID is left out of the broadcast, for the
// client that is mid-attach and cannot take an unsolicited message yet.
func (d *Daemon) recalculateAndBroadcastSize(sessionID, excludeClientID string) {
	session := d.manager.GetSessionByID(sessionID)
	if session == nil {
		return
	}

	newWidth, newHeight := d.calculateEffectiveSize(sessionID)
	if newWidth == 0 || newHeight == 0 {
		return
	}
	// The reserve settles with the size, and a change to either has to be
	// announced: the panes' box is the size less the reserve, so a client told
	// only about the size would lay them out in a box nobody else is using.
	newReserve := d.calculateSessionReserve(sessionID)

	oldWidth, oldHeight := session.Size()
	oldReserve := session.LayoutReserve()
	if newWidth == oldWidth && newHeight == oldHeight && newReserve == oldReserve {
		return
	}
	session.Resize(newWidth, newHeight)
	session.SetLayoutReserve(newReserve)

	payload := &SessionResizePayload{
		Width:       newWidth,
		Height:      newHeight,
		ClientCount: d.getSessionClientCount(sessionID),
		Reserve:     newReserve,
	}
	d.broadcastToSession(sessionID, MsgSessionResize, payload, excludeClientID)
	LogBasic("Session %s resized to %dx%d, chrome %+v (min of %d clients)",
		session.Name, newWidth, newHeight, newReserve, payload.ClientCount)
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
