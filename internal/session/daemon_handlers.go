package session

import (
	"fmt"
	"log"
	"sync/atomic"
)

func (d *Daemon) handleHello(cs *connState, msg *Message) error {
	var payload HelloPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return fmt.Errorf("invalid hello payload: %w", err)
	}

	cs.hello = &payload
	cs.mu.Lock()
	changed := cs.treeOps != payload.LayoutTreeOps
	cs.treeOps = payload.LayoutTreeOps
	attachedTo := cs.sessionID
	cs.mu.Unlock()
	// A second hello on an attached connection can change what the client
	// sends, and with it what the session can run.
	if changed && attachedTo != "" {
		d.refreshTreeOps(attachedTo)
	}

	// Refuse a client this daemon cannot serve before it can attach to anything.
	if protocolMismatch(payload.Protocol) {
		LogBasic("Client %s refused: speaks protocol %d, this daemon serves %d..%d",
			cs.clientID, peerProtocol(payload.Protocol), MinProtocolVersion, ProtocolVersion)
		return d.sendError(cs, ErrCodeInvalidMessage, clientProtocolRefusal(d.version, &payload))
	}

	// Store client's graphics capabilities for PTY pixel size reporting
	cs.pixelWidth = payload.PixelWidth
	cs.pixelHeight = payload.PixelHeight
	cs.cellWidth = payload.CellWidth
	cs.cellHeight = payload.CellHeight
	cs.kittyGraphics = payload.KittyGraphics
	cs.sixelGraphics = payload.SixelGraphics
	cs.terminalName = payload.TerminalName

	if payload.CellWidth > 0 && payload.CellHeight > 0 {
		LogBasic("Client %s capabilities: cell=%dx%d pixels, kitty=%v, sixel=%v, term=%s",
			cs.clientID, payload.CellWidth, payload.CellHeight, payload.KittyGraphics, payload.SixelGraphics, payload.TerminalName)
	}

	// gob is the only payload codec. PreferredCodec stays in the handshake
	// for wire stability but cannot select anything else.
	sessions := d.manager.ListSessions()
	names := make([]string, len(sessions))
	for i, s := range sessions {
		names[i] = s.Name
	}

	return d.sendMessage(cs, MsgWelcome, &WelcomePayload{
		Version:      d.version,
		SessionNames: names,
		Codec:        wireCodecName,
		Protocol:     ProtocolVersion,
		ClientFocus:  true,
		// See layout_tree.go.
		LayoutTreeOps: true,
	})
}

// attachSnapshotTaken runs in handleAttach between the state snapshot and the
// reply that carries it. It is unset outside tests, which use it to land a
// state change in that window on purpose. It is atomic because the test sets
// it while the daemon's connection goroutines read it.
var attachSnapshotTaken atomic.Pointer[func()]

// attachReplied runs in handleAttach just after the reply is written, before
// the checks that repair what the reply missed. It is unset outside tests,
// which use it to land a state change after the client has joined the
// broadcast set, and atomic for the reason attachSnapshotTaken is.
var attachReplied atomic.Pointer[func()]

