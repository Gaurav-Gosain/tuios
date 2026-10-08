package session

import (
	"errors"
	"time"
)

// A large input from the client, such as a paste, can be refused by the
// daemon: with ErrCodeBusy when other large messages hold its read budget or
// the pane has not read the last large input (see frame_budget.go), with
// ErrCodeForbidden when the pane does not take input from this client. The
// client does not let such a paste vanish, and does not let later input
// overtake it.
//
//   - A large input goes out with a request id, so a refusal names it, and
//     right behind it a MsgPing. The daemon handles a connection's frames in
//     order, so the pong says the paste was taken or refused.
//   - Until the pong, input to the same pane is held in the client, in order
//     (inputHold). Input to other panes is not held.
//   - A paste refused as busy is sent again once after pasteRetryDelay, with
//     the held input still behind it. A second refusal, or any other, is
//     reported to OnPasteRefused, which the app shows to the person, and the
//     held input is sent.
//   - A paste the daemon took is forgotten at its pong.
//
// A daemon that does not tag replies gets every input as it comes, with no
// hold and no retry, as before.

const (
	pasteRetryDelay = 500 * time.Millisecond

	// maxPasteBytes is the largest input one frame can carry: the frame
	// limit less the type, codec, request id and pane id.
	maxPasteBytes = maxFrameBytes - 2 - reqIDLen - 36
)

// ErrPasteTooLarge refuses an input larger than one frame can carry.
var ErrPasteTooLarge = errors.New("the paste is larger than the daemon takes")

// What the person is shown when a paste did not reach the pane, by why.
const (
	PasteRefusedForbidden = "This pane does not allow pastes."
	PasteRefusedTooLarge  = "The paste is too large."
	PasteRefusedBusy      = "The daemon was busy. Try again."
	PasteRefusedOther     = "The paste did not reach the pane."
)

// PasteRefusedHandler takes the pane a paste was refused for, and what to
// tell the person: one of the PasteRefused texts.
type PasteRefusedHandler func(ptyID, message string)

// pasteRecord is a large input sent and not yet settled by its pong.
type pasteRecord struct {
	ptyID   string
	data    []byte
	retried bool
	// code is the error code the daemon refused the input with, 0 while it
	// has not refused it.
	code int
}

// inputHold is the input for one pane held behind a paste that is not
// settled. Guarded by the client's write lock, c.mu.
type inputHold struct {
	queue [][]byte
}

// OnPasteRefused sets the handler for a paste that did not reach its pane.
func (c *TUIClient) OnPasteRefused(handler PasteRefusedHandler) {
	c.multiClientMu.Lock()
	c.pasteRefusedHandler = handler
	c.multiClientMu.Unlock()
}

// pasteRefused tells the handler a paste did not reach ptyID.
func (c *TUIClient) pasteRefused(ptyID, message string) {
	c.multiClientMu.RLock()
	handler := c.pasteRefusedHandler
	c.multiClientMu.RUnlock()
	if handler != nil {
		handler(ptyID, message)
	}
}

// refusalMessage is what to tell the person for a paste refused with code.
func refusalMessage(code int) string {
	switch code {
	case ErrCodeForbidden:
		return PasteRefusedForbidden
	case ErrCodeInvalidMessage:
		// A frame over its type's limit (FrameTooLargeError).
		return PasteRefusedTooLarge
	case ErrCodeBusy:
		return PasteRefusedBusy
	}
	return PasteRefusedOther
}

// isLargeInput reports whether input of n bytes goes out as a paste: one
// whose frame payload, the pane id and the data, the daemon may refuse as
// busy. Only to a daemon that tags replies.
func (c *TUIClient) isLargeInput(n int) bool {
	return 36+n > largeFrame && c.requestIDs.Load()
}

