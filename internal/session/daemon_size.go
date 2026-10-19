package session

import (
	"os"
	"strings"
	"sync"
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
	width, height, policy, _ = d.settleSessionSize(sessionID)
	return width, height, policy
}

// settleSessionSize is calculateSessionSize, and also the client that sets
// the size: the latest client under latest, otherwise the client whose width
// (or, failing that, height) the size took. owner is "" with no clients.
func (d *Daemon) settleSessionSize(sessionID string) (width, height int, policy, owner string) {
	clients := d.sessionSizedClients(sessionID)
	policy = d.effectiveWindowSize(d.manager.GetSessionByID(sessionID), clients)
	clients = sizingClients(policy, clients)
	latest := ""
	if policy == config.WindowSizeLatest {
		latest = d.pickLatest(sessionID, clients, time.Now())
	}
	width, height = sizeForPolicy(policy, clients, latest)
	return width, height, policy, sizeOwner(clients, latest, width, height)
}

// sizeOwner is the client a session's size came from: latest when it is one
// of the clients, otherwise the client that attached first of those that are
// the session's size along both axes. It is "" when no one client is, as
// under smallest with one client setting the width and another the height:
// the hint then names no machine, since either name would be half wrong.
//
// The order is the attach order, never the order clients come in: that is a
// map's, so two clients of one size would swap as the owner from one call to
// the next, and every swap is a broadcast to every client.
func sizeOwner(clients []sizedClient, latest string, width, height int) string {
	for _, c := range clients {
		if latest != "" && c.id == latest {
			return c.id
		}
	}
	var best *sizedClient
	for i := range clients {
		c := &clients[i]
		if w, h := clampClientSize(c.w, c.h); w == width && h == height && (best == nil || c.seq < best.seq) {
			best = c
		}
	}
	if best == nil {
		return ""
	}
	return best.id
}

// calculateSessionReserve returns the chrome reserve the session's panes are
// laid out around.
//
// Under smallest and largest it is the reserve that fits every client of the
// session: the largest each edge is asked for. It is deliberately not an
// average or a minimum. A client cannot draw its rail in fewer columns than
// the rail is, and it must not take those columns off the panes, so the only
// reserve every client can honour at once is the biggest one.
//
// Under latest, an edge counts only the clients that are the session's size
// along its axis: the latest client, and any other client exactly as wide (for
// the left and right edges) or as tall (for the top and bottom). The session
// is the latest client's size, so its panes have to reach its own rail and
// dock. With the largest reserve of every client, a client with a 28 column
// rail beside a smaller one with a 31 column rail left three blank columns
// between its panes and its rail. A client of another size draws the session
// as a view inside its own chrome (see pane_view.go in internal/app), so it
// needs no room in the session for its chrome. A client of the same size has
// no room to spare, so it still counts, as under smallest. So does a client
// whose hello did not offer ViewChrome, such as a build from before this
// rule: it lays the panes out around its own chrome whatever the session
// says, and pushes that layout, so a box without its chrome is a box it
// would not use.
func (d *Daemon) calculateSessionReserve(sessionID, policy, owner string, width, height int) LayoutReserve {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	latest := policy == config.WindowSizeLatest && owner != ""
	var agreed LayoutReserve
	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.sessionID == sessionID && cs.isTUIClient
		r := cs.reserve
		id := cs.clientID
		view := cs.viewChrome
		cw, ch := clampClientSize(cs.width, cs.height)
		cs.mu.Unlock()
		if !match {
			continue
		}
		if latest && view && id != owner {
			if cw != width {
				r.Left, r.Right = 0, 0
			}
			if ch != height {
				r.Top, r.Bottom = 0, 0
			}
		}
		agreed = agreed.Max(r)
	}
	return agreed
}