func (d *Daemon) handleAttach(cs *connState, msg *Message) error {
	var payload AttachPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return fmt.Errorf("invalid attach payload: %w", err)
	}

	cfg := &SessionConfig{}
	if cs.hello != nil {
		cfg.Term = cs.hello.Term
		cfg.ColorTerm = cs.hello.ColorTerm
		cfg.Shell = cs.hello.Shell
	}

	var session *Session
	var err error

	if payload.SessionName == "" {
		session, err = d.manager.GetDefaultSession(cfg, payload.Width, payload.Height)
	} else if payload.CreateNew {
		session, _, err = d.manager.GetOrCreateSession(payload.SessionName, cfg, payload.Width, payload.Height)
	} else {
		session = d.manager.GetSession(payload.SessionName)
		if session == nil {
			return d.sendError(cs, ErrCodeSessionNotFound, fmt.Sprintf("session '%s' not found", payload.SessionName))
		}
	}

	if err != nil {
		return fmt.Errorf("failed to get/create session: %w", err)
	}

	// Record what the attaching client's host terminal can display so shells
	// started from now on advertise a matching terminal identity (see
	// guestenv.TermProgram). Without this the daemon's own environment decides,
	// and image tools inside a window fall back to block art.
	session.SetGraphicsCapabilities(cs.kittyGraphics, cs.sixelGraphics)

	// Record the new client's dimensions from the attach payload. Previously
	// these were zeroed out on the theory that 80x24 might be a bubbletea
	// placeholder; but leaving them at 0 excludes the client from
	// calculateEffectiveSize until NotifyTerminalSize arrives, which causes
	// web clients to be stuck at stale session dimensions from a previously-
	// attached native client. Trust the attach payload: web/native attach
	// callers already pass the real client viewport.
	// TUI clients are the ones that can receive and execute remote commands.
	// Set under cs.mu, then release before calling helpers that take
	// clientsMu then cs.mu (avoids a re-entrant cs.mu lock).
	// A fresh nonce per attach, so one handed out for an earlier session on
	// this connection does not vouch for mail in the new one.
	//
	// A client that may not act as the person gets none: one inside a pane
	// of this daemon, or one that came over a link its hub did not vouch
	// for. Its reply from human is then stored as a claim, or refused. See
	// human_origin.go.
	humanNonce := ""
	if d.mayActAsHuman(cs) {
		var err error
		if humanNonce, err = newHumanNonce(); err != nil {
			return fmt.Errorf("failed to issue the attach nonce: %w", err)
		}
	}
	cs.mu.Lock()
	previousSession := cs.sessionID
	cs.sessionID = session.ID
	cs.width = payload.Width
	cs.height = payload.Height
	cs.reserve = payload.Reserve
	cs.isTUIClient = true
	cs.humanNonce = humanNonce
	cs.missedStateSync = false
	cs.mu.Unlock()

	// Before the reply, so a pane that probes the moment this client can see
	// it is already answered for this client's machine.
	d.refreshLinkedViewer(session.ID)

	clientCount := d.getSessionClientCount(session.ID)
	log.Printf("Client %s attached to session %s (TUI client, %d clients total, size=%dx%d)",
		cs.clientID, session.Name, clientCount, payload.Width, payload.Height)

	// Tell the clients already here that somebody joined.
	if clientCount > 1 {
		d.notifyClientJoined(session.ID, cs)
	}

	// What the session measures, now that this client is one of the things
	// measuring it: the effective size and the chrome reserve, settled and
	// recorded in one place under one lock, and announced to everyone already
	// attached. The joining client is left out of that announcement and told by
	// the reply below instead, because it has no read loop yet and would read an
	// unsolicited message as its own answer.
	//
	// Both of these used to be computed here by hand and recorded either side
	// of the join notification, on the reasoning that the broadcast has to
	// compare against the old recorded value to know there is anything to
	// announce. That is true, and it is why the comparison and the record now
	// live together in one function rather than being spread across a caller
	// another goroutine can interleave with.
	effectiveWidth, effectiveHeight, effectiveReserve := d.recalculateAndBroadcastSize(session.ID, cs.clientID)
	if effectiveWidth == 0 || effectiveHeight == 0 {
		// No known sizes yet: fall back to this client's payload.
		effectiveWidth = payload.Width
		effectiveHeight = payload.Height
		session.Resize(effectiveWidth, effectiveHeight)
	}

	// A client is now looking at the session, so the restored mark has served its
	// purpose. Cleared before the snapshot below so the attaching client is never
	// handed a mark that the next state push would take straight back off.
	session.ClearRestored()

	// Get session state to return.
	//
	// The dimensions on it are the session's effective size, always. A client
	// reads them as "the minimum over everyone attached" (that is what
	// RestoreFromState does with them), so anything else here is a lie that the
	// attaching client renders at.
	//
	// It used to be stamped only when the effective size differed from what
	// this client asked for, which left exactly one case wrong and it is the
	// common one: a client attaching at the smallest size in the session
	// matches the effective size, skipped the stamp, and was handed whatever
	// dimensions the last client to sync happened to be. A local client joining
	// a browser session therefore came up rendering at the browser's width.
	//
	// The record of the last state forwarded to peers is dropped first. A sync
	// that repeats that record is not forwarded, on the grounds that every peer
	// already holds it, and this client does not: it holds the snapshot below.
	// Dropped before the snapshot, every state that changes after the snapshot
	// is broadcast, and so either reaches this client or marks it as missed.
	// Whether the session's clients send their trees as ops, with this client
	// counted. Settled before the snapshot, so the reply says what is in force.
	// A client that moved here without a detach no longer counts where it was.
	d.refreshTreeOps(session.ID)
	if previousSession != "" && previousSession != session.ID {
		d.refreshTreeOps(previousSession)
	}
	session.forgetBroadcast()
	state := session.GetState()
	if hook := attachSnapshotTaken.Load(); hook != nil {
		(*hook)()
	}
	state.Width = effectiveWidth
	state.Height = effectiveHeight

	debugLog("[DEBUG] Session state: %d windows, %d PTYs", len(state.Windows), session.PTYCount())
	for i, w := range state.Windows {
		debugLog("[DEBUG]   Window %d: ID=%s, PTYID=%s", i, shortID(w.ID), shortID(w.PTYID))
	}

	// Sync PTY pixel dimensions from client's terminal capabilities
	// This enables graphics tools like kitty icat to query proper pixel sizes
	if cs.cellWidth > 0 && cs.cellHeight > 0 {
		d.syncPTYPixelDimensions(session, cs.cellWidth, cs.cellHeight)
	}

	// The reply, and with it this client's admission to the session's
	// broadcasts. See sendAttachReply for why those are one step.
	if err := d.sendAttachReply(cs, &AttachedPayload{
		SessionName: session.Name,
		SessionID:   session.ID,
		Width:       effectiveWidth,
		Height:      effectiveHeight,
		WindowCount: len(state.Windows),
		State:       state,
		Reserve:     effectiveReserve,
		Generation:  session.LayoutGeneration(),
		HumanNonce:  humanNonce,
	}); err != nil {
		return err
	}
	if hook := attachReplied.Load(); hook != nil {
		(*hook)()
	}

	// The hardest thing that can have happened while the reply was being put
	// together is the session going away underneath it. A client registers on
	// the session at the top of this function and joins the broadcast set with
	// the reply at the bottom, so a delete landing between those two reaches
	// neither: the broadcast skips a client that is still attaching, and the
	// client is left holding a session that no longer exists with nothing ever
	// coming to tell it. Checked here, after the reply, where the answer cannot
	// change again without the broadcast catching it.
	if d.manager.GetSessionByID(session.ID) == nil {
		LogBasic("Session %s was terminated while %s was attaching; telling it directly",
			session.Name, cs.clientID)
		_ = d.sendMessage(cs, MsgSessionEnded, &SessionEndedPayload{
			SessionName: session.Name,
			Reason:      "the session was terminated",
		})
		return nil
	}

	// Anything else that moved while the reply was being put together did not
	// reach this client, because it was not in the broadcast set for that part
	// of it. Compare what the reply promised against what the session holds
	// now, and repair it directly. This runs after the reply, so it cannot race
	// it, which is the whole reason the repair is here rather than left to a
	// broadcast.
	//
	// The state is the same case. A state push from another client that lands
	// between the snapshot above and the reply is applied, and its broadcast
	// skips this client because it is not in the broadcast set yet. The reply
	// then carries the older state and nothing ever sends the newer one: the
	// broadcast is not repeated for a state already sent. A client joining a
	// session whose first client had just pushed its pane geometry kept its own
	// geometry for good, and the two ran the same PTYs at different sizes.
	//
	// The test is whether a state broadcast skipped this client, not whether
	// the state moved. A change made after the reply reached this client by
	// its own broadcast, and sending it again gave the client the same state
	// twice. See connState.missedStateSync. The repair is queued behind the
	// broadcasts already on their way to this client, so it cannot overtake
	// them either.
	cs.mu.Lock()
	missed := cs.missedStateSync
	cs.missedStateSync = false
	cs.mu.Unlock()
	if missed {
		LogBasic("Session %s state moved while %s was attaching; telling it directly",
			session.Name, cs.clientID)
		if msg, err := NewMessage(MsgStateSync, &StateSyncPayload{
			State:       session.GetState(),
			TriggerType: "update",
		}); err == nil {
			d.queueBroadcast(cs, msg, "attach state repair")
		}
	}
	if w, h := session.Size(); w > 0 && h > 0 {
		if r := session.LayoutReserve(); w != effectiveWidth || h != effectiveHeight || r != effectiveReserve {
			LogBasic("Session %s moved to %dx%d chrome %+v while %s was attaching; telling it directly",
				session.Name, w, h, r, cs.clientID)
			_ = d.sendMessage(cs, MsgSessionResize, &SessionResizePayload{
				Width:       w,
				Height:      h,
				ClientCount: d.getSessionClientCount(session.ID),
				Reserve:     r,
				Generation:  session.LayoutGeneration(),
			})
		}
	}
	return nil
}

