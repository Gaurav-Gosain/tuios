package session

import (
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/testknob"
	"tuios.dev/vt/snapshot"
)

// A pane whose output outruns what a client can show is sent to that client
// as frames of its screen, not as bytes. The daemon's emulator has parsed the
// bytes already, so a frame costs the daemon a capture and a pack, and the
// client applies it instead of parsing every byte a second time. Under a
// flood the client parsed bytes it never showed: a busy pane cost it as much
// CPU as it cost the daemon, and the panes it did show waited behind them.
//
// A stream turns to frames when it sends more than frameEnterBytes inside
// frameRateWindow, or when the client reports that it is behind on the pane
// (MsgPTYLag). It then takes no more bytes. Every frameInterval it sends a
// frame of the screen, when the screen moved, with the stream position the
// frame ends at. Once the pane has made less than frameExitBytes in each of
// frameQuietTurns turns, the stream is rebuilt from the last frame's position,
// as a client that restored a snapshot subscribes, and bytes flow again.
//
// A frame carries no history. The lines that scrolled off while the pane was
// sent as frames are on the daemon only, and the client reads them by number
// (FeatureRemoteHistory). So a frame is sent only to a client that listed
// both features, and only for a pane it asked frames for: a client that
// records the stream needs every byte.
//
// What the client would have done with the bytes it never parses is lost for
// them: a bell, a notification, a clipboard write, a picture. A pane that
// drew a picture lately is not sent as frames, and a picture ends them.

// PTYFramePayload is one frame of a pane: its state without history, and the
// stream position it ends at (State.Seq).
type PTYFramePayload struct {
	PTYID string         `json:"pty_id"`
	State *TerminalState `json:"state"`
}

// PTYLagPayload is a client's report that it is behind on a pane's output.
type PTYLagPayload struct {
	PTYID string `json:"pty_id"`
}

// The frame rules. Variables so a test can change them.
var (
	frameInterval   = time.Second / 120
	frameRateWindow = 50 * time.Millisecond
	frameExitBytes  = int64(32 << 10)
	frameQuietTurns = 3
	// frameQueueBytes is how much a stream may hold for a client that does
	// not read it before it turns to frames.
	frameQueueBytes = int64(1 << 20)
	// frameGraphicsQuiet is how long after a picture a pane is not sent as
	// frames.
	frameGraphicsQuiet = 2 * time.Second
)

// frameEnterBytes is how much a stream sends inside frameRateWindow before it
// turns to frames: 512 KiB, about 10 MB a second, or the pty_frame_enter_kb
// knob.
func frameEnterBytes() int {
	if kb, err := strconv.Atoi(testknob.String("pty_frame_enter_kb")); err == nil && kb > 0 {
		return kb << 10
	}
	return 512 << 10
}

// floodMeter counts what a stream sent in the current frameRateWindow.
type floodMeter struct {
	start time.Time
	bytes int
}

// add counts n bytes sent at now, and reports whether the window holds more
// than frameEnterBytes.
func (f *floodMeter) add(n int, now time.Time) bool {
	if now.Sub(f.start) >= frameRateWindow {
		f.start, f.bytes = now, 0
	}
	f.bytes += n
	return f.bytes >= frameEnterBytes()
}

// framesWanted reports whether the stream sub should turn to frames now.
// flooding is what the stream's floodMeter said for the last batch.
func (p *PTY) framesWanted(sub *ptySubscriber, flooding bool) bool {
	lagged := sub.lagged.Swap(false)
	if !flooding && !lagged && sub.queued.Load() < frameQueueBytes {
		return false
	}
	return !p.drewPictureLately()
}

// drewPictureLately reports whether the pane wrote a kitty graphics command
// in the last frameGraphicsQuiet.
func (p *PTY) drewPictureLately() bool {
	at := p.graphicsAt.Load()
	return at != 0 && time.Since(time.Unix(0, at)) <= frameGraphicsQuiet
}

// frameState is the pane's screen without history, and the stream position
// it ends at. open says the guest has a synchronized update open. It is nil
// when the pane has no emulator.
func (p *PTY) frameState() (state *TerminalState, open bool) {
	p.terminalMu.RLock()
	if p.terminal == nil {
		p.terminalMu.RUnlock()
		return nil, false
	}
	open = p.terminal.IsSyncActive()
	state, finish := snapshot.CapturePacked(p.terminal, p.terminal.Width(), p.terminal.Height(), -1, 0)
	state.Seq = p.vtSeq
	p.terminalMu.RUnlock()
	finish()
	return state, open
}

