package session

import (
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

func (d *Daemon) handleHello(cs *connState, msg *Message) error {
	var payload HelloPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return errfmt.Errorf("invalid hello payload: %w", err)
	}

	payload.Term, payload.ColorTerm = helloTermValue(payload.Term), helloTermValue(payload.ColorTerm)
	cs.hello = &payload
	cs.mu.Lock()
	cs.listingPush = payload.ListingPush
	cs.paneMarks = payload.PaneMarks
	cs.goodbye = payload.Goodbye
	cs.tmlSurfaces = payload.TMLSurfaces
	cs.pluginAsks = payload.PluginApprovals
	cs.prompts = payload.Prompts
	cs.holdOptions = payload.HoldOptions
	cs.machine, cs.kind = helloMachine(payload.Machine), helloKind(payload.ClientKind)
	cs.mu.Unlock()

	if protocolMismatch(payload.Protocol) {
		LogBasic("Client %s refused: speaks protocol %d, this daemon serves %d..%d",
			cs.clientID, peerProtocol(payload.Protocol), MinProtocolVersion, ProtocolVersion)
		return d.replyError(cs, msg, ErrCodeInvalidMessage, clientProtocolRefusal(d.version, &payload))
	}

	cs.mu.Lock()
	cs.pixelWidth = payload.PixelWidth
	cs.pixelHeight = payload.PixelHeight
	cs.cellWidth = payload.CellWidth
	cs.cellHeight = payload.CellHeight
	cs.mu.Unlock()
	cs.kittyGraphics = payload.KittyGraphics
	cs.sixelGraphics = payload.SixelGraphics
	cs.kittyAnimation = payload.KittyAnimation
	cs.symbolImages = payload.SymbolImages
	cs.terminalName = payload.TerminalName

	if payload.CellWidth > 0 && payload.CellHeight > 0 {
		LogBasic("Client %s capabilities: cell=%dx%d pixels, kitty=%v, sixel=%v, term=%s",
			cs.clientID, payload.CellWidth, payload.CellHeight, payload.KittyGraphics, payload.SixelGraphics, payload.TerminalName)
	}

	sessions := d.manager.ListSessions()
	names := make([]string, len(sessions))
	for i, s := range sessions {
		names[i] = s.Name
	}

	return d.sendMessage(cs, MsgWelcome, &WelcomePayload{
		Version:               d.version,
		SessionNames:          names,
		Protocol:              ProtocolVersion,
		ClientFocus:           true,
		ClientGraphics:        true,
		LayoutTreeOps:         true,
		TypeAtPrompt:          true,
		KittyAnimationRefusal: true,
		MasterLayoutOps:       true,
		WindowSize:            true,
		DirWatch:              true,
		SidebarOps:            true,
		ThemeEvents:           true,
		RequestIDs:            true,
		EmptyWorkspacePanes:   true,
		SessionUsed:           true,
		DetachOthers:          true,
		ListingPush:           true,
		PaneMarks:             true,
		TMLLink:               !legacyTMLLink(),
	})
}

var attachSnapshotTaken atomic.Pointer[func()]

var attachReplied atomic.Pointer[func()]