func (d *Daemon) handleDetach(cs *connState) error {
	clientID := cs.clientID

	// Snapshot the subscriptions and session, then clear the fields, all under
	// cs.mu. Unsubscribe and notify after releasing the lock.
	cs.mu.Lock()
	sessionID := cs.sessionID
	if sessionID == "" {
		cs.mu.Unlock()
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}
	subs := make([]string, 0, len(cs.ptySubscriptions))
	for ptyID := range cs.ptySubscriptions {
		subs = append(subs, ptyID)
	}
	cs.ptySubscriptions = make(map[string]struct{})
	cs.sessionID = ""
	cs.width = 0
	cs.height = 0
	cs.reserve = LayoutReserve{}
	cs.attached = false
	cs.mu.Unlock()

	// Unsubscribe from all PTYs and forget where each stream got to. A resume
	// position is a claim that the client still holds the pane it drew, and a
	// detach is the client letting the whole session's panes go: the one path
	// that detaches and attaches again on this connection is a session switch,
	// which closes every window and builds the next session's panes on new
	// emulators. Keeping the positions there told the daemon those empty
	// emulators were already caught up, so a pane came back with the screen its
	// snapshot restored and none of the history behind it.
	if session := d.manager.GetSessionByID(sessionID); session != nil {
		for _, ptyID := range subs {
			if pty := session.GetPTY(ptyID); pty != nil {
				pty.Unsubscribe(clientID)
			}
		}
	}
	cs.mu.Lock()
	// Cleared whole rather than per subscription: the loop above has already
	// released every subscription this connection held, and a detach is the
	// client letting all of them go, so nothing may be left claiming a position.
	cs.ptyResume = make(map[string]int64)
	cs.mu.Unlock()
	d.forgetPushes(cs, sessionID)

	// Notify other clients that this client left
	d.notifyClientLeft(sessionID, clientID)

	return d.sendMessage(cs, MsgDetached, nil)
}

