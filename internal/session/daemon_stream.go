package session

import (
	"log"
	"runtime/debug"
	"time"
)

// streamFrameInterval and streamFloodBytes gate how often a flooding pane is
// written to one client. Once a subscription has sent streamFloodBytes inside
// one streamFrameInterval, the next bytes wait for that interval to end and go
// out together in one frame.
//
// Without the gate a flood went out one frame per PTY read. The stream
// goroutine drains faster than the reader fills, so the batch loop below
// rarely found more than one chunk queued, and on macOS a PTY read is about
// 330 bytes. That was about 2,900 frames per MiB, each one a daemon write, a
// client read and a handful of goroutine wakeups, for a client that draws at
// most once every 8 ms anyway. Output below streamFloodBytes per interval is
// never held, so a keystroke echo into a quiet pane goes out at once.
//
// Variables, not constants, so a test can widen the window enough to observe.
var (
	streamFrameInterval = time.Millisecond
	streamFloodBytes    = 4096
)

// streamPTYOutput streams raw PTY bytes to a subscriber with batching.
// Multiple channel reads are coalesced into a single connection write to
// reduce syscall overhead (30K+ reads/sec at 500fps doom fire → one large
// write per batch instead of one per read).
//
// sub is the subscription this goroutine owns. It is replaced only by
// resumeAfterGap, and everything the goroutine releases it releases by
// identity, never by client ID alone.
func (d *Daemon) streamPTYOutput(cs *connState, pty *PTY, sub *ptySubscriber) {
	// On any exit, stop receiving from the PTY and drop the subscription entry so
	// the connState is left coherent: a later re-subscribe must not be blocked by
	// a stale "already subscribed" guard (daemon_handlers.go), and no PTY keeps
	// broadcasting into an unread channel.
	//
	// Both act only while they still refer to this goroutine's subscriber. A
	// pane hidden and shown quickly unsubscribes and subscribes again while
	// this goroutine is still blocked in a write. Cleaning up by client ID then
	// removed the new subscription and its entry, so the new goroutine's
	// channel was closed under it and the pane stopped updating.
	defer func() {
		pty.unsubscribeSub(cs.clientID, sub)
		cs.mu.Lock()
		if cs.ptySubscriptions[pty.ID] == sub {
			delete(cs.ptySubscriptions, pty.ID)
		}
		cs.mu.Unlock()
	}()

	const maxBatch = 256 * 1024
	// Grown by the first output and kept after that. Made up front it was
	// 256 KiB for every client and pane pair, including panes that never
	// print.
	var batch []byte
	var outputCh <-chan ptyChunk = sub.ch
	// take accounts for a chunk taken off the stream, so broadcast can tell
	// how much this client still holds, and adds its bytes to the batch. A
	// frame broadcast dropped for a newer one adds nothing.
	take := func(c ptyChunk) {
		batch = takeChunk(batch, c, sub)
		pty.wakePacer()
	}

	// The frame window: when it opened and how many bytes went out in it.
	// hold is the one timer a held stream waits on, made the first time.
	var windowStart time.Time
	windowBytes := 0
	var hold *time.Timer
	// resize is a value, not a pointer: taking the address of the receive
	// variables made them escape, one allocation per chunk.
	var resize ptyChunk

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
			batch = batch[:0]
			resize = ptyChunk{}
			// A dropped frame costs nothing to skip.
			if chunk.frame != nil && chunk.frame.state.Load() == frameDropped {
				goto send
			}
			// A resize is never held: it carries no bytes and it ends a batch
			// anyway. Bytes after a full window wait for the window to end, and
			// whatever queued meanwhile goes out with them below.
			if !chunk.isResize() && windowBytes >= streamFloodBytes {
				if wait := streamFrameInterval - time.Since(windowStart); wait > 0 {
					if hold == nil {
						hold = time.NewTimer(wait)
					} else {
						hold.Reset(wait)
					}
					select {
					case <-hold.C:
					case <-cs.done:
						return
					case <-d.ctx.Done():
						return
					}
				}
			}
			// A resize marks the byte the daemon's emulator changed width at,
			// so it ends the batch in front of it and is sent on its own.
			// Coalescing it into the bytes either side would put the client's
			// emulator at the wrong width for one of them.
			if chunk.isResize() {
				resize = chunk
			} else {
				take(chunk)
				for len(batch) < maxBatch {
					select {
					case more, ok := <-outputCh:
						if !ok {
							goto send
						}
						if more.isResize() {
							resize = more
							goto send
						}
						take(more)
					default:
						goto send
					}
				}
			}
		send:
			// A frame can make the batch larger than maxBatch, so it goes out
			// in pieces of at most maxBatch, each with its own deadline.
			for out := batch; len(out) > 0; {
				piece := out[:min(len(out), maxBatch)]
				out = out[len(piece):]
				cs.sendMu.Lock()
				_ = cs.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				err := WritePTYOutput(cs.conn, pty.ID, piece)
				cs.sendMu.Unlock()
				if now := time.Now(); now.Sub(windowStart) >= streamFrameInterval {
					windowStart, windowBytes = now, len(piece)
				} else {
					windowBytes += len(piece)
				}
				if err != nil {
					// The write failed mid-frame (a slow/stuck client hitting the 5s
					// deadline): the wire now carries a partial frame and every later
					// send would append onto a desynced stream. Tear the whole client
					// down rather than leaving it half-subscribed and desynced.
					cs.drop()
					return
				}
			}
			if resize.isResize() {
				if err := d.sendMessage(cs, MsgPTYResized, &PTYResizedPayload{
					PTYID:  pty.ID,
					Width:  resize.width,
					Height: resize.height,
				}); err != nil {
					return
				}
			}
			// A stream that was gapped while this client was slow has drained
			// by the time the channel is empty. Rebuild it from where it got
			// to, so the client is handed what it missed instead of the rest
			// of the stream painted over a hole.
			if ch, next := pty.resumeAfterGap(cs.clientID, sub); ch != nil {
				cs.mu.Lock()
				if cs.ptySubscriptions[pty.ID] == sub {
					cs.ptySubscriptions[pty.ID] = next
				}
				cs.mu.Unlock()
				outputCh, sub = ch, next
			}
		}
	}
}