func (d *Daemon) handleAttach(cs *connState, msg *Message) error {
	var payload AttachPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return errfmt.Errorf("invalid attach payload: %w", err)
	}

	cfg := &SessionConfig{}
	if cs.hello != nil {
		cfg.Term = cs.hello.Term
		cfg.ColorTerm = cs.hello.ColorTerm
		cfg.Shell = cs.hello.Shell
	}

	var session *Session
	var created bool
	var err error

	inside, insideWhy := d.placeClient(cs, &payload)
	if inside != nil && payload.SessionName == "" && !payload.AllowNested {
		return d.refuseNestedAttach(cs, msg, inside, nil, insideWhy)
	}

	if payload.SessionName == "" {
		session, created, err = d.manager.GetDefaultSession(cfg, payload.Width, payload.Height)
	} else if payload.CreateNew {
		session, created, err = d.manager.GetOrCreateSession(payload.SessionName, cfg, payload.Width, payload.Height)
	} else {
		session = d.manager.GetSession(payload.SessionName)
		if session == nil {
			if renamed, ok := d.manager.ResolveSession(payload.SessionName); ok && renamed != nil {
				return d.replyError(cs, msg, ErrCodeSessionNotFound, RenamedSessionMessage(payload.SessionName, renamed.Name()))
			}
			return d.replyError(cs, msg, ErrCodeSessionNotFound, fmt.Sprintf("session '%s' not found", payload.SessionName))
		}
	}

	if err != nil {
		return errfmt.Errorf("failed to get/create session: %w", err)
	}
	if created && payload.Cwd != "" && checkWindowCwd(payload.Cwd) == nil {
		session.SetStartDir(absStartDir(payload.Cwd))
	}
	if inside != nil && !payload.AllowNested && d.shownInside(inside.ID, session.ID, cs.clientID) {
		return d.refuseNestedAttach(cs, msg, inside, session, insideWhy)
	}

	session.SetGraphicsCapabilities(cs.kittyGraphics, cs.sixelGraphics)
	d.manager.noteHostGraphics(cs.kittyGraphics, cs.sixelGraphics)

	humanNonce := ""
	if d.mayActAsHuman(cs) {
		var err error
		if humanNonce, err = newHumanNonce(); err != nil {
			return errfmt.Errorf("failed to issue the attach nonce: %w", err)
		}
	}
	session.attachMu.Lock()
	attachLocked := true
	defer func() {
		if attachLocked {
			session.attachMu.Unlock()
		}
	}()
	returnInput := d.latestReturnInput(session.ID, cs.clientID)
	cs.mu.Lock()
	previousSession := cs.sessionID
	cs.sessionID = session.ID
	cs.repliedSession = ""
	cs.sessionName = session.Name()
	cs.width = payload.Width
	cs.height = payload.Height
	cs.reserve = payload.Reserve
	cs.isTUIClient = true
	cs.attachSeq = d.attachCount.Add(1)
	cs.viewOnly = payload.ViewOnly
	cs.upgradeDialog = payload.UpgradeDialog
	cs.passive = payload.Passive
	cs.lastActivity = returnInput
	cs.humanNonce = humanNonce
	cs.missedStateSync = false
	cs.mu.Unlock()
	d.events.publish(streamEvent{Type: EventClientSessionChanged, ClientID: cs.clientID, PID: cs.peerPID, Session: session.Name(), Attached: ptr(true)})
	var victims []*connState
	if d.exclusiveAttach(&payload) {
		storeMax(&session.exclusiveSeq, cs.attachSeq)
		victims = d.markOthers(session, cs, cs.attachSeq)
	}
	if previousSession != "" && previousSession != session.ID {
		d.agentForget(previousSession, cs.clientID)
		d.displayForget(previousSession, cs.clientID)
	}
	d.agentNoteUse(cs, session.ID)
	d.displayNoteUse(cs, session.ID)
	d.kickPRPoll()

	d.refreshLinkedViewer(session.ID)

	clientCount := d.getSessionClientCount(session.ID)
	log.Printf("Client %s attached to session %s (TUI client, %d clients total, size=%dx%d)",
		cs.clientID, session.Name(), clientCount, payload.Width, payload.Height)

	if clientCount > 1 {
		d.notifyClientJoined(session.ID, cs)
	}

	effectiveWidth, effectiveHeight, effectiveReserve := d.recalculateAndBroadcastSize(session.ID, cs.clientID)
	if effectiveWidth == 0 || effectiveHeight == 0 {
		effectiveWidth = payload.Width
		effectiveHeight = payload.Height
		session.Resize(effectiveWidth, effectiveHeight)
	}

	session.ClearRestored()

	d.recountTUIClients(session.ID)
	if previousSession != "" && previousSession != session.ID {
		d.recountTUIClients(previousSession)
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

	d.syncSessionCell(session.ID)

	sizedBy := session.SizedBy()
	err = d.sendAttachReply(cs, msg, &AttachedPayload{
		SessionName:   session.Name(),
		SessionID:     session.ID,
		Width:         effectiveWidth,
		Height:        effectiveHeight,
		WindowCount:   len(state.Windows),
		State:         state,
		Reserve:       effectiveReserve,
		Generation:    session.LayoutGeneration(),
		HumanNonce:    humanNonce,
		Rig:           d.rig,
		Policy:        session.WindowSizePolicy(),
		SizedBy:       sizedBy.Machine,
		SizedByClient: sizedBy.Client,
		SizedByKind:   sizedBy.Kind,
		ClientID:      cs.clientID,
		ClientCount:   d.getSessionClientCount(session.ID),
	})
	attachLocked = false
	session.attachMu.Unlock()
	d.noticeDetached(victims, session, DetachedByAttachMessage)
	if err != nil {
		return err
	}
	d.ejectIfSuperseded(cs, session)
	session.wakeStateWaiters()
	if hook := attachReplied.Load(); hook != nil {
		(*hook)()
	}
	d.sendTMLSurfaces(cs, session.ID)
	d.sendPluginAsks(cs)
	d.sendPrompt(cs)
	go d.applyWaitingOptions(session)

	if inside == nil && !cs.viaLink {
		d.watchNestedAfterAttach(cs, session.ID, payload.NestProbe, payload.AllowNested)
	}

	if d.manager.GetSessionByID(session.ID) == nil {
		LogBasic("Session %s was terminated while %s was attaching; telling it directly",
			session.Name(), cs.clientID)
		_ = d.sendMessage(cs, MsgSessionEnded, &SessionEndedPayload{
			SessionName: session.Name(),
			Reason:      "the session was terminated",
		})
		return nil
	}

	cs.mu.Lock()
	missed := cs.missedStateSync
	cs.missedStateSync = false
	cs.mu.Unlock()
	if missed {
		LogBasic("Session %s state moved while %s was attaching; telling it directly",
			session.Name(), cs.clientID)
		session.resendState(func(state *SessionState) {
			if msg, err := NewMessage(MsgStateSync, &StateSyncPayload{
				State:       state,
				TriggerType: "update",
			}); err == nil {
				d.queueBroadcast(cs, msg, "attach state repair")
			}
		})
	}
	if w, h := session.Size(); w > 0 && h > 0 {
		if r := session.LayoutReserve(); w != effectiveWidth || h != effectiveHeight || r != effectiveReserve {
			LogBasic("Session %s moved to %dx%d chrome %+v while %s was attaching; telling it directly",
				session.Name(), w, h, r, cs.clientID)
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

func (d *Daemon) handleDetach(cs *connState, msg *Message) error {
	if !d.detachClient(cs) {
		return d.replyError(cs, msg, ErrCodeNotAttached, "not attached to any session")
	}
	return d.sendMessage(cs, MsgDetached, nil)
}

func (d *Daemon) detachClient(cs *connState) bool {
	return d.detachClientFrom(cs, "")
}

func (d *Daemon) detachClientFrom(cs *connState, want string) bool {
	clientID := cs.clientID

	cs.mu.Lock()
	sessionID := cs.sessionID
	sessionName := cs.sessionName
	input := cs.lastActivity
	if sessionID == "" || (want != "" && (sessionID != want || cs.repliedSession != want)) {
		cs.mu.Unlock()
		return false
	}
	subs := make([]string, 0, len(cs.ptySubscriptions))
	for ptyID := range cs.ptySubscriptions {
		subs = append(subs, ptyID)
	}
	cs.ptySubscriptions = make(map[string]*ptySubscriber)
	cs.sessionID = ""
	cs.sessionName = ""
	cs.repliedSession = ""
	cs.width = 0
	cs.height = 0
	cs.reserve = LayoutReserve{}
	cs.attached = false
	cs.promptShown = ""
	cs.mu.Unlock()
	d.events.publish(streamEvent{
		Type:     EventClientSessionChanged,
		ClientID: cs.clientID,
		PID:      cs.peerPID,
		Session:  sessionName,
		Attached: ptr(false),
	})

	if session := d.manager.GetSessionByID(sessionID); session != nil {
		for _, ptyID := range subs {
			if pty := session.GetPTY(ptyID); pty != nil {
				pty.Unsubscribe(clientID)
			}
		}
	}
	cs.mu.Lock()
	cs.ptyResume = make(map[string]int64)
	cs.mu.Unlock()
	d.forgetPushes(cs, sessionID)

	d.notifyClientLeft(sessionID, clientID, input)
	return true
}

func (d *Daemon) handleNew(cs *connState, msg *Message) error {
	var payload NewPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return errfmt.Errorf("invalid new payload: %w", err)
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
			return d.replyError(cs, msg, ErrCodeSessionExists, err.Error())
		}
		return errfmt.Errorf("failed to create session: %w", err)
	}
	if payload.Cwd != "" && checkWindowCwd(payload.Cwd) == nil {
		sess.SetStartDir(absStartDir(payload.Cwd))
	}

	if payload.Detach && !payload.Global {
		sessionID := sess.ID
		onExit := func(ptyID string) { d.notifyPTYClosed(sessionID, ptyID) }
		if _, err := sess.AddDaemonWindow("", onExit); err != nil {
			return d.replyError(cs, msg, ErrCodeInternal, fmt.Sprintf("failed to create initial window: %v", err))
		}
		log.Printf("Created detached session %q with an initial window", name)
	}

	return d.handleList(cs, msg)
}

func (d *Daemon) handleList(cs *connState, msg *Message) error {
	sessions := d.listSessions()
	return d.reply(cs, msg, MsgSessionList, &SessionListPayload{
		Sessions: sessions,
	})
}

func (d *Daemon) handleKill(cs *connState, msg *Message) error {
	var payload KillPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return errfmt.Errorf("invalid kill payload: %w", err)
	}

	if err := d.manager.DeleteSession(payload.SessionName); err != nil {
		if renamed, ok := d.manager.ResolveSession(payload.SessionName); ok && renamed != nil {
			return d.replyError(cs, msg, ErrCodeSessionNotFound, RenamedSessionMessage(payload.SessionName, renamed.Name()))
		}
		return d.replyError(cs, msg, ErrCodeSessionNotFound, err.Error())
	}

	return d.handleList(cs, msg)
}