func (d *Daemon) handleNew(cs *connState, msg *Message) error {
	var payload NewPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return fmt.Errorf("invalid new payload: %w", err)
	}

	cfg := &SessionConfig{Global: payload.Global}
	if cs.hello != nil {
		cfg.Term = cs.hello.Term
		cfg.ColorTerm = cs.hello.ColorTerm
		cfg.Shell = cs.hello.Shell
	}

	name := payload.SessionName
	if name == "" {
		name = d.manager.GenerateSessionName()
	}

	sess, err := d.manager.CreateSession(name, cfg, payload.Width, payload.Height)
	if err != nil {
		if err.Error() == fmt.Sprintf("session '%s' already exists", name) {
			return d.sendError(cs, ErrCodeSessionExists, err.Error())
		}
		return fmt.Errorf("failed to create session: %w", err)
	}

	// A detached session has no client to create its first window, so spawn one
	// daemon-side. This makes the session immediately usable by control verbs
	// and gives a later 'tuios attach' a window to restore. Non-detach creation
	// keeps its historical behavior of an empty session the TUI populates.
	if payload.Detach && !payload.Global {
		sessionID := sess.ID
		onExit := func(ptyID string) { d.notifyPTYClosed(sessionID, ptyID) }
		if _, err := sess.AddDaemonWindow("", onExit); err != nil {
			return d.sendError(cs, ErrCodeInternal, fmt.Sprintf("failed to create initial window: %v", err))
		}
		log.Printf("Created detached session %q with an initial window", name)
	}

	return d.handleList(cs)
}

func (d *Daemon) handleList(cs *connState) error {
	sessions := d.listSessions()
	return d.sendMessage(cs, MsgSessionList, &SessionListPayload{
		Sessions: sessions,
	})
}

