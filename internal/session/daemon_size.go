package session

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

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

// minClientWidth and minClientHeight are the smallest size a client counts as
// in the session's size. A smaller client is counted at this size.
//
// It is the guard against the loop nested_attach.go describes, for the client
// the daemon could not place in a pane. That client's size is the session's
// less the chrome, so without a floor the minimum ratchets down to 1x1 and the
// client redraws without end. With a floor the ratchet stops: the client
// reports less than the floor, it counts as the floor, the session keeps the
// size it already has, and nothing is broadcast. A value below the floor is
// clamped rather than ignored, because ignoring it grows the session back,
// which grows the pane, which lets the client count again: a loop that never
// settles.
//
// The floor is well under the smallest terminal the client starts in (40x12,
// see checkTerminalSize), so a real small client is not overruled by it.
const (
	minClientWidth  = 20
	minClientHeight = 6
)

// clampClientSize applies the floor to one client's size.
func clampClientSize(w, h int) (int, int) {
	return max(w, minClientWidth), max(h, minClientHeight)
}

// calculateSessionSize returns the session's size under its window_size
// policy (see window_size.go): the smallest client, the largest, or the
// latest, and the policy in force. Each client counts at no less than
// minClientWidth x minClientHeight.
func (d *Daemon) calculateSessionSize(sessionID string) (width, height int, policy string) {
	clients := d.sessionSizedClients(sessionID)
	policy = d.effectiveWindowSize(d.manager.GetSessionByID(sessionID), clients)
	clients = sizingClients(policy, clients)
	latest := ""
	if policy == config.WindowSizeLatest {
		latest = d.pickLatest(sessionID, clients, time.Now())
	}
	width, height = sizeForPolicy(policy, clients, latest)
	return width, height, policy
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
	_, _, _ = d.recalculateAndBroadcastSize(sessionID, joiningClient.clientID)
}

// refreshLinkedViewer records on a session whether any TUI client drawing it
// arrived over a link, from another machine. It runs whenever a client joins
// or leaves, so the answer the session's panes give a kitty graphics query
// follows the clients actually attached. See Session.kittyQueryResponse.
func (d *Daemon) refreshLinkedViewer(sessionID string) {
	session := d.manager.GetSessionByID(sessionID)
	if session == nil {
		return
	}
	linked := false
	d.clientsMu.RLock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.sessionID == sessionID && cs.isTUIClient && cs.viaLink
		cs.mu.Unlock()
		if match {
			linked = true
			break
		}
	}
	d.clientsMu.RUnlock()
	session.SetLinkedViewer(linked)
}

// notifyClientLeft broadcasts a client leave event to all other clients in the session.
func (d *Daemon) notifyClientLeft(sessionID string, leavingClientID string) {
	d.refreshLinkedViewer(sessionID)
	clientCount := d.getSessionClientCount(sessionID)

	payload := &ClientLeftPayload{
		ClientID:    leavingClientID,
		ClientCount: clientCount,
	}

	d.broadcastToSession(sessionID, MsgClientLeft, payload, leavingClientID)

	// Recalculate effective size and broadcast if changed
	if clientCount > 0 {
		_, _, _ = d.recalculateAndBroadcastSize(sessionID, leavingClientID)
	}
}

// recalculateAndBroadcastSize recalculates what the session measures (its
// effective size and the chrome reserve its clients lay panes out around),
// records it, and tells everyone if either moved. It returns what it settled
// on, so a caller that also has to put those numbers in a reply does not
// compute them a second time.
//
// The whole of it runs under layoutMu, and that is the point rather than an
// implementation detail. Two of these can run at once (a client announcing its
// chrome and another client attaching are different connections on different
// goroutines), and each is a read over every client followed by a write. Run
// interleaved they record an answer computed from a client set that no longer
// exists: measured, the session's reserve flapped between the dock's two rows
// and nothing at all, because the attaching client's recalculation read the
// other client's reserve just before that client's own recalculation wrote it.
//
// excludeClientID is left out of the broadcast. It is the client whose own
// action caused the change and which is being answered directly instead.
func (d *Daemon) recalculateAndBroadcastSize(sessionID, excludeClientID string) (width, height int, reserve LayoutReserve) {
	d.layoutMu.Lock()
	defer d.layoutMu.Unlock()

	session := d.manager.GetSessionByID(sessionID)
	if session == nil {
		return 0, 0, LayoutReserve{}
	}

	newWidth, newHeight, policy := d.calculateSessionSize(sessionID)
	if newWidth == 0 || newHeight == 0 {
		// Nothing known to measure. Report what the session already holds, so a
		// caller stamping a reply is never handed a zero.
		w, h := session.Size()
		return w, h, session.LayoutReserve()
	}
	// The reserve settles with the size, and a change to either has to be
	// announced: the panes' box is the size less the reserve, so a client told
	// only about the size would lay them out in a box nobody else is using.
	newReserve := d.calculateSessionReserve(sessionID)

	oldWidth, oldHeight := session.Size()
	oldReserve := session.LayoutReserve()
	policyChanged := session.swapWindowSizePolicy(policy)
	if newWidth == oldWidth && newHeight == oldHeight && newReserve == oldReserve && !policyChanged {
		return newWidth, newHeight, newReserve
	}
	// Recorded and stamped as one step, under the same lock the whole of this
	// function holds, so the generation on the wire and the state it describes
	// cannot disagree.
	gen := session.SettleLayout(newWidth, newHeight, newReserve)

	payload := &SessionResizePayload{
		Width:       newWidth,
		Height:      newHeight,
		ClientCount: d.getSessionClientCount(sessionID),
		Reserve:     newReserve,
		Generation:  gen,
		Policy:      policy,
	}
	d.broadcastToSession(sessionID, MsgSessionResize, payload, excludeClientID)
	LogBasic("Session %s resized to %dx%d, chrome %+v (%s of %d clients)",
		session.Name(), newWidth, newHeight, newReserve, policy, payload.ClientCount)
	return newWidth, newHeight, newReserve
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