func (d *Daemon) handleResurrect(cs *connState, msg *Message) error {
	var payload ResurrectPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return errfmt.Errorf("invalid resurrect payload: %w", err)
	}

	if payload.SessionName == "" {
		return d.replyError(cs, msg, ErrCodeInvalidMessage, "session name required")
	}
	if err := ValidateSessionName(payload.SessionName); err != nil {
		return d.replyError(cs, msg, ErrCodeInvalidMessage, err.Error())
	}

	if d.manager.GetSession(payload.SessionName) != nil {
		return d.handleList(cs, msg)
	}

	state, err := LoadResurrectionState(payload.SessionName)
	if err != nil {
		return d.replyError(cs, msg, ErrCodeSessionNotFound, err.Error())
	}

	if _, err := d.restoreSession(state); err != nil {
		return d.replyError(cs, msg, ErrCodeInternal, fmt.Sprintf("failed to restore session: %v", err))
	}

	log.Printf("Resurrected session %q on demand (%d windows)", payload.SessionName, len(state.Windows))
	return d.handleList(cs, msg)
}

func (d *Daemon) handleInput(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return nil
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return nil
	}

	ptyID, data, err := ParseBinaryPTYMessage(msg.Payload)
	if err != nil {
		debugLog("[DEBUG] handleInput: failed to parse payload: %v", err)
		return nil
	}

	if ptyID != "" {
		if why := d.refuseTypingInto(cs, session, ptyID); why != "" {
			_ = d.replyError(cs, msg, ErrCodeForbidden, "input is refused for this pane: "+why)
			return nil
		}
		if pty := session.GetPTY(ptyID); pty != nil {
			debugLog("[DEBUG] Writing %d bytes to PTY %s", len(data), shortID(ptyID))
			if _, err := pty.Write(data); errors.Is(err, errPaneInputBusy) {
				_ = d.replyError(cs, msg, ErrCodeBusy, "refused: "+err.Error()+". Try again.")
				return nil
			}
			pty.noteProgramStatusInput(data)
			session.TouchActive()
			cs.lastInput.Store(time.Now().UnixNano())
			pty.noteHumanKey(data)
		} else {
			debugLog("[DEBUG] PTY %s not found for input", shortID(ptyID))
		}
	}

	return nil
}