// takeChunk takes one chunk off a client's stream: it appends the chunk's
// bytes to batch and settles the accounting broadcast reads. A frame that
// broadcast dropped for a newer one adds nothing.
func takeChunk(batch []byte, c ptyChunk, sub *ptySubscriber) []byte {
	if c.frame != nil {
		if c.frame.take() {
			for _, part := range c.frame.parts {
				batch = append(batch, part...)
			}
		}
		return batch
	}
	if sub != nil {
		sub.queued.Add(-int64(len(c.data)))
	}
	return append(batch, c.data...)
}

// notifyPTYClosed sends MsgPTYClosed to every client attached to the session
// that owns the PTY. This is called when the PTY process exits (e.g., user
// types exit or Ctrl+D).
//
// It goes to every attached client, subscribed to the PTY or not. A client
// streams only the panes on the workspace it shows, and this message is the
// only way a client learns that a pane's program ended: the client closes
// the window, and that close is what removes it from the session. Sent to
// subscribers only, a pane on a hidden workspace outlived its program and
// stayed listed until somebody showed its workspace and closed it by hand.
func (d *Daemon) notifyPTYClosed(sessionID, ptyID string) {
	debugLog("[DEBUG] notifyPTYClosed: sessionID=%s, ptyID=%s", shortID(sessionID), shortID(ptyID))

	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	for _, cs := range d.clients {
		// Only notify clients attached to this session. Read the guarded
		// fields under cs.mu (clientsMu is already held, preserving the
		// clientsMu-then-cs.mu order).
		cs.mu.Lock()
		// attached for the same reason as broadcastToSession: nothing
		// unsolicited reaches a client before its attach reply.
		match := cs.sessionID == sessionID && cs.attached
		cs.mu.Unlock()
		if !match {
			continue
		}

		debugLog("[DEBUG] notifyPTYClosed: sending to client %s", cs.clientID)
		// Send in a goroutine to avoid blocking if client is slow
		client := cs
		d.goTracked(func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("PANIC in notifyPTYClosed send goroutine: %v\n%s", r, debug.Stack())
				}
			}()
			if err := d.sendMessage(client, MsgPTYClosed, &ClosePTYPayload{PTYID: ptyID}); err != nil {
				debugLog("[DEBUG] notifyPTYClosed: failed to send to client: %v", err)
			}
		})
	}
}

func (d *Daemon) sendMessage(cs *connState, msgType MessageType, payload any) error {
	msg, err := NewMessage(msgType, payload)
	if err != nil {
		return err
	}
	return d.sendEncoded(cs, msg)
}