// writeInputLocked sends input to a pane, or holds it behind a paste to the
// same pane that is not settled. The caller holds c.mu.
func (c *TUIClient) writeInputLocked(ptyID string, data []byte) error {
	if hold := c.inputHolds[ptyID]; hold != nil {
		// The caller may reuse data once this returns.
		hold.queue = append(hold.queue, append([]byte(nil), data...))
		return nil
	}
	if c.isLargeInput(len(data)) {
		return c.sendPasteLocked(ptyID, append([]byte(nil), data...), false)
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return WritePTYInput(c.conn, ptyID, data)
}

// sendPasteLocked sends a large input with a request id and a ping behind it,
// and holds later input to the pane until the pong. The caller holds c.mu.
func (c *TUIClient) sendPasteLocked(ptyID string, data []byte, retried bool) error {
	if c.inputHolds == nil {
		c.inputHolds = make(map[string]*inputHold)
	}
	if c.inputHolds[ptyID] == nil {
		c.inputHolds[ptyID] = &inputHold{}
	}
	id, ping := c.nextReqID.Add(1), c.nextReqID.Add(1)
	rec := &pasteRecord{ptyID: ptyID, data: data, retried: retried}
	c.pastesMu.Lock()
	if c.pastes == nil {
		c.pastes = make(map[uint64]*pasteRecord)
		c.pasteBarriers = make(map[uint64]*pasteRecord)
	}
	c.pastes[id] = rec
	c.pasteBarriers[ping] = rec
	c.pastesMu.Unlock()

	payload := make([]byte, 36+len(data))
	copy(payload, ptyID)
	copy(payload[36:], data)
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err := WriteMessage(c.conn, &Message{Type: MsgInput, Payload: payload, ReqID: id})
	if err == nil {
		err = WriteMessage(c.conn, &Message{Type: MsgPing, ReqID: ping})
	}
	if err != nil {
		c.pastesMu.Lock()
		delete(c.pastes, id)
		delete(c.pasteBarriers, ping)
		c.pastesMu.Unlock()
		delete(c.inputHolds, ptyID)
	}
	return err
}

// takePasteReply takes a reply to a paste or to the ping behind it, and
// reports whether msg was one. It runs on the read loop, so the work that
// writes goes to a goroutine of its own.
func (c *TUIClient) takePasteReply(msg *Message) bool {
	if msg.ReqID == 0 {
		return false
	}
	c.pastesMu.Lock()
	defer c.pastesMu.Unlock()
	if rec := c.pastes[msg.ReqID]; rec != nil {
		delete(c.pastes, msg.ReqID)
		if msg.Type == MsgError {
			var payload ErrorPayload
			_ = msg.ParsePayload(&payload)
			rec.code = payload.Code
			if rec.code == 0 {
				rec.code = ErrCodeUnknown
			}
			debugLog("[CLIENT] the daemon refused a paste of %d bytes: %s", len(rec.data), payload.Message)
		}
		return true
	}
	if rec := c.pasteBarriers[msg.ReqID]; rec != nil {
		delete(c.pasteBarriers, msg.ReqID)
		go c.settlePaste(rec)
		return true
	}
	return false
}

// settlePaste acts on a paste whose ping came back: it was taken, or refused
// and is sent again or reported. Held input goes out after it either way.
func (c *TUIClient) settlePaste(rec *pasteRecord) {
	if rec.code == ErrCodeBusy && !rec.retried {
		debugLog("[CLIENT] the daemon was busy with a paste of %d bytes, sending it again", len(rec.data))
		time.Sleep(pasteRetryDelay)
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.conn != nil && c.sendPasteLocked(rec.ptyID, rec.data, true) == nil {
			return
		}
		c.pasteRefused(rec.ptyID, PasteRefusedBusy)
		return
	}
	if rec.code != 0 {
		c.pasteRefused(rec.ptyID, refusalMessage(rec.code))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.releaseHoldLocked(rec.ptyID)
}

// releaseHoldLocked sends the input held for a pane, in order. A held paste
// goes out as a paste, and what is behind it stays held. The caller holds
// c.mu.
func (c *TUIClient) releaseHoldLocked(ptyID string) {
	hold := c.inputHolds[ptyID]
	if hold == nil {
		return
	}
	delete(c.inputHolds, ptyID)
	for i, data := range hold.queue {
		if c.conn == nil {
			return
		}
		if c.isLargeInput(len(data)) {
			if c.sendPasteLocked(ptyID, data, false) == nil {
				c.inputHolds[ptyID].queue = append(c.inputHolds[ptyID].queue, hold.queue[i+1:]...)
			}
			return
		}
		_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if WritePTYInput(c.conn, ptyID, data) != nil {
			return
		}
	}
}
