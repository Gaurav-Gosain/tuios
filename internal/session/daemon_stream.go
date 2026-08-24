package session

import (
	"log"
	"runtime/debug"
	"time"
)

// streamPTYOutput streams raw PTY bytes to a subscriber with batching.
// Multiple channel reads are coalesced into a single connection write to
// reduce syscall overhead (30K+ reads/sec at 500fps doom fire → one large
// write per batch instead of one per read).
func (d *Daemon) streamPTYOutput(cs *connState, pty *PTY, outputCh <-chan ptyChunk) {
	// On any exit, stop receiving from the PTY and drop the subscription entry so
	// the connState is left coherent: a later re-subscribe must not be blocked by
	// a stale "already subscribed" guard (daemon_handlers.go), and no PTY keeps
	// broadcasting into an unread channel.
	defer func() {
		pty.Unsubscribe(cs.clientID)
		cs.mu.Lock()
		delete(cs.ptySubscriptions, pty.ID)
		cs.mu.Unlock()
	}()

	const maxBatch = 256 * 1024
	batch := make([]byte, 0, maxBatch)

	for {
		select {
		case <-cs.done:
			return
		case <-d.ctx.Done():
			return
		case chunk, ok := <-outputCh:
			if !ok {
				return
			}
			// A resize marks the byte the daemon's emulator changed width at,
			// so it ends the batch in front of it and is sent on its own.
			// Coalescing it into the bytes either side would put the client's
			// emulator at the wrong width for one of them.
			var resize *ptyChunk
			if chunk.isResize() {
				resize = &chunk
				batch = batch[:0]
			} else {
				batch = append(batch[:0], chunk.data...)
				for len(batch) < maxBatch {
					select {
					case more, ok := <-outputCh:
						if !ok {
							goto send
						}
						if more.isResize() {
							resize = &more
							goto send
						}
						batch = append(batch, more.data...)
					default:
						goto send
					}
				}
			}
		send:
			if len(batch) > 0 {
				cs.sendMu.Lock()
				_ = cs.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				err := WritePTYOutput(cs.conn, pty.ID, batch)
				cs.sendMu.Unlock()
				if err != nil {
					// The write failed mid-frame (a slow/stuck client hitting the 5s
					// deadline): the wire now carries a partial frame and every later
					// send would append onto a desynced stream. Tear the whole client
					// down rather than leaving it half-subscribed and desynced.
					cs.drop()
					return
				}
			}
			if resize != nil {
				if err := d.sendMessage(cs, MsgPTYResized, &PTYResizedPayload{
					PTYID:  pty.ID,
					Width:  resize.width,
					Height: resize.height,
				}); err != nil {
					return
				}
			}
		}
	}
}

// notifyPTYClosed sends MsgPTYClosed to all clients subscribed to the given PTY.
// This is called when the PTY process exits (e.g., user types exit or Ctrl+D).
func (d *Daemon) notifyPTYClosed(sessionID, ptyID string) {
	debugLog("[DEBUG] notifyPTYClosed: sessionID=%s, ptyID=%s", shortID(sessionID), shortID(ptyID))

	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	for _, cs := range d.clients {
		// Only notify clients attached to this session and subscribed to this
		// PTY. Read the guarded fields under cs.mu (clientsMu is already held,
		// preserving the clientsMu-then-cs.mu order).
		cs.mu.Lock()
		// attached for the same reason as broadcastToSession. A client mid
		// attach holds no subscriptions either, so this is belt as well as
		// braces - but the rule is "nothing unsolicited before the reply", and
		// a rule each call site re-derives is a rule waiting to be missed.
		match := cs.sessionID == sessionID && cs.attached
		if match {
			_, match = cs.ptySubscriptions[ptyID]
		}
		cs.mu.Unlock()
		if !match {
			continue
		}

		debugLog("[DEBUG] notifyPTYClosed: sending to client %s", cs.clientID)
		// Send in a goroutine to avoid blocking if client is slow
		d.wg.Add(1)
		go func(client *connState) {
			defer d.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("PANIC in notifyPTYClosed send goroutine: %v\n%s", r, debug.Stack())
				}
			}()
			if err := d.sendMessage(client, MsgPTYClosed, &ClosePTYPayload{PTYID: ptyID}); err != nil {
				debugLog("[DEBUG] notifyPTYClosed: failed to send to client: %v", err)
			}
		}(cs)
	}
}

func (d *Daemon) sendMessage(cs *connState, msgType MessageType, payload any) error {
	msg, err := NewMessageWithCodec(msgType, payload, cs.codec)
	if err != nil {
		return err
	}

	cs.sendMu.Lock()
	_ = cs.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err = WriteMessageWithCodec(cs.conn, msg, cs.codec)
	cs.sendMu.Unlock()
	if err != nil {
		// A mid-frame write failure permanently desyncs framing for this
		// connection; drop the client so the read loop runs its full cleanup
		// instead of appending later frames onto a corrupt stream.
		cs.drop()
	}
	return err
}

func (d *Daemon) sendError(cs *connState, code int, message string) error {
	return d.sendMessage(cs, MsgError, &ErrorPayload{
		Code:    code,
		Message: message,
	})
}

// sendAttachReply writes the attach reply and opens this client to the
// session's broadcasts, in that order and with no gap between them.
//
// The flag is set inside the same send lock the reply is written under, which
// is what makes the pair indivisible. A broadcast that reads the flag as set
// queues behind the reply on that lock instead of overtaking it, and a
// broadcast that reads it as unset ran before the reply was written, when the
// client had nothing to reconcile against anyway.
//
// Setting it after the write instead leaves a window of exactly the wrong kind:
// the client believes it is attached the moment the reply lands, and anything
// the session says before the flag catches up - a session being killed, most of
// all - is dropped on the floor. The window is a few instructions and has not
// been caught in the act; it is closed here because it costs one lock to close
// and nothing about it is bounded by how narrow it happens to be today.
func (d *Daemon) sendAttachReply(cs *connState, payload *AttachedPayload) error {
	msg, err := NewMessageWithCodec(MsgAttached, payload, cs.codec)
	if err != nil {
		return err
	}

	cs.sendMu.Lock()
	cs.mu.Lock()
	cs.attached = true
	cs.mu.Unlock()
	_ = cs.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err = WriteMessageWithCodec(cs.conn, msg, cs.codec)
	cs.sendMu.Unlock()

	if err != nil {
		// Same treatment as any other failed write: framing is unrecoverable,
		// so the read loop is left to run its full cleanup.
		cs.drop()
	}
	return err
}

// broadcastToSession sends a message to all TUI clients attached to a session.
// If excludeClientID is non-empty, that client is excluded from the broadcast.
func (d *Daemon) broadcastToSession(sessionID string, msgType MessageType, payload any, excludeClientID string) {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	for _, cs := range d.clients {
		cs.mu.Lock()
		// attached, not just sessionID: a client mid-attach is counted by
		// everything that measures the session and spoken to by nothing. See
		// connState.attached.
		match := cs.sessionID == sessionID && cs.isTUIClient && cs.attached
		cs.mu.Unlock()
		if !match {
			continue
		}
		if cs.clientID == excludeClientID {
			continue
		}
		// Send in a goroutine to avoid blocking if client is slow
		d.wg.Add(1)
		go func(client *connState) {
			defer d.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("PANIC in broadcastToSession send goroutine: %v\n%s", r, debug.Stack())
				}
			}()
			if err := d.sendMessage(client, msgType, payload); err != nil {
				debugLog("[DEBUG] broadcastToSession: failed to send to client %s: %v", client.clientID, err)
			}
		}(cs)
	}
}