func (d *Daemon) handleResize(cs *connState, msg *Message) error {
	var payload ResizePTYPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return errfmt.Errorf("invalid resize payload: %w", err)
	}

	if cs.sessionID == "" {
		return nil
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return nil
	}

	if payload.PTYID == "" {
		cs.mu.Lock()
		cs.width = payload.Width
		cs.height = payload.Height
		cs.reserve = payload.Reserve
		cs.mu.Unlock()
		d.recalculateAndBroadcastSize(cs.sessionID, "")
	} else {
		if pty := session.GetPTY(payload.PTYID); pty != nil {
			_ = pty.Resize(payload.Width, payload.Height)
			if w, h, ok := d.sessionCellSize(cs.sessionID); ok {
				_ = pty.UpdatePixelDimensions(w, h)
			}
		}
	}

	return nil
}

func (d *Daemon) handleCreatePTY(cs *connState, msg *Message) error {
	debugLog("[DEBUG] handleCreatePTY called for client %s", cs.clientID)

	if cs.sessionID == "" {
		debugLog("[DEBUG] handleCreatePTY: client not attached")
		return d.replyError(cs, msg, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		debugLog("[DEBUG] handleCreatePTY: session not found")
		return d.replyError(cs, msg, ErrCodeSessionNotFound, "session not found")
	}

	var payload CreatePTYPayload
	if err := msg.ParsePayload(&payload); err != nil {
		debugLog("[DEBUG] handleCreatePTY: invalid payload: %v", err)
		return errfmt.Errorf("invalid create PTY payload: %w", err)
	}

	width := payload.Width
	height := payload.Height
	if width == 0 {
		width = 80
	}
	if height == 0 {
		height = 24
	}

	sessionID := cs.sessionID
	onExit := func(ptyID string) {
		d.notifyPTYClosed(sessionID, ptyID)
	}

	debugLog("[DEBUG] Creating PTY %dx%d for session %s", width, height, session.Name())
	pty, err := session.CreatePTY(payload.WindowID, width, height, onExit)
	if err != nil {
		debugLog("[DEBUG] handleCreatePTY: failed to create PTY: %v", err)
		return d.replyError(cs, msg, ErrCodeInternal, fmt.Sprintf("failed to create PTY: %v", err))
	}

	if w, h, ok := d.sessionCellSize(cs.sessionID); ok {
		if err := pty.UpdatePixelDimensions(w, h); err != nil {
			debugLog("[DEBUG] handleCreatePTY: failed to set pixel size: %v", err)
		}
	}

	debugLog("[DEBUG] PTY created: %s", pty.ID)
	return d.reply(cs, msg, MsgPTYCreated, &PTYCreatedPayload{
		ID:    pty.ID,
		Title: payload.Title,
	})
}

func (d *Daemon) handleClosePTY(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return d.replyError(cs, msg, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.replyError(cs, msg, ErrCodeSessionNotFound, "session not found")
	}

	var payload ClosePTYPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return errfmt.Errorf("invalid close PTY payload: %w", err)
	}

	cs.mu.Lock()
	delete(cs.ptySubscriptions, payload.PTYID)
	cs.mu.Unlock()

	if err := session.ClosePTY(payload.PTYID); err != nil {
		return d.replyError(cs, msg, ErrCodePTYNotFound, err.Error())
	}

	return d.sendMessage(cs, MsgPTYClosed, &ClosePTYPayload{PTYID: payload.PTYID})
}