// sendEncoded writes an already encoded message to one client, dropping the
// client if the write fails.
func (d *Daemon) sendEncoded(cs *connState, msg *Message) error {
	cs.sendMu.Lock()
	_ = cs.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err := WriteMessage(cs.conn, msg)
	cs.sendMu.Unlock()
	if err != nil {
		// A mid-frame write failure permanently desyncs framing for this
		// connection; drop the client so the read loop runs its full cleanup
		// instead of appending later frames onto a corrupt stream.
		cs.drop()
	}
	return err
}

// reply sends msgType as the answer to req, tagged with req's request id so
// the client can tell it from the answer to any other request. Everything a
// handler sends in answer to the message it is handling goes through here or
// replyError; a broadcast or push that happens to share a type does not.
//
// A client matched replies by type alone before ids existed, so an error a
// fire-and-forget subscribe drew was taken as the answer to whatever state
// request was waiting, and the state that request was really answered with
// went to the next one. See requestIDs on TUIClient.
func (d *Daemon) reply(cs *connState, req *Message, msgType MessageType, payload any) error {
	msg, err := NewMessage(msgType, payload)
	if err != nil {
		return err
	}
	if req != nil {
		msg.ReqID = req.ReqID
	}
	return d.sendEncoded(cs, msg)
}

// replyError sends an error as the answer to req. See reply.
func (d *Daemon) replyError(cs *connState, req *Message, code int, message string) error {
	return d.reply(cs, req, MsgError, &ErrorPayload{
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
// the session says before the flag catches up (a session being killed, most of
// all) is dropped on the floor. The window is a few instructions and has not
// been caught in the act; it is closed here because it costs one lock to close
// and nothing about it is bounded by how narrow it happens to be today.
func (d *Daemon) sendAttachReply(cs *connState, req *Message, payload *AttachedPayload) error {
	msg, err := NewMessage(MsgAttached, payload)
	if err != nil {
		return err
	}
	msg.ReqID = req.ReqID

	cs.sendMu.Lock()
	cs.mu.Lock()
	cs.attached = true
	// Written with the reply, under sendMu: a message sent to the client
	// for this session after this point cannot overtake the reply.
	cs.repliedSession = payload.SessionID
	cs.mu.Unlock()
	_ = cs.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err = WriteMessage(cs.conn, msg)
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
	// Encoded once. Every client speaks the one codec there is, and a state
	// push is the whole session, so encoding it on each client's goroutine
	// was the same gob of the same state as many times as there were peers.
	msg, err := NewMessage(msgType, payload)
	if err != nil {
		debugLog("[DEBUG] broadcastToSession: encode: %v", err)
		return
	}

	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	for _, cs := range d.clients {
		cs.mu.Lock()
		// attached, not just sessionID: a client mid-attach is counted by
		// everything that measures the session and spoken to by nothing. See
		// connState.attached.
		member := cs.sessionID == sessionID && cs.isTUIClient && cs.clientID != excludeClientID
		match := member && cs.attached
		// A client still attaching is told afterwards that it missed a state,
		// by its attach handler. See connState.missedStateSync.
		if member && !cs.attached && msgType == MsgStateSync {
			cs.missedStateSync = true
		}
		cs.mu.Unlock()
		if !match {
			continue
		}
		d.queueBroadcast(cs, msg, "broadcastToSession")
	}
}

// queueBroadcast writes one already encoded broadcast to one client, off this
// goroutine and in turn.
//
// Sent off this goroutine so a slow client blocks nobody else, and behind a
// ticket so two broadcasts to one client cannot pass each other on the way: a
// goroutine per send with no order between them delivered a client's two
// pushes to a peer swapped, and the peer that adopted the older one last held a
// tree the session had moved on from. what names the caller in the log.
func (d *Daemon) queueBroadcast(cs *connState, msg *Message, what string) {
	ticket := cs.takeBroadcastTicket()
	client := cs
	started := d.goTracked(func() {
		defer client.finishBroadcast()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("PANIC in %s send goroutine: %v\n%s", what, r, debug.Stack())
			}
		}()
		client.awaitBroadcastTurn(ticket)
		if err := d.sendEncoded(client, msg); err != nil {
			debugLog("[DEBUG] %s: failed to send to client %s: %v", what, client.clientID, err)
		}
	})
	if !started {
		// The daemon is stopping, so the message is dropped. The ticket is
		// still released in its turn: a later ticket that did start waits on
		// it, and releasing it early would skip the count past that ticket.
		go func() {
			client.awaitBroadcastTurn(ticket)
			client.finishBroadcast()
		}()
	}
}