// clientLabel names a client for the hint a larger client shows. Its
// machine is the link peer for a client that came over a link, then the host
// name the client's hello gave, then this machine's host name for a client
// from before that field. Its kind is the hello's ClientKind. A zero
// SizeOwner when the client is not known.
func (d *Daemon) clientLabel(clientID string) SizeOwner {
	if clientID == "" {
		return SizeOwner{}
	}
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		id, peer, machine, kind, link := cs.clientID, cs.linkPeer, cs.machine, cs.kind, cs.viaLink
		cs.mu.Unlock()
		if id != clientID {
			continue
		}
		owner := SizeOwner{Kind: kind, Client: clientID}
		switch {
		case peer != "":
			owner.Machine = peer
		case machine != "":
			owner.Machine = machine
		case !link:
			owner.Machine = localHostLabel()
		}
		return owner
	}
	return SizeOwner{}
}

// helloMachine is the hello's Machine when it is a valid machine name, the
// rule a link peer's name follows, and "" otherwise. It is shown to other
// clients, and over a link it comes from another machine.
func helloMachine(name string) string {
	if ValidLinkPeerName(name) != nil {
		return ""
	}
	return name
}

// helloKind is the hello's ClientKind when it is one the hint names, and ""
// otherwise.
func helloKind(kind string) string {
	switch kind {
	case ClientKindSSH, ClientKindWeb:
		return kind
	}
	return ""
}

// The client kinds a hello can name. See HelloPayload.ClientKind.
const (
	ClientKindSSH = "ssh"
	ClientKindWeb = "web"
)

// localHostLabel is this machine's short host name, or "" when it cannot be
// read.
var localHostLabel = sync.OnceValue(func() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	if i := strings.IndexByte(name, '.'); i > 0 {
		name = name[:i]
	}
	return name
})

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
	// Every leave comes here, a detach and a dropped connection alike, so
	// this is where the agent link moves back to the client before.
	d.agentForget(sessionID, leavingClientID)
	d.displayForget(sessionID, leavingClientID)
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
	width, height, reserve = d.recalculateSize(sessionID, excludeClientID)
	// Every change to who is attached, and to which client owns the size,
	// comes through here, so the cell the panes take follows the same owner.
	// It runs after layoutMu is released: it takes each PTY's locks.
	d.syncSessionCell(sessionID)
	return width, height, reserve
}