func (d *Daemon) forgetPushes(cs *connState, sessionID string) {
	cs.mu.Lock()
	origin := cs.pushOrigin
	cs.pushOrigin = ""
	cs.mu.Unlock()
	if session := d.manager.GetSessionByID(sessionID); session != nil {
		session.ForgetPush(origin)
	}
	d.recountTUIClients(sessionID)
}

func (d *Daemon) recountTUIClients(sessionID string) {
	session := d.manager.GetSessionByID(sessionID)
	if session == nil {
		return
	}
	session.recountMu.Lock()
	defer session.recountMu.Unlock()
	animate, tuiClients := true, 0
	images, kittyImages := false, false
	d.clientsMu.RLock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		if cs.sessionID == sessionID && cs.isTUIClient {
			tuiClients++
			if !cs.kittyAnimation {
				animate = false
			}
			if cs.sixelGraphics || cs.kittyGraphics || cs.symbolImages {
				images = true
			}
			if cs.kittyGraphics {
				kittyImages = true
			}
		}
		cs.mu.Unlock()
	}
	d.clientsMu.RUnlock()
	session.SetKittyAnimation(animate && tuiClients > 0)
	if tuiClients > 0 {
		session.SetSixelAdvertised(images)
		session.SetKittyAdvertised(kittyImages)
	}
	if hook := treeOpsCounted.Load(); hook != nil {
		(*hook)()
	}
	session.noteTUIClients(tuiClients)
}

