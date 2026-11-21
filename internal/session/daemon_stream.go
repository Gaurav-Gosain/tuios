package session

import (
	"log"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

var (
	streamFrameInterval = time.Millisecond
	streamFloodBytes    = 4096
)

const streamBatchKeep = 32 * 1024

func (d *Daemon) streamPTYOutput(cs *connState, pty *PTY, sub *ptySubscriber) {
	defer func() {
		pty.unsubscribeSub(cs.clientID, sub)
		cs.mu.Lock()
		if cs.ptySubscriptions[pty.ID] == sub {
			delete(cs.ptySubscriptions, pty.ID)
		}
		cs.mu.Unlock()
	}()

	const maxBatch = 256 * 1024
	var batch []byte
	queue := &sub.out
	take := func(c ptyChunk) {
		batch = takeChunk(batch, c, sub)
		pty.wakePacer()
	}

	var windowStart time.Time
	windowBytes := 0
	var hold *time.Timer
	var trim *time.Timer
	var resize ptyChunk

	for {
		select {
		case <-cs.done:
			return
		case <-d.ctx.Done():
			return
		default:
		}
		chunk, ok, closed := queue.pop()
		if closed {
			return
		}
		if !ok {
			var trimC <-chan time.Time
			if queue.oversized() || cap(batch) > streamBatchKeep {
				if trim == nil {
					trim = time.NewTimer(queueTrimAfter)
				} else {
					trim.Reset(queueTrimAfter)
				}
				trimC = trim.C
			}
			select {
			case <-cs.done:
				return
			case <-d.ctx.Done():
				return
			case <-queue.wake:
			case <-trimC:
				queue.trim()
				if cap(batch) > streamBatchKeep {
					batch = nil
				}
			}
			continue
		}
		batch = batch[:0]
		resize = ptyChunk{}
		if chunk.frame != nil && chunk.frame.state.Load() == frameDropped {
			goto send
		}
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
		if chunk.isResize() {
			resize = chunk
		} else {
			take(chunk)
			for len(batch) < maxBatch {
				more, ok, _ := queue.pop()
				if !ok {
					goto send
				}
				if more.isResize() {
					resize = more
					goto send
				}
				take(more)
			}
		}
	send:
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
		if q, next := pty.resumeAfterGap(cs.clientID, sub); q != nil {
			cs.mu.Lock()
			if cs.ptySubscriptions[pty.ID] == sub {
				cs.ptySubscriptions[pty.ID] = next
			}
			cs.mu.Unlock()
			queue, sub = q, next
		}
	}
}

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

func (d *Daemon) notifyPTYClosed(sessionID, ptyID string) {
	if holder := d.sessionHoldingPTY(ptyID); holder != nil {
		sessionID = holder.ID
	}
	debugLog("[DEBUG] notifyPTYClosed: sessionID=%s, ptyID=%s", shortID(sessionID), shortID(ptyID))

	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.sessionID == sessionID && cs.attached
		cs.mu.Unlock()
		if !match {
			continue
		}

		debugLog("[DEBUG] notifyPTYClosed: sending to client %s", cs.clientID)
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

func (d *Daemon) sendEncoded(cs *connState, msg *Message) error {
	cs.sendMu.Lock()
	_ = cs.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err := WriteMessage(cs.conn, msg)
	cs.sendMu.Unlock()
	if err != nil {
		cs.drop()
	}
	return err
}

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

//go:noinline
func (d *Daemon) replyError(cs *connState, req *Message, code int, message string) error {
	return d.reply(cs, req, MsgError, &ErrorPayload{
		Code:    code,
		Message: message,
	})
}

func (d *Daemon) sendAttachReply(cs *connState, req *Message, payload *AttachedPayload) error {
	msg, err := NewMessage(MsgAttached, payload)
	if err != nil {
		return err
	}
	msg.ReqID = req.ReqID

	cs.sendMu.Lock()
	cs.mu.Lock()
	cs.attached = true
	cs.repliedSession = payload.SessionID
	paneMarks := cs.paneMarks
	cs.mu.Unlock()
	_ = cs.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err = WriteMessage(cs.conn, msg)
	cs.sendMu.Unlock()
	if paneMarks {
		if s := d.manager.GetSessionByID(payload.SessionID); s != nil {
			s.rearmPaneMarks()
		}
	}

	if err != nil {
		cs.drop()
	}
	return err
}

func (d *Daemon) broadcastToSession(sessionID string, msgType MessageType, payload any, excludeClientID string) {
	if !d.sessionHasMember(sessionID, excludeClientID) {
		return
	}
	msg, err := NewMessage(msgType, payload)
	if err != nil {
		debugLog("[DEBUG] broadcastToSession: encode: %v", err)
		return
	}
	d.broadcastEncodedToSession(sessionID, msg, excludeClientID)
}

func (d *Daemon) sessionHasMember(sessionID, excludeClientID string) bool {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		member := cs.sessionID == sessionID && cs.isTUIClient && cs.clientID != excludeClientID
		cs.mu.Unlock()
		if member {
			return true
		}
	}
	return false
}

func (d *Daemon) broadcastEncodedToSession(sessionID string, msg *Message, excludeClientID string) {
	msgType := msg.Type
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	for _, cs := range d.clients {
		cs.mu.Lock()
		member := cs.sessionID == sessionID && cs.isTUIClient && cs.clientID != excludeClientID
		match := member && cs.attached
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

var broadcastSendHeld atomic.Pointer[func()]

var statePushTestHold = sync.OnceValue(func() time.Duration {
	return min(testknob.Millis("state_push_hold_ms"), time.Minute)
})

func (d *Daemon) sendInBroadcastOrder(cs *connState, msgType MessageType, payload any) error {
	msg, err := NewMessage(msgType, payload)
	if err != nil {
		return err
	}
	ticket := cs.takeBroadcastTicket()
	defer cs.finishBroadcast()
	cs.awaitBroadcastTurn(ticket)
	return d.sendEncoded(cs, msg)
}

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
		if hook := broadcastSendHeld.Load(); hook != nil {
			(*hook)()
		}
		if msg.Type == MsgStateSync {
			if hold := statePushTestHold(); hold > 0 {
				time.Sleep(hold)
			}
		}
		if err := d.sendEncoded(client, msg); err != nil {
			debugLog("[DEBUG] %s: failed to send to client %s: %v", what, client.clientID, err)
		}
	})
	if !started {
		go func() {
			client.awaitBroadcastTurn(ticket)
			client.finishBroadcast()
		}()
	}
}