func (d *Daemon) handleKill(cs *connState, msg *Message) error {
	var payload KillPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return fmt.Errorf("invalid kill payload: %w", err)
	}

	if err := d.manager.DeleteSession(payload.SessionName); err != nil {
		return d.sendError(cs, ErrCodeSessionNotFound, err.Error())
	}

	return d.handleList(cs)
}

func (d *Daemon) handleResurrect(cs *connState, msg *Message) error {
	var payload ResurrectPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return fmt.Errorf("invalid resurrect payload: %w", err)
	}

	if payload.SessionName == "" {
		return d.sendError(cs, ErrCodeInvalidMessage, "session name required")
	}

	// Already live (e.g. auto-restored on start): nothing to do, report success.
	if d.manager.GetSession(payload.SessionName) != nil {
		return d.handleList(cs)
	}

	state, err := LoadResurrectionState(payload.SessionName)
	if err != nil {
		return d.sendError(cs, ErrCodeSessionNotFound, err.Error())
	}

	if _, err := d.restoreSession(state); err != nil {
		return d.sendError(cs, ErrCodeInternal, fmt.Sprintf("failed to restore session: %v", err))
	}

	log.Printf("Resurrected session %q on demand (%d windows)", payload.SessionName, len(state.Windows))
	return d.handleList(cs)
}

func (d *Daemon) handleInput(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return nil
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return nil
	}

	// MsgInput is always the binary format (36-byte PTY ID + data); the gob
	// InputPayload fallback misparsed any gob payload of 36 bytes or more as
	// a PTY id, and no current client sends one.
	ptyID, data, err := ParseBinaryPTYMessage(msg.Payload)
	if err != nil {
		debugLog("[DEBUG] handleInput: failed to parse payload: %v", err)
		return nil
	}

	if ptyID != "" {
		if pty := session.GetPTY(ptyID); pty != nil {
			debugLog("[DEBUG] Writing %d bytes to PTY %s", len(data), shortID(ptyID))
			_, _ = pty.Write(data)
			// Someone is typing in this session, which is the plainest thing
			// "last active" can mean. It used to be recorded only as a side
			// effect of the state sync a client sent after every keypress, so
			// it was right by accident and would have gone stale the moment
			// those redundant syncs stopped being sent.
			session.TouchActive()
		} else {
			debugLog("[DEBUG] PTY %s not found for input", shortID(ptyID))
		}
	}

	return nil
}

func (d *Daemon) handleResize(cs *connState, msg *Message) error {
	var payload ResizePTYPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return fmt.Errorf("invalid resize payload: %w", err)
	}

	if cs.sessionID == "" {
		return nil
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return nil
	}

	// Update client dimensions for multi-client size calculation
	if payload.PTYID == "" {
		// This is a client resize, not a PTY-specific resize.
		// width/height are guarded by cs.mu (read under it in calculateEffectiveSize).
		cs.mu.Lock()
		cs.width = payload.Width
		cs.height = payload.Height
		cs.reserve = payload.Reserve
		cs.mu.Unlock()
		// Recalculate effective session size
		d.recalculateAndBroadcastSize(cs.sessionID, "")
	} else {
		// PTY-specific resize
		if pty := session.GetPTY(payload.PTYID); pty != nil {
			_ = pty.Resize(payload.Width, payload.Height)
			_ = pty.UpdatePixelDimensions(cs.cellWidth, cs.cellHeight)
		}
	}

	return nil
}

