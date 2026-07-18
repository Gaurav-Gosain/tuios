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

	// Negotiate codec based on client preference
	cs.codec = NegotiateCodec(payload.PreferredCodec)
	LogBasic("Client %s negotiated codec: %s", cs.clientID, cs.codec.Type())

	sessions := d.manager.ListSessions()
	names := make([]string, len(sessions))
	for i, s := range sessions {
		names[i] = s.Name
	}

	return d.sendMessage(cs, MsgWelcome, &WelcomePayload{
		Version:      d.version,
		SessionNames: names,
		Codec:        cs.codec.Type().String(),
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
	cs.isTUIClient = true
	cs.mu.Unlock()

	clientCount := d.getSessionClientCount(session.ID)
	log.Printf("Client %s attached to session %s (TUI client, %d clients total, size=%dx%d)",
		cs.clientID, session.Name, clientCount, payload.Width, payload.Height)

	// Calculate effective size including the new client's dimensions.
	effectiveWidth, effectiveHeight := d.calculateEffectiveSize(session.ID)
	if effectiveWidth == 0 || effectiveHeight == 0 {
		// No known sizes yet  - fall back to this client's payload.
		effectiveWidth = payload.Width
		effectiveHeight = payload.Height
	}

	// Update session size if needed
	session.Resize(effectiveWidth, effectiveHeight)

	// Notify other clients that a new client joined (this also broadcasts size change)
	if clientCount > 1 {
		d.notifyClientJoined(session.ID, cs)
	}

	// Get session state to return
	state := session.GetState()
	// Only update state dimensions if we have real client sizes
	// When reattaching after all clients disconnect, preserve the original state dimensions
	// so that window scaling works correctly. The placeholder 80x24 values would cause
	// windows to be scaled incorrectly when the real terminal size is known.
	if effectiveWidth != payload.Width || effectiveHeight != payload.Height {
		// We have real dimensions from other clients, use them
		state.Width = effectiveWidth
		state.Height = effectiveHeight
	}
	// If state dimensions are 0 (new session), use effective/placeholder values
	if state.Width == 0 || state.Height == 0 {
		state.Width = effectiveWidth
		state.Height = effectiveHeight
	}

	debugLog("[DEBUG] Session state: %d windows, %d PTYs", len(state.Windows), session.PTYCount())
	for i, w := range state.Windows {
		debugLog("[DEBUG]   Window %d: ID=%s, PTYID=%s", i, shortID(w.ID), shortID(w.PTYID))
	}

	// Sync PTY pixel dimensions from client's terminal capabilities
	// This enables graphics tools like kitty icat to query proper pixel sizes
	if cs.cellWidth > 0 && cs.cellHeight > 0 {
		d.syncPTYPixelDimensions(session, cs.cellWidth, cs.cellHeight)
	}

	return d.sendMessage(cs, MsgAttached, &AttachedPayload{
		SessionName: session.Name,
		SessionID:   session.ID,
		Width:       effectiveWidth,
		Height:      effectiveHeight,
		WindowCount: len(state.Windows),
		State:       state,
	})
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
	cs.mu.Unlock()

	// Unsubscribe from all PTYs
	if session := d.manager.GetSessionByID(sessionID); session != nil {
		for _, ptyID := range subs {
			if pty := session.GetPTY(ptyID); pty != nil {
				pty.Unsubscribe(clientID)
			}
		}
	}

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

	_, err := d.manager.CreateSession(name, cfg, payload.Width, payload.Height)
	if err != nil {
		if err.Error() == fmt.Sprintf("session '%s' already exists", name) {
			return d.sendError(cs, ErrCodeSessionExists, err.Error())
		}
		return fmt.Errorf("failed to create session: %w", err)
	}

	return d.handleList(cs)
}

func (d *Daemon) handleList(cs *connState) error {
	sessions := d.manager.ListSessions()
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

	// Try binary format first (36-byte PTY ID + data)
	ptyID, data, err := ParseBinaryPTYMessage(msg.Payload)
	if err != nil {
		// Fall back to codec format
		var payload InputPayload
		if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
			debugLog("[DEBUG] handleInput: failed to parse payload: %v", err)
			return nil
		}
		ptyID = payload.PTYID
		data = payload.Data
	}

	if ptyID != "" {
		if pty := session.GetPTY(ptyID); pty != nil {
			debugLog("[DEBUG] Writing %d bytes to PTY %s", len(data), shortID(ptyID))
			_, _ = pty.Write(data)
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
		cs.mu.Unlock()
		// Recalculate effective session size
		d.recalculateAndBroadcastSize(cs.sessionID)
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

func (d *Daemon) handleListPTYs(cs *connState) error {
	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	ptyIDs := session.ListPTYIDs()
	ptys := make([]PTYInfo, 0, len(ptyIDs))

	for _, id := range ptyIDs {
		pty := session.GetPTY(id)
		if pty != nil {
			ptys = append(ptys, PTYInfo{
				ID:     pty.ID,
				Exited: pty.IsExited(),
			})
		}
	}

	return d.sendMessage(cs, MsgPTYList, &PTYListPayload{PTYs: ptys})
}

func (d *Daemon) handleGetState(cs *connState) error {
	if cs.sessionID == "" {
		return d.sendError(cs, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.sendError(cs, ErrCodeSessionNotFound, "session not found")
	}

	state := session.GetState()
	return d.sendMessage(cs, MsgStateData, state)
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

	session.UpdateState(&state)

	// Broadcast state change to other clients in the session
	clientCount := d.getSessionClientCount(cs.sessionID)
	if clientCount > 1 {
		d.broadcastStateSync(cs.sessionID, &state, "update", cs.clientID)
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
	cs.mu.Unlock()

	debugLog("[DEBUG] Starting PTY output stream for %s", payload.PTYID)
	go d.streamPTYOutput(cs, pty)

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

	// Unsubscribe from the PTY output channel
	pty := session.GetPTY(payload.PTYID)
	if pty != nil {
		pty.Unsubscribe(cs.clientID)
		debugLog("[DEBUG] Successfully unsubscribed client %s from PTY %s", cs.clientID, shortID(payload.PTYID))
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

	state := pty.GetTerminalState()
	return d.sendMessage(cs, MsgTerminalState, &TerminalStatePayload{
		PTYID: payload.PTYID,
		State: state,
	})
}
