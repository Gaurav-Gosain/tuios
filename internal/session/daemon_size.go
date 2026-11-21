package session

import (
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

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

const (
	minClientWidth  = 20
	minClientHeight = 6
)

func clampClientSize(w, h int) (int, int) {
	return max(w, minClientWidth), max(h, minClientHeight)
}

func (d *Daemon) calculateSessionSize(sessionID string) (width, height int, policy string) {
	width, height, policy, _, _ = d.settleSessionSize(sessionID)
	return width, height, policy
}

func (d *Daemon) settleSessionSize(sessionID string) (width, height int, policy, owner string, kept bool) {
	session := d.manager.GetSessionByID(sessionID)
	clients := d.sessionSizedClients(sessionID)
	policy = d.sessionWindowSize(session)
	clients = sizingClients(policy, clients)
	latest := ""
	if policy == config.WindowSizeLatest {
		latest, kept = d.pickLatest(sessionID, clients, time.Now())
	}
	if kept && session != nil && len(clients) > 0 {
		if width, height = session.Size(); width > 0 && height > 0 {
			return width, height, policy, latest, true
		}
	}
	width, height = sizeForPolicy(policy, clients, latest)
	return width, height, policy, sizeOwner(clients, latest, width, height), false
}

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
		cw, ch := clampClientSize(cs.width, cs.height)
		cs.mu.Unlock()
		if !match {
			continue
		}
		if latest && id != owner {
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

func helloMachine(name string) string {
	if ValidLinkPeerName(name) != nil {
		return ""
	}
	return name
}

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

func (d *Daemon) notifyClientJoined(sessionID string, joiningClient *connState) {
	clientCount := d.getSessionClientCount(sessionID)

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

	_, _, _ = d.recalculateAndBroadcastSize(sessionID, joiningClient.clientID)
}

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

func (d *Daemon) notifyClientLeft(sessionID string, leavingClientID string, input time.Time) {
	d.latestLeft(sessionID, leavingClientID, input)
	d.agentForget(sessionID, leavingClientID)
	d.displayForget(sessionID, leavingClientID)
	d.refreshLinkedViewer(sessionID)
	clientCount := d.getSessionClientCount(sessionID)

	payload := &ClientLeftPayload{
		ClientID:    leavingClientID,
		ClientCount: clientCount,
	}

	d.broadcastToSession(sessionID, MsgClientLeft, payload, leavingClientID)
	if sess := d.manager.GetSessionByID(sessionID); sess != nil {
		sess.wakeStateWaiters()
	}

	if clientCount > 0 {
		_, _, _ = d.recalculateAndBroadcastSize(sessionID, leavingClientID)
	}
}

func (d *Daemon) recalculateAndBroadcastSize(sessionID, excludeClientID string) (width, height int, reserve LayoutReserve) {
	width, height, reserve = d.recalculateSize(sessionID, excludeClientID)
	d.syncSessionCell(sessionID)
	return width, height, reserve
}

func (d *Daemon) recalculateSize(sessionID, excludeClientID string) (width, height int, reserve LayoutReserve) {
	d.layoutMu.Lock()
	defer d.layoutMu.Unlock()

	session := d.manager.GetSessionByID(sessionID)
	if session == nil {
		return 0, 0, LayoutReserve{}
	}

	newWidth, newHeight, policy, owner, kept := d.settleSessionSize(sessionID)
	if newWidth == 0 || newHeight == 0 {
		w, h := session.Size()
		return w, h, session.LayoutReserve()
	}
	newReserve := session.LayoutReserve()
	if !kept {
		newReserve = d.calculateSessionReserve(sessionID, policy, owner, newWidth, newHeight)
	}

	oldWidth, oldHeight := session.Size()
	oldReserve := session.LayoutReserve()
	sizedBy := d.clientLabel(owner)
	policyChanged := session.swapWindowSizePolicy(policy, sizedBy)
	if newWidth == oldWidth && newHeight == oldHeight && newReserve == oldReserve && !policyChanged {
		return newWidth, newHeight, newReserve
	}
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

func (d *Daemon) broadcastStateSync(sessionID string, state *SessionState, triggerType string, sourceClientID string) {
	payload := &StateSyncPayload{
		State:       state,
		TriggerType: triggerType,
		SourceID:    sourceClientID,
	}
	d.broadcastToSession(sessionID, MsgStateSync, payload, sourceClientID)
}

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

func (m *Manager) cellSize(sessionID string) (w, h int, ok bool) {
	if src := m.cellSource.Load(); src != nil {
		return (*src)(sessionID)
	}
	return 0, 0, false
}

func (d *Daemon) syncSessionCell(sessionID string) {
	if w, h, ok := d.sessionCellSize(sessionID); ok {
		d.syncPTYPixelDimensions(d.manager.GetSessionByID(sessionID), w, h)
	}
}

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