func (d *Daemon) handleCreatePTY(cs *connState, msg *Message) error {
	debugLog("[DEBUG] handleCreatePTY called for client %s", cs.clientID)

	if cs.sessionID == "" {
		debugLog("[DEBUG] handleCreatePTY: client not attached")
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		debugLog("[DEBUG] handleCreatePTY: session not found")
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	var payload CreatePTYPayload
	if err := msg.ParsePayload(&payload); err != nil {
		debugLog("[DEBUG] handleCreatePTY: invalid payload: %v", err)
		return fmt.Errorf("invalid create PTY payload: %w", err)
	}

	width := payload.Width
	height := payload.Height
	if width == 0 {
		width = 80
	}
	if height == 0 {
		height = 24
	}

	// Exit callback to notify subscribed clients when the PTY process exits.
	// Passed into CreatePTY so it is set before the monitor goroutine starts,
	// avoiding a data race and a lost notification for shells that exit at once.
	sessionID := cs.sessionID
	onExit := func(ptyID string) {
		d.notifyPTYClosed(sessionID, ptyID)
	}

	debugLog("[DEBUG] Creating PTY %dx%d for session %s", width, height, session.Name)
	pty, err := session.CreatePTY(payload.WindowID, width, height, onExit)
	if err != nil {
		debugLog("[DEBUG] handleCreatePTY: failed to create PTY: %v", err)
		return d.sendError(cs, ErrCodeInternal, fmt.Sprintf("failed to create PTY: %v", err))
	}

	// Set pixel dimensions from client's terminal capabilities
	if err := pty.UpdatePixelDimensions(cs.cellWidth, cs.cellHeight); err != nil {
		debugLog("[DEBUG] handleCreatePTY: failed to set pixel size: %v", err)
	}

	debugLog("[DEBUG] PTY created: %s", pty.ID)
	return d.sendMessage(cs, MsgPTYCreated, &PTYCreatedPayload{
		ID:    pty.ID,
		Title: payload.Title,
	})
}

func (d *Daemon) handleClosePTY(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	var payload ClosePTYPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return fmt.Errorf("invalid close PTY payload: %w", err)
	}

	// Unsubscribe first
	cs.mu.Lock()
	delete(cs.ptySubscriptions, payload.PTYID)
	cs.mu.Unlock()

	if err := session.ClosePTY(payload.PTYID); err != nil {
		return d.sendError(cs, ErrCodePTYNotFound, err.Error())
	}

	return d.sendMessage(cs, MsgPTYClosed, &ClosePTYPayload{PTYID: payload.PTYID})
}

// forgetPushes drops a leaving connection's entry from its session's push
// table. See SessionState.PushSeen.
func (d *Daemon) forgetPushes(cs *connState, sessionID string) {
	cs.mu.Lock()
	origin := cs.pushOrigin
	cs.pushOrigin = ""
	cs.mu.Unlock()
	if session := d.manager.GetSessionByID(sessionID); session != nil {
		session.ForgetPush(origin)
	}
	// A client that leaves may have been the one holding the ops off.
	d.refreshTreeOps(sessionID)
}

// refreshTreeOps turns the session's tree ops on while every TUI client
// attached to it sends them, and off while any does not. A client too old for
// ops sends its trees in its pushes and reads trees only from those, so a
// current client beside it has to do the same, or the two keep two trees.
// A client still attaching is counted: it is about to be handed a snapshot
// that has to say what is in force.
func (d *Daemon) refreshTreeOps(sessionID string) {
	session := d.manager.GetSessionByID(sessionID)
	if session == nil {
		return
	}
	// Held from the count to the change. A detach that counted "no older
	// client" and then applied it after an older client's attach had applied
	// "off" left the ops on beside a client that cannot send them.
	session.treeOpsMu.Lock()
	defer session.treeOpsMu.Unlock()
	on := true
	d.clientsMu.RLock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		if cs.sessionID == sessionID && cs.isTUIClient && !cs.treeOps {
			on = false
		}
		cs.mu.Unlock()
	}
	d.clientsMu.RUnlock()
	if hook := treeOpsCounted.Load(); hook != nil {
		(*hook)()
	}
	session.SetLayoutTreeOps(on)
}

// treeOpsCounted runs in refreshTreeOps between the count and the change. It
// is unset outside tests, which use it to widen that window on purpose.
var treeOpsCounted atomic.Pointer[func()]

// notePushOrigin records the name cs gives its pushes and layout ops. The
// count itself is recorded by the session with the change it makes (see
// notePushLocked), or by NotePush for a push that is refused. See
// SessionState.PushSeen.
func (d *Daemon) notePushOrigin(cs *connState, session *Session, origin string) {
	if origin == "" || len(origin) > maxPushOriginLen {
		return
	}
	cs.mu.Lock()
	prev := cs.pushOrigin
	cs.pushOrigin = origin
	cs.mu.Unlock()
	// One entry per connection: a client names its pushes afresh only on
	// attach, and a connection that kept changing the name must not grow
	// the table every state carries.
	if prev != "" && prev != origin {
		session.ForgetPush(prev)
	}
}

