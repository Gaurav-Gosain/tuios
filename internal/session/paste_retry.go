package session

import (
	"errors"
	"time"
)

// A large input from the client, such as a paste, can be refused by the
// daemon with ErrCodeBusy: other large messages hold its read budget, or the
// pane has not read the last large input. See frame_budget.go. The client
// does not let such a paste vanish. It sends each large input with a request
// id, so the refusal names it, sends a refused one again once after
// pasteRetryDelay, and reports a second refusal to OnPasteRefused, which the
// app shows to the person.
//
// The daemon answers a large input only when it refuses it, so the record of
// each one is kept for pasteRecordTTL and then dropped.

const (
	pasteRetryDelay = 500 * time.Millisecond
	pasteRecordTTL  = time.Minute

	// maxPasteBytes is the largest input one frame can carry: the frame
	// limit less the type, codec, request id and pane id.
	maxPasteBytes = maxFrameBytes - 2 - reqIDLen - 36
)

// ErrPasteTooLarge refuses an input larger than one frame can carry.
var ErrPasteTooLarge = errors.New("the paste is larger than the daemon takes")

// PasteRefusedMessage is what the person is shown when a paste did not reach
// the pane.
const PasteRefusedMessage = "The paste was too large or the daemon was busy. Try again."

// PasteRefusedHandler takes the pane a paste was refused for.
type PasteRefusedHandler func(ptyID string)

// pasteRecord is a large input sent and not yet answered.
type pasteRecord struct {
	ptyID   string
	data    []byte
	retried bool
	sent    time.Time
}

// OnPasteRefused sets the handler for a paste the daemon refused twice, or
// one too large to send.
func (c *TUIClient) OnPasteRefused(handler PasteRefusedHandler) {
	c.multiClientMu.Lock()
	c.pasteRefusedHandler = handler
	c.multiClientMu.Unlock()
}

// pasteRefused tells the handler a paste did not reach ptyID.
func (c *TUIClient) pasteRefused(ptyID string) {
	c.multiClientMu.RLock()
	handler := c.pasteRefusedHandler
	c.multiClientMu.RUnlock()
	if handler != nil {
		handler(ptyID)
	}
}

// writeLargeInput sends input whose frame payload is over largeFrame with a request
// id, and keeps it until pasteRecordTTL so a refusal can send it again. The
// caller holds c.mu.
func (c *TUIClient) writeLargeInput(ptyID string, data []byte, retried bool) error {
	id := c.nextReqID.Add(1)
	now := time.Now()
	c.pastesMu.Lock()
	if c.pastes == nil {
		c.pastes = make(map[uint64]*pasteRecord)
	}
	for old, rec := range c.pastes {
		if now.Sub(rec.sent) > pasteRecordTTL {
			delete(c.pastes, old)
		}
	}
	c.pastes[id] = &pasteRecord{ptyID: ptyID, data: data, retried: retried, sent: now}
	c.pastesMu.Unlock()

	payload := make([]byte, 36+len(data))
	copy(payload, ptyID)
	copy(payload[36:], data)
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err := WriteMessage(c.conn, &Message{Type: MsgInput, Payload: payload, ReqID: id})
	if err != nil {
		c.pastesMu.Lock()
		delete(c.pastes, id)
		c.pastesMu.Unlock()
	}
	return err
}

// takePasteReply takes a reply to a large input: a refusal. It reports
// whether msg was one. A busy refusal of a first send is sent again after
// pasteRetryDelay. Any other refusal is reported to OnPasteRefused.
func (c *TUIClient) takePasteReply(msg *Message) bool {
	if msg.ReqID == 0 {
		return false
	}
	c.pastesMu.Lock()
	rec := c.pastes[msg.ReqID]
	delete(c.pastes, msg.ReqID)
	c.pastesMu.Unlock()
	if rec == nil {
		return false
	}
	if msg.Type != MsgError {
		return true
	}
	var payload ErrorPayload
	_ = msg.ParsePayload(&payload)
	if payload.Code == ErrCodeBusy && !rec.retried {
		debugLog("[CLIENT] the daemon was busy with a paste of %d bytes, sending it again", len(rec.data))
		time.AfterFunc(pasteRetryDelay, func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.conn == nil {
				c.pasteRefused(rec.ptyID)
				return
			}
			if err := c.writeLargeInput(rec.ptyID, rec.data, true); err != nil {
				c.pasteRefused(rec.ptyID)
			}
		})
		return true
	}
	debugLog("[CLIENT] the daemon refused a paste of %d bytes: %s", len(rec.data), payload.Message)
	c.pasteRefused(rec.ptyID)
	return true
}
