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
func (d *Daemon) streamPTYOutput(cs *connState, pty *PTY) {
	outputCh := pty.Subscribe(cs.clientID)

	const maxBatch = 256 * 1024
	batch := make([]byte, 0, maxBatch)

	for {
		select {
		case <-cs.done:
			pty.Unsubscribe(cs.clientID)
			return
		case <-d.ctx.Done():
			return
		case data, ok := <-outputCh:
			if !ok {
				return
			}
			batch = append(batch[:0], data...)
			for len(batch) < maxBatch {
				select {
				case more, ok := <-outputCh:
					if !ok {
						goto send
					}
					batch = append(batch, more...)
				default:
					goto send
				}
			}
		send:
			cs.sendMu.Lock()
			_ = cs.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			err := WritePTYOutput(cs.conn, pty.ID, batch)
			cs.sendMu.Unlock()
			if err != nil {
				pty.Unsubscribe(cs.clientID)
				return
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
		match := cs.sessionID == sessionID
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
	defer cs.sendMu.Unlock()

	_ = cs.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return WriteMessageWithCodec(cs.conn, msg, cs.codec)
}

func (d *Daemon) sendError(cs *connState, code int, message string) error {
	return d.sendMessage(cs, MsgError, &ErrorPayload{
		Code:    code,
		Message: message,
	})
}

func (d *Daemon) sendPong(cs *connState) error {
	return d.sendMessage(cs, MsgPong, nil)
}

// broadcastToSession sends a message to all TUI clients attached to a session.
// If excludeClientID is non-empty, that client is excluded from the broadcast.
func (d *Daemon) broadcastToSession(sessionID string, msgType MessageType, payload any, excludeClientID string) {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.sessionID == sessionID && cs.isTUIClient
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