var treeOpsCounted atomic.Pointer[func()]

func (d *Daemon) notePushOrigin(cs *connState, session *Session, origin string) {
	if origin == "" || len(origin) > maxPushOriginLen {
		return
	}
	cs.mu.Lock()
	prev := cs.pushOrigin
	cs.pushOrigin = origin
	cs.mu.Unlock()
	if prev != "" && prev != origin {
		session.ForgetPush(prev)
	}
}

func (d *Daemon) handleUpdateState(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return d.replyError(cs, msg, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.replyError(cs, msg, ErrCodeSessionNotFound, "session not found")
	}

	var state SessionState
	if err := msg.ParsePayload(&state); err != nil {
		return errfmt.Errorf("invalid state payload: %w", err)
	}
	d.notePushOrigin(cs, session, state.PushOrigin)
	if err := validateSessionState(&state); err != nil {
		LogError("Refused a state update from %s: %v", cs.clientID, err)
		session.NotePush(state.PushOrigin, state.PushSeq)
		return d.replyError(cs, msg, ErrCodeInvalidMessage, "state update refused: "+err.Error())
	}
	clampPushedText(&state)
	if session.keepRectsOfStaleLayout(&state) {
		LogBasic("Kept the session's rectangles over a push from %s tiled in an older layout", cs.clientID)
	}

	reportBg, reportFg, reportPal := state.PaneReportBg, state.PaneReportFg, state.PaneReportPalette
	accepted, behind, pushSeq := session.updateStateFrom(&state, d.mayActAsHuman(cs))
	if d.ownsReportColors(cs, session.ID) {
		session.applyReportColors(reportBg, reportFg, reportPal)
	}

	var merged *SessionState
	mergedState := func() *SessionState {
		if merged == nil {
			merged = session.GetState()
		}
		return merged
	}

	reconciled := !accepted || behind

	clientCount := d.getSessionClientCount(cs.sessionID)
	if !reconciled && clientCount <= 1 {
		return nil
	}
	snap := mergedState()
	if hook := statePushSnapshotTaken.Load(); hook != nil {
		(*hook)()
	}
	prepare := func(state *SessionState, withPeers bool) pushSends {
		var sends pushSends
		if reconciled {
			if msg, err := NewMessage(MsgStateSync, &StateSyncPayload{
				State:       state,
				TriggerType: "reconcile",
			}); err == nil {
				sends.toSender = func() { d.queueBroadcast(cs, msg, "reconcile reply") }
			}
		}
		if withPeers && clientCount > 1 {
			if msg, err := NewMessage(MsgStateSync, &StateSyncPayload{
				State:       state,
				TriggerType: "update",
				SourceID:    cs.clientID,
			}); err == nil {
				sends.fp = StateFingerprint(state)
				sends.toPeers = func() { d.broadcastEncodedToSession(cs.sessionID, msg, cs.clientID) }
			}
		}
		return sends
	}
	session.deliverPush(snap, pushSeq, prepare)
	return nil
}

