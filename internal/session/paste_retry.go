package session

import (
	"errors"
	"log"
	"net"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

const (
	pasteRetryDelay = 500 * time.Millisecond

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

type pasteRecord struct {
	ptyID   string
	data    []byte
	retried bool
	code    int
}

type inputHold struct {
	queue [][]byte
}

// OnPasteRefused sets the handler for a paste that did not reach its pane.
func (c *TUIClient) OnPasteRefused(handler PasteRefusedHandler) {
	c.multiClientMu.Lock()
	c.pasteRefusedHandler = handler
	c.multiClientMu.Unlock()
}

func (c *TUIClient) pasteRefused(ptyID, message string) {
	c.multiClientMu.RLock()
	handler := c.pasteRefusedHandler
	c.multiClientMu.RUnlock()
	if handler != nil {
		handler(ptyID, message)
	}
}

func refusalMessage(code int) string {
	switch code {
	case ErrCodeForbidden:
		return PasteRefusedForbidden
	case ErrCodeInvalidMessage:
		return PasteRefusedTooLarge
	case ErrCodeBusy:
		return PasteRefusedBusy
	}
	return PasteRefusedOther
}

func (c *TUIClient) isLargeInput(n int) bool {
	return 36+n > largeFrame
}

func (c *TUIClient) writeInputLocked(ptyID string, data []byte) error {
	if hold := c.inputHolds[ptyID]; hold != nil {
		if err := c.connLost(); err != nil {
			delete(c.inputHolds, ptyID)
			return err
		}
		hold.queue = append(hold.queue, append([]byte(nil), data...))
		return nil
	}
	if c.isLargeInput(len(data)) {
		return c.sendPasteLocked(ptyID, append([]byte(nil), data...), false)
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return WritePTYInput(c.conn, ptyID, data)
}

func (c *TUIClient) connLost() error {
	select {
	case <-c.done:
	default:
		return nil
	}
	if err := c.lostErr.Load(); err != nil {
		return errfmt.Errorf("the connection to the daemon was lost: %w", *err)
	}
	return net.ErrClosed
}

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
		return err
	}
	time.AfterFunc(pasteHoldTimeout, func() { c.pasteHoldExpired(id, ping, rec) })
	return nil
}

var pasteHoldTimeout = 30 * time.Second

func (c *TUIClient) pasteHoldExpired(id, ping uint64, rec *pasteRecord) {
	c.pastesMu.Lock()
	_, waiting := c.pasteBarriers[ping]
	delete(c.pastes, id)
	delete(c.pasteBarriers, ping)
	c.pastesMu.Unlock()
	if !waiting {
		return
	}
	log.Printf("The daemon did not answer a paste of %d bytes within %v. The input held behind it is sent now.", len(rec.data), pasteHoldTimeout)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.releaseHoldLocked(rec.ptyID)
}

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