func (d *Daemon) handleUpdateState(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	var state SessionState
	if err := msg.ParsePayload(&state); err != nil {
		return fmt.Errorf("invalid state payload: %w", err)
	}
	// Counted before anything can refuse or rewrite the push: the client
	// counts every push it sends, and PushSeen has to agree with it. See
	// SessionState.PushSeen. A name longer than any client makes is not one,
	// and is not let into a table every state carries.
	d.notePushOrigin(cs, session, state.PushOrigin)
	// Before anything that walks the layout trees by recursion (the merge,
	// the fingerprint, the save, the rebroadcast) sees them. See
	// wire_bounds.go.
	if err := validateSessionState(&state); err != nil {
		LogError("Refused a state update from %s: %v", cs.clientID, err)
		// Counted all the same: the client counts every push it sends.
		session.NotePush(state.PushOrigin, state.PushSeq)
		return d.sendError(cs, ErrCodeInvalidMessage, "state update refused: "+err.Error())
	}
	clampPushedText(&state)

	// A client running inside a pane is an agent's view, not the person's,
	// so its focus does not mark a finished turn seen. See human_origin.go.
	// Read before the merge, which may keep another state's fields: the pair
	// is this client's own, and it is what the panes' emulators answer OSC 11
	// and OSC 10 with. See report_colors.go.
	reportBg, reportFg, reportPal := state.PaneReportBg, state.PaneReportFg, state.PaneReportPalette
	accepted, behind := session.updateStateFrom(&state, d.mayActAsHuman(cs))
	session.applyReportColors(reportBg, reportFg, reportPal)

	// The merged state is a full copy of the session's, retitled from every
	// live emulator. It is read only by the reconcile reply and the peer
	// broadcast below, so a sync from the one attached client that the daemon
	// accepted as it stands, which is nearly every sync, does not build it.
	var merged *SessionState
	mergedState := func() *SessionState {
		if merged == nil {
			merged = session.GetState()
		}
		return merged
	}

	// A sync built before a daemon-side mutation was reconciled against it, so
	// what is canonical now is not what this client pushed. Send the merged state
	// straight back: without it the client keeps rendering its stale view and
	// pushes it again on the next sync.
	//
	// A push that was accepted but built before a tree op it had not seen is
	// answered the same way. The op's own broadcast reached this client before
	// the push landed, so the client dropped it as older than the push, and
	// nothing else would ever tell it about that tree.
	if !accepted || behind {
		if err := d.sendMessage(cs, MsgStateSync, &StateSyncPayload{
			State:       mergedState(),
			TriggerType: "reconcile",
		}); err != nil {
			return err
		}
	}

	// Broadcast state change to other clients in the session. Peers get the
	// merged state, not the raw push, so every client converges on the same view.
	//
	// A merge that landed on the state already broadcast is not sent again. The
	// push side is unconditional by design (a client syncs after every
	// keystroke and every click so nothing it does can be lost), so almost all
	// of them say what the last one said, and each one costs every peer a full
	// state application and a redraw. A peer already holds this state: it was
	// either sent it, or handed it in its attach reply, which is this same
	// snapshot. An attach drops the record, so a state from before a client
	// joined is never taken as one it holds. Nothing else rides on the
	// message, so there is nothing for a suppressed one to have delivered.
	//
	// The reconcile reply above is deliberately outside this: it goes to the
	// sender, whose state is by definition not the merged one.
	clientCount := d.getSessionClientCount(cs.sessionID)
	if clientCount > 1 {
		fp := StateFingerprint(mergedState())
		if session.NoteBroadcastFingerprint(fp) {
			d.broadcastStateSync(cs.sessionID, mergedState(), "update", cs.clientID)
		}
	}

	return nil
}