func (d *Daemon) handleSubscribePTY(cs *connState, msg *Message) error {
	debugLog("[DEBUG] handleSubscribePTY called for client %s", cs.clientID)

	if cs.sessionID == "" {
		return d.replyError(cs, msg, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.replyError(cs, msg, ErrCodeSessionNotFound, "session not found")
	}

	var payload SubscribePTYPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return errfmt.Errorf("invalid subscribe PTY payload: %w", err)
	}

	debugLog("[DEBUG] Subscribing to PTY %s", payload.PTYID)
	pty := session.GetPTY(payload.PTYID)
	if pty == nil {
		debugLog("[DEBUG] PTY %s not found", payload.PTYID)
		return d.replyError(cs, msg, ErrCodePTYNotFound, fmt.Sprintf("PTY %s not found", payload.PTYID))
	}

	cs.mu.Lock()
	if _, already := cs.ptySubscriptions[payload.PTYID]; already {
		cs.mu.Unlock()
		debugLog("[DEBUG] PTY %s already subscribed for client %s", payload.PTYID, cs.clientID)
		return nil
	}
	cs.ptySubscriptions[payload.PTYID] = nil
	resume := cs.ptyResume[payload.PTYID]
	if payload.FromSeq > 0 {
		resume = payload.FromSeq
	}
	paces := !cs.viewOnly && !cs.viaLink
	cs.mu.Unlock()

	debugLog("[DEBUG] Starting PTY output stream for %s", payload.PTYID)
	sub := pty.subscribeSub(cs.clientID, resume, payload.FromSnapshot)
	cs.mu.Lock()
	if s, claimed := cs.ptySubscriptions[payload.PTYID]; claimed && s == nil {
		cs.ptySubscriptions[payload.PTYID] = sub
	}
	cs.mu.Unlock()
	pty.rearmPaneMarks()
	if !paces {
		pty.SetPacing(cs.clientID, false)
	}
	go d.streamPTYOutput(cs, pty, sub)

	return nil
}

func (d *Daemon) handleUnsubscribePTY(cs *connState, msg *Message) error {
	debugLog("[DEBUG] handleUnsubscribePTY called for client %s", cs.clientID)

	if cs.sessionID == "" {
		return d.replyError(cs, msg, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.replyError(cs, msg, ErrCodeSessionNotFound, "session not found")
	}

	var payload UnsubscribePTYPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return errfmt.Errorf("invalid unsubscribe PTY payload: %w", err)
	}

	debugLog("[DEBUG] Unsubscribing from PTY %s", payload.PTYID)

	cs.mu.Lock()
	delete(cs.ptySubscriptions, payload.PTYID)
	cs.mu.Unlock()

	pty := session.GetPTY(payload.PTYID)
	if pty != nil {
		resume := pty.Unsubscribe(cs.clientID)
		cs.mu.Lock()
		cs.ptyResume[payload.PTYID] = resume
		cs.mu.Unlock()
		pty.rearmPaneMarks()
		debugLog("[DEBUG] Successfully unsubscribed client %s from PTY %s at %d", cs.clientID, shortID(payload.PTYID), resume)
	}

	return nil
}

func (d *Daemon) handleGetTerminalState(cs *connState, msg *Message) error {
	if cs.sessionID == "" {
		return d.replyError(cs, msg, ErrCodeNotAttached, "not attached to any session")
	}

	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.replyError(cs, msg, ErrCodeSessionNotFound, "session not found")
	}

	var payload GetTerminalStatePayload
	if err := msg.ParsePayload(&payload); err != nil {
		return errfmt.Errorf("invalid get terminal state payload: %w", err)
	}

	pty := session.GetPTY(payload.PTYID)
	if pty == nil {
		return d.replyError(cs, msg, ErrCodePTYNotFound, fmt.Sprintf("PTY %s not found", payload.PTYID))
	}

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
	return d.reply(cs, msg, MsgTerminalState, &TerminalStatePayload{
		PTYID: payload.PTYID,
		State: state,
	})
}