// streamFrames sends frames of the pane for the stream sub until the pane is
// quiet. It returns the byte stream rebuilt from the last frame, or nil when
// the client let go of sub or the connection ended.
func (d *Daemon) streamFrames(cs *connState, pty *PTY, sub *ptySubscriber) *ptySubscriber {
	// A gapped stream takes no more bytes from broadcast and holds no pane
	// back. What it holds is older than the first frame.
	sub.gapped.Store(true)
	if dropQueued(sub) {
		return nil
	}
	pty.wakePacer()
	pty.noteFramed()

	tick := time.NewTicker(frameInterval)
	defer tick.Stop()
	last := int64(-1)
	var sent *TerminalState
	quiet, held := 0, 0
	for {
		state, open := pty.frameState()
		if state == nil {
			return nil
		}
		// A frame inside a synchronized update would show it half drawn. It
		// waits for the update to end, up to two turns.
		if open && held < 2 {
			held++
		} else {
			held = 0
			made := state.Seq - last
			if resume, done := d.framePassedGraphics(cs, pty, sub, state, sent, last); done {
				if resume < 0 {
					return nil
				}
				last = resume
				break
			}
			if last < 0 || made > 0 {
				if err := d.sendFrame(cs, pty.ID, state); err != nil {
					return nil
				}
				sub.sent.Store(state.Seq)
				sent = state
			}
			if last >= 0 && made < frameExitBytes {
				quiet++
			} else {
				quiet = 0
			}
			last = state.Seq
			// A picture goes to the client as bytes, which the stream
			// rebuilt from last carries.
			if quiet >= frameQuietTurns || pty.drewPictureLately() {
				break
			}
		}
		select {
		case <-cs.done:
			return nil
		case <-d.ctx.Done():
			return nil
		case <-tick.C:
		}
		if dropQueued(sub) {
			return nil
		}
	}
	debugLog("[DEBUG] PTY %s: client %s gets bytes from %d", pty.ID[:8], cs.clientID, last)
	return pty.resumeFromFrame(cs.clientID, sub, last)
}

// framePassedGraphics ends the frames of sub when the emulator has parsed a
// kitty graphics command the client has not got: one after the last frame,
// or one the last frame was taken inside. state is the next frame, sent the
// last one, and last its position (-1 before the first).
//
// Only the client moves the cursor past an image it places, and only the
// client draws it. The daemon's emulator does neither, so a frame taken after
// the command holds neither, and a stream rebuilt there never gives the
// client the command. The stream is rebuilt where the command starts. The
// daemon's screen there is the screen of state when only graphics commands
// come between, and the screen of sent when the command started inside it.
// Otherwise it starts at last, and its bytes carry the command.
//
// It returns where the stream is rebuilt, -1 when the connection ended, and
// whether the frames end.
func (d *Daemon) framePassedGraphics(cs *connState, pty *PTY, sub *ptySubscriber, state, sent *TerminalState, last int64) (int64, bool) {
	if pty.graphicsAt.Load() == 0 {
		return 0, false
	}
	lo := last
	if lo < 0 {
		lo = sub.sent.Load()
	}
	from, found, only := pty.graphicsSince(lo, state.Seq)
	if !found {
		return 0, false
	}
	frame, resume := (*TerminalState)(nil), lo
	switch {
	case only:
		frame, resume = state, from
	case from < lo && sent != nil:
		frame, resume = sent, from
	}
	if frame != nil {
		frame.Seq = resume
		frame.PendingInput = nil
		if err := d.sendFrame(cs, pty.ID, frame); err != nil {
			return -1, true
		}
		sub.sent.Store(resume)
	}
	return resume, true
}

// graphicsSince is gfxScanner.spanSince for the pane's stream.
func (p *PTY) graphicsSince(lo, hi int64) (from int64, found, only bool) {
	// broadcast changes gfx under the read lock, one at a time under
	// streamMu, so reading it takes the write lock, as subscribeLocked does.
	p.subscribersMu.Lock()
	defer p.subscribersMu.Unlock()
	return p.gfx.spanSince(lo, hi)
}