// recalculateSize is recalculateAndBroadcastSize less the cell sync.
func (d *Daemon) recalculateSize(sessionID, excludeClientID string) (width, height int, reserve LayoutReserve) {
	d.layoutMu.Lock()
	defer d.layoutMu.Unlock()

	session := d.manager.GetSessionByID(sessionID)
	if session == nil {
		return 0, 0, LayoutReserve{}
	}

	newWidth, newHeight, policy, owner := d.settleSessionSize(sessionID)
	if newWidth == 0 || newHeight == 0 {
		// Nothing known to measure. Report what the session already holds, so a
		// caller stamping a reply is never handed a zero.
		w, h := session.Size()
		return w, h, session.LayoutReserve()
	}
	// The reserve settles with the size, and a change to either has to be
	// announced: the panes' box is the size less the reserve, so a client told
	// only about the size would lay them out in a box nobody else is using.
	newReserve := d.calculateSessionReserve(sessionID, policy, owner, newWidth, newHeight)

	oldWidth, oldHeight := session.Size()
	oldReserve := session.LayoutReserve()
	sizedBy := d.clientLabel(owner)
	policyChanged := session.swapWindowSizePolicy(policy, sizedBy)
	if newWidth == oldWidth && newHeight == oldHeight && newReserve == oldReserve && !policyChanged {
		return newWidth, newHeight, newReserve
	}
	// Recorded and stamped as one step, under the same lock the whole of this
	// function holds, so the generation on the wire and the state it describes
	// cannot disagree.
	gen := session.SettleLayout(newWidth, newHeight, newReserve)

	payload := &SessionResizePayload{
		Width:         newWidth,
		Height:        newHeight,
		ClientCount:   d.getSessionClientCount(sessionID),
		Reserve:       newReserve,
		Generation:    gen,
		Policy:        policy,
		SizedBy:       sizedBy.Machine,
		SizedByClient: sizedBy.Client,
		SizedByKind:   sizedBy.Kind,
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

// sessionCellSize is the cell, in pixels, that the session's panes take. It
// is one client's cell, never a mix: clients with different fonts used to
// set it in turn, on every resize and attach, and a guest saw its pixel size
// flip and was sent a 2048 report each time.
//
// The client is the one that owns the session's size when there is one, the
// latest client under window_size = latest, because the pane is laid out for
// that client's screen. Under smallest and largest no single client owns the
// size, and the client that attached first is used, so the cell holds still
// while others come and go. A client that reported no cell is skipped. ok is
// false when no attached client reported one; the panes then keep the cell
// they have, the fallback cell for one no client has measured.
func (d *Daemon) sessionCellSize(sessionID string) (w, h int, ok bool) {
	owner := ""
	if s := d.manager.GetSessionByID(sessionID); s != nil && s.WindowSizePolicy() == config.WindowSizeLatest {
		d.latest.mu.Lock()
		if st := d.latest.sessions[sessionID]; st != nil {
			owner = st.client
		}
		d.latest.mu.Unlock()
	}

	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	var firstSeq uint64
	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.sessionID == sessionID && cs.isTUIClient && !cs.passive && cs.cellWidth > 0 && cs.cellHeight > 0
		id, seq, cw, ch := cs.clientID, cs.attachSeq, cs.cellWidth, cs.cellHeight
		cs.mu.Unlock()
		if !match {
			continue
		}
		if owner != "" && id == owner {
			return cw, ch, true
		}
		if !ok || seq < firstSeq {
			w, h, firstSeq, ok = cw, ch, seq, true
		}
	}
	return w, h, ok
}

// newPaneCellSize is the cell a pane takes when it is made: the session's
// (see sessionCellSize), or, in a session no measuring client is attached to,
// the cell of the client that attached first to any session. A pane an agent
// opens in a background session is drawn for that screen when someone looks,
// and a cell measured on it is nearer than the fallback, which no program can
// tell from a real one.
//
// Only a new pane takes the cell from elsewhere. The panes already in a
// session keep theirs when its last client leaves: switching them would send
// every program there a 2048 report and a redraw nobody sees, and another
// when a client comes back.
func (d *Daemon) newPaneCellSize(sessionID string) (w, h int, ok bool) {
	if w, h, ok = d.sessionCellSize(sessionID); ok {
		return w, h, ok
	}
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	var firstSeq uint64
	for _, cs := range d.clients {
		cs.mu.Lock()
		measured := cs.sessionID != "" && cs.isTUIClient && !cs.passive && cs.cellWidth > 0 && cs.cellHeight > 0
		seq, cw, ch := cs.attachSeq, cs.cellWidth, cs.cellHeight
		cs.mu.Unlock()
		if measured && (!ok || seq < firstSeq) {
			w, h, firstSeq, ok = cw, ch, seq, true
		}
	}
	return w, h, ok
}

// cellSize is the cell a new pane of the session takes. See
// Daemon.newPaneCellSize.
func (m *Manager) cellSize(sessionID string) (w, h int, ok bool) {
	if src := m.cellSource.Load(); src != nil {
		return (*src)(sessionID)
	}
	return 0, 0, false
}

// syncSessionCell gives every pane of the session the session's cell (see
// sessionCellSize). A pane that has it already is left alone.
func (d *Daemon) syncSessionCell(sessionID string) {
	if w, h, ok := d.sessionCellSize(sessionID); ok {
		d.syncPTYPixelDimensions(d.manager.GetSessionByID(sessionID), w, h)
	}
}

// syncPTYPixelDimensions sets pixel dimensions on all PTYs in a session.
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
