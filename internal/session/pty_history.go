package session

import (
	"fmt"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"tuios.dev/vt"
	"tuios.dev/vt/snapshot"
)

// A client can keep less history of a pane than its daemon does. It reads
// the older lines from the daemon by their number (vt.Terminal.ScrollbackBase),
// a page at a time. Both emulators are fed the same output, so they give each
// line the same number, and a snapshot carries the numbering to a client that
// attaches.

// maxHistoryPage bounds the lines one MsgHistoryPage carries.
const maxHistoryPage = 1024

// ReadHistoryPayload asks for up to Max history lines of a pane that come
// before line number Before.
type ReadHistoryPayload struct {
	PTYID  string `json:"pty_id"`
	Before int64  `json:"before"`
	Max    int    `json:"max"`
}

// HistoryPagePayload answers MsgReadHistory. Page is empty when the daemon
// no longer holds the lines, and nil when its emulator does not number its
// history.
type HistoryPagePayload struct {
	PTYID string                `json:"pty_id"`
	Page  *snapshot.HistoryPage `json:"page,omitempty"`
}

// numbersHistory reports whether this build's emulator numbers its history:
// the pure emulator does, the ghostty backend does not.
var numbersHistory = sync.OnceValue(func() bool {
	_, ok := vt.NewWithScrollback(1, 1, 1).ScrollbackBase()
	return ok
})

// HistoryPage reads up to max history lines before line number before. The
// lines are copied under the emulator's lock and packed after it is let go.
func (p *PTY) HistoryPage(before int64, max int) *snapshot.HistoryPage {
	p.terminalMu.RLock()
	if p.terminal == nil {
		p.terminalMu.RUnlock()
		return nil
	}
	page, finish := snapshot.CaptureHistoryPage(p.terminal, before, max)
	p.terminalMu.RUnlock()
	finish()
	return page
}

func (d *Daemon) handleReadHistory(cs *connState, msg *Message) error {
	var payload ReadHistoryPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return errfmt.Errorf("invalid read history payload: %w", err)
	}
	// As for a terminal state: only a pane of the session this client is
	// attached to.
	if cs.sessionID == "" {
		return d.replyError(cs, msg, ErrCodeNotAttached, "not attached to any session")
	}
	session := d.manager.GetSessionByID(cs.sessionID)
	if session == nil {
		return d.replyError(cs, msg, ErrCodeSessionNotFound, "session not found")
	}
	pty := session.GetPTY(payload.PTYID)
	if pty == nil {
		return d.replyError(cs, msg, ErrCodePTYNotFound, fmt.Sprintf("PTY %s not found", payload.PTYID))
	}
	return d.reply(cs, msg, MsgHistoryPage, &HistoryPagePayload{
		PTYID: payload.PTYID,
		Page:  pty.HistoryPage(payload.Before, min(max(payload.Max, 0), maxHistoryPage)),
	})
}

// ReadHistory reads up to max history lines of a pane before line number
// before from the daemon. It needs FeatureRemoteHistory on both sides. The
// page is nil when the daemon does not number the pane's history.
func (c *TUIClient) ReadHistory(ptyID string, before int64, max int) (*snapshot.HistoryPage, error) {
	if !c.features.Has(FeatureRemoteHistory) {
		return nil, errfmt.Errorf("the daemon does not serve history")
	}
	msg, err := NewMessage(MsgReadHistory, &ReadHistoryPayload{PTYID: ptyID, Before: before, Max: max})
	if err != nil {
		return nil, err
	}
	var page *HistoryPagePayload
	forThisPane := func(m *Message) bool {
		if m.Type != MsgHistoryPage {
			return true
		}
		var p HistoryPagePayload
		if err := m.ParsePayload(&p); err != nil {
			return true
		}
		if p.PTYID != ptyID {
			return false
		}
		page = &p
		return true
	}
	resp, err := c.sendAndWaitMatching(msg, forThisPane, MsgHistoryPage, MsgError)
	if err != nil {
		return nil, err
	}
	if resp.Type == MsgError {
		var e ErrorPayload
		_ = resp.ParsePayload(&e)
		return nil, refusalOf(e)
	}
	if page == nil {
		return nil, errfmt.Errorf("read history: no page in the reply")
	}
	return page.Page, nil
}