// noteFramed counts a stream turned to frames, and says so in the daemon log
// at most once every pacingReportEvery.
func (p *PTY) noteFramed() {
	n := p.framed.Add(1)
	now := time.Now().UnixNano()
	last := p.framedLogAt.Load()
	if now-last < int64(pacingReportEvery) || !p.framedLogAt.CompareAndSwap(last, now) {
		return
	}
	LogBasic("PTY %s output: %d client streams sent as frames so far", shortID(p.ID), n)
}

// dropQueued drops what the stream sub holds, and reports whether its queue
// was closed: the client let go of the stream.
func dropQueued(sub *ptySubscriber) bool {
	for {
		c, ok, closed := sub.out.pop()
		if closed {
			return true
		}
		if !ok {
			return false
		}
		if c.frame != nil {
			c.frame.take()
		} else {
			sub.queued.Add(-int64(len(c.data)))
		}
	}
}

// sendFrame writes one frame to the client. It is a large write, so it takes
// sendMu as one.
func (d *Daemon) sendFrame(cs *connState, ptyID string, state *TerminalState) error {
	msg, err := NewMessage(MsgPTYFrame, &PTYFramePayload{PTYID: ptyID, State: state})
	if err != nil {
		return err
	}
	cs.sendMu.Lock()
	_ = cs.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err = WriteMessage(cs.conn, msg)
	cs.sendMu.Unlock()
	if err != nil {
		cs.drop()
	}
	return err
}

// resumeFromFrame rebuilds the stream cur from stream position seq, which the
// client's emulator holds by a frame. It returns nil when cur is no longer
// the client's stream.
func (p *PTY) resumeFromFrame(clientID string, cur *ptySubscriber, seq int64) *ptySubscriber {
	p.subscribersMu.Lock()
	defer p.subscribersMu.Unlock()
	if sub, ok := p.subscribers[clientID]; !ok || sub != cur {
		return nil
	}
	cur.out.close()
	delete(p.subscribers, clientID)
	p.subscribeLocked(clientID, seq, true)
	next := p.subscribers[clientID]
	next.noPace.Store(cur.noPace.Load())
	next.frames.Store(cur.frames.Load())
	p.wakePacer()
	return next
}

// handlePTYLag marks the client's stream of a pane as behind, so it turns to
// frames after its next batch.
func (d *Daemon) handlePTYLag(cs *connState, msg *Message) error {
	var payload PTYLagPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return errfmt.Errorf("invalid pty lag payload: %w", err)
	}
	cs.mu.Lock()
	sub := cs.ptySubscriptions[payload.PTYID]
	cs.mu.Unlock()
	if sub != nil && sub.frames.Load() {
		sub.lagged.Store(true)
	}
	return nil
}

// OnPTYFrame registers a handler for the frames of a pane. It is called on
// the read loop, in order with the pane's output. A pane subscribed after it
// is registered is sent as frames when it floods, if the daemon can.
func (c *TUIClient) OnPTYFrame(ptyID string, handler func(*TerminalState)) {
	c.ptyFrameHandlersMu.Lock()
	if handler == nil {
		delete(c.ptyFrameHandlers, ptyID)
	} else {
		c.ptyFrameHandlers[ptyID] = handler
	}
	c.ptyFrameHandlersMu.Unlock()
}

// framesFor reports whether the client takes frames for ptyID.
func (c *TUIClient) framesFor(ptyID string) bool {
	if !c.features.Has(FeaturePTYFrames) || !c.features.Has(FeatureRemoteHistory) {
		return false
	}
	c.ptyFrameHandlersMu.RLock()
	defer c.ptyFrameHandlersMu.RUnlock()
	return c.ptyFrameHandlers[ptyID] != nil
}

// ReportPTYLag tells the daemon that the client is behind on a pane, off the
// caller's goroutine.
func (c *TUIClient) ReportPTYLag(ptyID string) {
	msg, err := NewMessage(MsgPTYLag, &PTYLagPayload{PTYID: ptyID})
	if err != nil {
		return
	}
	go func() { _ = c.send(msg) }()
}

// handlePTYFrame hands a frame to its pane's handler.
func (c *TUIClient) handlePTYFrame(msg *Message) {
	var payload PTYFramePayload
	if err := msg.ParsePayload(&payload); err != nil || payload.State == nil {
		return
	}
	c.ptyFrameHandlersMu.RLock()
	handler := c.ptyFrameHandlers[payload.PTYID]
	c.ptyFrameHandlersMu.RUnlock()
	if handler != nil {
		handler(payload.State)
	}
}
