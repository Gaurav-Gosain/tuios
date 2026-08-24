package session

import (
	"fmt"
	"log"
)

func (d *Daemon) handleHello(cs *connState, msg *Message) error {
	var payload HelloPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid hello payload: %w", err)
	}

	cs.hello = &payload

	// Refuse a client this daemon cannot serve before it can attach to anything.
	// The codec is negotiated below, so the refusal goes out on the codec the
	// client opened with, which is the one it can read.
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

	// gob is the only payload codec; PreferredCodec stays in the handshake
	// for wire stability but cannot select anything else.
	cs.codec = DefaultCodec()

	sessions := d.manager.ListSessions()
	names := make([]string, len(sessions))
	for i, s := range sessions {
		names[i] = s.Name
	}

	return d.sendMessage(cs, MsgWelcome, &WelcomePayload{
		Version:      d.version,
		SessionNames: names,
		Codec:        cs.codec.Type().String(),
		Protocol:     ProtocolVersion,
	})
}

func (d *Daemon) handleAttach(cs *connState, msg *Message) error {
	var payload AttachPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
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
	// attached native client. Trust the attach payload  - web/native attach
	// callers already pass the real client viewport.
	// TUI clients are the ones that can receive and execute remote commands.
	// Set under cs.mu, then release before calling helpers that take
	// clientsMu then cs.mu (avoids a re-entrant cs.mu lock).
	cs.mu.Lock()
	cs.sessionID = session.ID
	cs.width = payload.Width
	cs.height = payload.Height
	cs.reserve = payload.Reserve
	cs.isTUIClient = true
	cs.mu.Unlock()

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
	// the reply below instead - it has no read loop yet and would read an
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
		// No known sizes yet - fall back to this client's payload.
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
	// reads them as "the minimum over everyone attached" - that is what
	// RestoreFromState does with them - so anything else here is a lie that the
	// attaching client renders at.
	//
	// It used to be stamped only when the effective size differed from what
	// this client asked for, which left exactly one case wrong and it is the
	// common one: a client attaching at the smallest size in the session
	// matches the effective size, skipped the stamp, and was handed whatever
	// dimensions the last client to sync happened to be. A local client joining
	// a browser session therefore came up rendering at the browser's width.
	state := session.GetState()
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
	}); err != nil {
		return err
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
	// of it.
	// Compare what the reply promised against what the session holds now, and
	// repair it directly. This runs after the reply, so it cannot race it -
	// which is the whole reason the repair is here rather than left to a
	// broadcast.
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

	// Notify other clients that this client left
	d.notifyClientLeft(sessionID, clientID)

	return d.sendMessage(cs, MsgDetached, nil)
}

func (d *Daemon) handleNew(cs *connState, msg *Message) error {
	var payload NewPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid new payload: %w", err)
	}

	cfg := &SessionConfig{}
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
	if payload.Detach {
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
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return fmt.Errorf("invalid kill payload: %w", err)
	}

	if err := d.manager.DeleteSession(payload.SessionName); err != nil {
		return d.sendError(cs, ErrCodeSessionNotFound, err.Error())
	}

	return d.handleList(cs)
}

func (d *Daemon) handleResurrect(cs *connState, msg *Message) error {
	var payload ResurrectPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
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
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
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
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
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
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
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

func (d *Daemon) handleUpdateState(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	var state SessionState
	if err := msg.ParsePayloadWithCodec(&state, cs.codec); err != nil {
		return fmt.Errorf("invalid state payload: %w", err)
	}

	accepted := session.UpdateState(&state)
	merged := session.GetState()

	// A sync built before a daemon-side mutation was reconciled against it, so
	// what is canonical now is not what this client pushed. Send the merged state
	// straight back: without it the client keeps rendering its stale view and
	// pushes it again on the next sync.
	if !accepted {
		if err := d.sendMessage(cs, MsgStateSync, &StateSyncPayload{
			State:       merged,
			TriggerType: "reconcile",
		}); err != nil {
			return err
		}
	}

	// Broadcast state change to other clients in the session. Peers get the
	// merged state, not the raw push, so every client converges on the same view.
	//
	// A merge that landed on the state already broadcast is not sent again. The
	// push side is unconditional by design - a client syncs after every
	// keystroke and every click so nothing it does can be lost - so almost all
	// of them say what the last one said, and each one costs every peer a full
	// state application and a redraw. A peer already holds this state: it was
	// either sent it, or handed it in its attach reply, which is this same
	// snapshot. Nothing else rides on the message, so there is nothing for a
	// suppressed one to have delivered.
	//
	// The reconcile reply above is deliberately outside this: it goes to the
	// sender, whose state is by definition not the merged one.
	clientCount := d.getSessionClientCount(cs.sessionID)
	if clientCount > 1 {
		fp := StateFingerprint(merged)
		if session.NoteBroadcastFingerprint(fp) {
			d.broadcastStateSync(cs.sessionID, merged, "update", cs.clientID)
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
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
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
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
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
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
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
	state := pty.GetTerminalState(maxScrollback, payload.HaveScrollback)
	return d.sendMessage(cs, MsgTerminalState, &TerminalStatePayload{
		PTYID: payload.PTYID,
		State: state,
	})
}