func (d *Daemon) handleSubscribePTY(cs *connState, msg *Message) error {
	debugLog("[DEBUG] handleSubscribePTY called for client %s", cs.clientID)

	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	var payload SubscribePTYPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return fmt.Errorf("invalid subscribe PTY payload: %w", err)
	}

	debugLog("[DEBUG] Subscribing to PTY %s", payload.PTYID)
	pty := session.GetPTY(payload.PTYID)
	if pty == nil {
		debugLog("[DEBUG] PTY %s not found", payload.PTYID)
		return d.sendError(cs, ErrCodePTYNotFound, fmt.Sprintf("PTY %s not found", payload.PTYID))
	}

	cs.mu.Lock()
	if _, already := cs.ptySubscriptions[payload.PTYID]; already {
		cs.mu.Unlock()
		// Already streaming; a second streamPTYOutput would compete for the
		// same output channel and interleave halves of the output.
		debugLog("[DEBUG] PTY %s already subscribed for client %s", payload.PTYID, cs.clientID)
		return nil
	}
	cs.ptySubscriptions[payload.PTYID] = struct{}{}
	// A client that restored a snapshot names the position that snapshot ends
	// at, and that beats anything recorded here: the recorded position is where
	// this connection's stream last got to, which is older than the snapshot and
	// would replay output the snapshot already shows.
	resume := cs.ptyResume[payload.PTYID]
	if payload.FromSeq > 0 {
		resume = payload.FromSeq
	}
	cs.mu.Unlock()

	debugLog("[DEBUG] Starting PTY output stream for %s", payload.PTYID)
	// Registered here rather than inside the goroutine, so that anything this
	// connection asks for next is answered to a subscriber that already exists.
	// A resize sent straight after a subscribe used to be broadcast to nobody,
	// and the pane it belonged to kept the size it had before, on a client that
	// was by then waiting to be told.
	// A client that restored a snapshot before subscribing holds an
	// authoritative copy of the pane's state at resume, so a rolled catch-up
	// must replay the tail on top of it rather than clear it (issue #123).
	if payload.FromSnapshot {
		outputCh := pty.SubscribeFromSnapshot(cs.clientID, resume)
		go d.streamPTYOutput(cs, pty, outputCh)
		return nil
	}
	outputCh := pty.Subscribe(cs.clientID, resume)
	go d.streamPTYOutput(cs, pty, outputCh)

	return nil
}

func (d *Daemon) handleUnsubscribePTY(cs *connState, msg *Message) error {
	debugLog("[DEBUG] handleUnsubscribePTY called for client %s", cs.clientID)

	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	var payload UnsubscribePTYPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return fmt.Errorf("invalid unsubscribe PTY payload: %w", err)
	}

	debugLog("[DEBUG] Unsubscribing from PTY %s", payload.PTYID)

	// Remove from subscriptions
	cs.mu.Lock()
	delete(cs.ptySubscriptions, payload.PTYID)
	cs.mu.Unlock()

	// Unsubscribe from the PTY output channel, keeping where the stream had got
	// to: this is a pane going out of view, not a client leaving, and it will be
	// back the moment its workspace is current again.
	pty := session.GetPTY(payload.PTYID)
	if pty != nil {
		resume := pty.Unsubscribe(cs.clientID)
		cs.mu.Lock()
		cs.ptyResume[payload.PTYID] = resume
		cs.mu.Unlock()
		debugLog("[DEBUG] Successfully unsubscribed client %s from PTY %s at %d", cs.clientID, shortID(payload.PTYID), resume)
	}

	return nil
}

func (d *Daemon) handleGetTerminalState(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	var payload GetTerminalStatePayload
	if err := msg.ParsePayload(&payload); err != nil {
		return fmt.Errorf("invalid get terminal state payload: %w", err)
	}

	pty := session.GetPTY(payload.PTYID)
	if pty == nil {
		return d.sendError(cs, ErrCodePTYNotFound, fmt.Sprintf("PTY %s not found", payload.PTYID))
	}

	// Both request fields were parsed and then ignored, so every state request
	// carried a thousand scrollback rows whether or not the caller wanted any.
	maxScrollback := -1
	if payload.IncludeScrollback {
		maxScrollback = payload.MaxScrollbackLines
	}
	var state *TerminalState
	if payload.Packed {
		state = pty.GetTerminalStatePacked(maxScrollback, payload.HaveScrollback)
	} else {
		state = pty.GetTerminalState(maxScrollback, payload.HaveScrollback)
	}
	return d.sendMessage(cs, MsgTerminalState, &TerminalStatePayload{
		PTYID: payload.PTYID,
		State: state,
	})
}
