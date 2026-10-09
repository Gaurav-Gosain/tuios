package session

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"
)

// stream-pane: one pane as a byte stream, for a client that is not tuios.
//
// The attached client gets a pane's bytes over the gob protocol, which only Go
// can read. A native phone app needs the same thing in a form any language
// can read, and it needs to resume after a dropped network without a fresh
// screen every time. stream-pane gives it that on a plain verb connection:
// one JSON reply line, and then the connection carries binary frames both
// ways, each a type byte, a 4-byte big-endian length and the payload.
//
// From the daemon:
//
//	'S' snapshot: u64 seq, u16 cols, u16 rows, then bytes that paint the pane
//	    on a fresh emulator of cols by rows (snapshotVT).
//	'O' output:   u64 seq, then the bytes the pane wrote. seq is the stream
//	    position after the frame's last byte.
//	'R' resize:   u64 seq, u16 cols, u16 rows: the pane changed size at seq.
//	'E' error:    JSON {"code","message"}: an input or lease frame was refused.
//	    The stream goes on.
//	'X' exit:     UTF-8 reason. The pane is gone, or the caller may no
//	    longer read it, and the daemon closes the connection.
//
// From the client:
//
//	'I' input:  bytes for the pane, as typed, at most maxPaneInputFrame.
//	    Checked as send-text is.
//	'L' lease:  u16 cols, u16 rows, or 0,0 to release (pane_lease.go).
//
// The stream position is the pane's outputSeq: every byte it ever wrote,
// counted from the daemon's start. A snapshot is taken from the daemon's
// emulator under the lock that also reads how far the emulator has got, so
// the seq a snapshot carries is exactly the stream it shows. A client that
// reconnects with the seq it reached and the daemon's boot id is resumed from
// the ring when the ring still holds that position, and given a snapshot
// otherwise. A client that falls behind by more than its queue holds is
// resumed the same way, so it never paints the rest of the stream over a hole.
//
// Bytes the daemon itself skips do not count as a hole: a kitty graphics
// frame replaced by a newer one before a slow client took it, or the rest of
// a graphics command a catch-up began in the middle of. The seq still moves
// past them.

// The frame types.
const (
	paneFrameSnap   = 'S'
	paneFrameOutput = 'O'
	paneFrameResize = 'R'
	paneFrameError  = 'E'
	paneFrameExit   = 'X'
	paneFrameInput  = 'I'
	paneFrameLease  = 'L'
)

const (
	// paneStreamScrollback is how many history rows a snapshot carries.
	paneStreamScrollback = 500
	// paneFrameHeader is a frame's type byte and length.
	paneFrameHeader = 5
	// maxPaneInputFrame bounds what one client frame may announce. A client
	// that sends more is refused and the stream ends, since the bytes after
	// the frame cannot be found again. It is largeFrame, the size up to which
	// a frame is not charged to the read budget (frame_budget.go), because
	// the frame is read here, outside that budget, and its buffer is made
	// before its bytes arrive. At 1 MiB, a pane with the read grant could
	// open streams that each announced a megabyte and never finished it, and
	// hold a gigabyte of the daemon's memory.
	maxPaneInputFrame = largeFrame
	// maxPaneOutputFrame bounds one output frame's payload.
	maxPaneOutputFrame = 256 << 10
	// maxLeaseDim bounds a lease, which is a terminal size.
	maxLeaseDim = 4096
)

// errPaneStreamGone ends a stream whose connection failed.
var errPaneStreamGone = errors.New("pane stream connection gone")

// paneStream is one stream-pane connection after its reply.
type paneStream struct {
	d      *Daemon
	cs     *connState
	sess   *Session
	window string
	pty    *PTY
	subID  string
	sub    *ptySubscriber
	pos    int64
	cols   int
	rows   int
	snap   *TerminalState // the snapshot to send first, nil in resume mode
	// leaseMu guards leased and closed. The client's frames are read on a
	// goroutine of their own, so a lease frame can arrive while the stream
	// is closing, and closed keeps it from setting a lease nobody releases.
	leaseMu sync.Mutex
	leased  bool
	closed  bool
	done    sync.Once
}

// verbStreamPane answers stream-pane and hands the connection to the stream.
func (d *Daemon) verbStreamPane(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string `json:"session"`
		Window    string `json:"window"`
		FromSeq   *int64 `json:"from_seq"`
		BootID    string `json:"boot_id"`
		LeaseCols int    `json:"lease_cols"`
		LeaseRows int    `json:"lease_rows"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if cs == nil {
		return nil, newVerbError(ErrVerbForbidden, "stream-pane needs a connection")
	}
	if p.Window == "" {
		return nil, invalidParam("window", "stream-pane needs window: the id or name of the pane to stream.")
	}
	if verr := checkLeaseSize(p.LeaseCols, p.LeaseRows); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	pty, err := d.resolvePTYForTarget(sess, p.Window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	state := sess.GetState()
	idx, err := findWindowStateIndex(state.Windows, p.Window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	win := state.Windows[idx]
	ps := &paneStream{d: d, cs: cs, sess: sess, window: win.ID, pty: pty, subID: cs.clientID}
	if p.LeaseCols > 0 {
		if verr := ps.admitLease(p.LeaseCols, p.LeaseRows); verr != nil {
			return nil, verr
		}
	}

	boot := d.bootID()
	mode := "snapshot"
	if p.FromSeq != nil && *p.FromSeq >= 0 && p.BootID != "" && p.BootID == boot {
		if sub, ok := pty.subscribeAt(ps.subID, *p.FromSeq, false); ok {
			mode = "resume"
			ps.setSub(sub)
			ps.pos = *p.FromSeq
			ps.cols, ps.rows = pty.sizeAt(ps.pos)
		}
	}
	if ps.sub == nil {
		if verr := ps.snapshotSubscribe(); verr != nil {
			return nil, verr
		}
	}
	if p.LeaseCols > 0 {
		if err := ps.setLease(p.LeaseCols, p.LeaseRows); err != nil {
			ps.close()
			return nil, newVerbError(ErrVerbInternal, "could not resize the pane: "+err.Error())
		}
	}

	title := pty.Title()
	if title == "" {
		title = win.CustomName
	}
	if title == "" {
		title = win.Title
	}
	LogBasic("Client %s streams pane %s (%s at %d)", cs.clientID, shortWindowID(win.ID), mode, ps.pos)
	cs.replyFailed = ps.close
	cs.takeover = func(br *bufio.Reader) { ps.run(br) }
	return map[string]any{
		"type":    "pane_stream",
		"mode":    mode,
		"seq":     ps.pos,
		"cols":    ps.cols,
		"rows":    ps.rows,
		"boot_id": boot,
		"session": sess.Name(),
		"window":  win.ID,
		"title":   title,
	}, nil
}

// bootID is this daemon start's boot id, "" when events are off.
func (d *Daemon) bootID() string {
	if d.events == nil {
		return ""
	}
	return d.events.bootIdentity()
}

// checkLeaseSize refuses a lease that is not a size: both zero, or both
// between 1 and maxLeaseDim.
func checkLeaseSize(cols, rows int) *verbError {
	if cols == 0 && rows == 0 {
		return nil
	}
	if cols < 1 || rows < 1 || cols > maxLeaseDim || rows > maxLeaseDim {
		return invalidParam("lease_cols", "lease_cols and lease_rows must both be between 1 and "+strconv.Itoa(maxLeaseDim)+", or both 0.")
	}
	return nil
}

// setSub makes sub the stream's subscription. A phone never holds the pane's
// output back for itself, as a viewer on another machine does not.
func (ps *paneStream) setSub(sub *ptySubscriber) {
	sub.noPace.Store(true)
	ps.sub = sub
}

// snapshotSubscribe takes a snapshot and subscribes from the position it was
// taken at. The two are separate steps, and output in between is in the ring,
// unless more than the ring holds came between them. Then the snapshot is
// taken again. A pane that floods that hard for every try is subscribed from
// the ring's start, and the bytes in between are the one hole the stream
// allows.
func (ps *paneStream) snapshotSubscribe() *verbError {
	for attempt := 0; ; attempt++ {
		st := ps.pty.GetTerminalState(paneStreamScrollback, 0)
		if st == nil {
			return newVerbError(ErrVerbInternal, "the pane has no terminal to stream")
		}
		sub, ok := ps.pty.subscribeAt(ps.subID, st.Seq, true)
		if !ok && attempt >= 20 {
			LogBasic("Pane %s floods faster than a snapshot is taken; the stream starts at the ring", shortWindowID(ps.window))
			sub, ok = ps.pty.subscribeSub(ps.subID, st.Seq, true), true
		}
		if ok && sub != nil {
			ps.setSub(sub)
			ps.snap = st
			ps.pos = st.Seq
			ps.cols, ps.rows = st.Width, st.Height
			return nil
		}
		if ps.pty.ctx.Err() != nil {
			return newVerbError(ErrVerbUnknownPane, "the pane closed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// close ends the stream's subscription and releases its lease. It runs once.
func (ps *paneStream) close() {
	ps.done.Do(func() {
		if ps.sub != nil {
			ps.pty.unsubscribeSub(ps.subID, ps.sub)
		}
		ps.leaseMu.Lock()
		if ps.leased {
			_ = ps.pty.SetLease(ps.subID, 0, 0)
		}
		ps.leased, ps.closed = false, true
		ps.leaseMu.Unlock()
		LogBasic("Client %s stopped streaming pane %s", ps.cs.clientID, shortWindowID(ps.window))
	})
}

// run serves the stream until the client or the pane goes.
func (ps *paneStream) run(br *bufio.Reader) {
	defer ps.close()
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		ps.readClient(br)
	}()
	if ps.snap != nil {
		if err := ps.writeSnap(); err != nil {
			return
		}
	}
	err := ps.stream(gone)
	if err == nil {
		return
	}
	var exit *paneExit
	if errors.As(err, &exit) {
		_ = ps.writeFrame(paneFrameExit, []byte(exit.reason))
		_ = ps.cs.conn.Close()
	}
}

// paneExit ends a stream because the pane is gone.
type paneExit struct{ reason string }

func (e *paneExit) Error() string { return e.reason }

// exitReason says why the pane's stream ended.
func (ps *paneStream) exitReason() *paneExit {
	if code, exited := ps.pty.ExitStatus(); exited {
		return &paneExit{reason: "exited " + strconv.Itoa(code)}
	}
	return &paneExit{reason: "closed"}
}

// stream copies the subscription to the client as frames. It returns nil
// when the client went, errPaneStreamGone when a write failed, and a
// *paneExit when the pane went.
func (ps *paneStream) stream(gone <-chan struct{}) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	quiet := 0
	var out []byte
	flush := func() error {
		if len(out) == 0 {
			return nil
		}
		err := ps.writeSeqFrame(paneFrameOutput, ps.pos, out)
		out = out[:0]
		return err
	}
	take := func(c ptyChunk) error {
		if c.isResize() {
			if err := flush(); err != nil {
				return err
			}
			if c.width == ps.cols && c.height == ps.rows {
				return nil
			}
			ps.cols, ps.rows = c.width, c.height
			return ps.writeResize()
		}
		data := takeChunk(nil, c, ps.sub)
		ps.pty.wakePacer()
		end := c.end
		if c.frame != nil {
			end = c.frame.end
		}
		if end == 0 {
			end = ps.pos + int64(len(data))
		}
		if end <= ps.pos {
			return nil // already sent
		}
		if start := end - int64(len(data)); start < ps.pos {
			data = data[ps.pos-start:]
		}
		ps.pos = end
		out = append(out, data...)
		if len(out) >= maxPaneOutputFrame {
			return flush()
		}
		return nil
	}

	for {
		select {
		case <-gone:
			return nil
		case <-ps.cs.done:
			return nil
		case <-ps.d.ctx.Done():
			return &paneExit{reason: "the daemon stopped"}
		case <-tick.C:
			// The caller was let in when the stream opened. A pane whose
			// read grant is taken away, or a link whose policy no longer
			// allows list, is held to that from here on: its stream ends.
			if verr := ps.recheck(); verr != nil {
				if err := flush(); err != nil {
					return err
				}
				return &paneExit{reason: "refused: " + verr.Message}
			}
			// A pane whose program exited stays open until a client closes
			// its window. With nothing more coming from it, the stream ends.
			if ps.pty.IsExited() && len(ps.sub.ch) == 0 {
				if quiet++; quiet >= 2 {
					return ps.exitReason()
				}
			}
		case c, ok := <-ps.sub.ch:
			if !ok {
				if err := flush(); err != nil {
					return err
				}
				return ps.exitReason()
			}
			quiet = 0
			if err := take(c); err != nil {
				return err
			}
		drain:
			for {
				select {
				case more, ok := <-ps.sub.ch:
					if !ok {
						if err := flush(); err != nil {
							return err
						}
						return ps.exitReason()
					}
					if err := take(more); err != nil {
						return err
					}
				default:
					break drain
				}
			}
			if err := flush(); err != nil {
				return err
			}
		}
		if ps.sub.gapped.Load() && len(ps.sub.ch) == 0 {
			if err := ps.recoverGap(); err != nil {
				return err
			}
		}
	}
}

// recheck runs the checks stream-pane passed at the start again, against
// the caller's grants and link policy as they are now.
func (ps *paneStream) recheck() *verbError {
	params, _ := json.Marshal(map[string]string{"session": ps.sess.Name(), "window": ps.window})
	_, _, verr := ps.d.admitVerb(ps.cs, "stream-pane", params)
	return verr
}

// recoverGap rebuilds a stream that fell out of its queue: from the ring when
// the ring still holds where the client got to, from a fresh snapshot
// otherwise.
func (ps *paneStream) recoverGap() error {
	ps.pty.unsubscribeSub(ps.subID, ps.sub)
	if sub, ok := ps.pty.subscribeAt(ps.subID, ps.pos, false); ok {
		ps.setSub(sub)
		return nil
	}
	if verr := ps.snapshotSubscribe(); verr != nil {
		return ps.exitReason()
	}
	return ps.writeSnap()
}

// writeSnap sends the snapshot the stream holds, and forgets it.
func (ps *paneStream) writeSnap() error {
	st := ps.snap
	ps.snap = nil
	body := snapshotVT(st)
	payload := make([]byte, 12, 12+len(body))
	binary.BigEndian.PutUint64(payload[0:8], uint64(st.Seq))
	binary.BigEndian.PutUint16(payload[8:10], uint16(st.Width))
	binary.BigEndian.PutUint16(payload[10:12], uint16(st.Height))
	payload = append(payload, body...)
	return ps.writeFrame(paneFrameSnap, payload)
}

// writeResize sends the stream's current size at its current position.
func (ps *paneStream) writeResize() error {
	var payload [12]byte
	binary.BigEndian.PutUint64(payload[0:8], uint64(ps.pos))
	binary.BigEndian.PutUint16(payload[8:10], uint16(ps.cols))
	binary.BigEndian.PutUint16(payload[10:12], uint16(ps.rows))
	return ps.writeFrame(paneFrameResize, payload[:])
}

// writeSeqFrame sends a frame whose payload is a seq and then data.
func (ps *paneStream) writeSeqFrame(typ byte, seq int64, data []byte) error {
	var head [paneFrameHeader + 8]byte
	head[0] = typ
	binary.BigEndian.PutUint32(head[1:5], uint32(8+len(data)))
	binary.BigEndian.PutUint64(head[5:13], uint64(seq))
	return ps.write(head[:], data)
}

// writeFrame sends one frame.
func (ps *paneStream) writeFrame(typ byte, payload []byte) error {
	var head [paneFrameHeader]byte
	head[0] = typ
	binary.BigEndian.PutUint32(head[1:5], uint32(len(payload)))
	return ps.write(head[:], payload)
}

// write writes the parts of one frame under the connection's send lock. A
// failed write leaves a partial frame on the wire, so the connection is
// dropped.
func (ps *paneStream) write(parts ...[]byte) error {
	cs := ps.cs
	cs.sendMu.Lock()
	defer cs.sendMu.Unlock()
	_ = cs.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		if _, err := cs.conn.Write(p); err != nil {
			cs.drop()
			return errPaneStreamGone
		}
	}
	return nil
}

// writeError tells the client a frame it sent was refused.
func (ps *paneStream) writeError(verr *verbError) {
	body, _ := json.Marshal(map[string]string{"code": verr.Code, "message": verr.Message})
	_ = ps.writeFrame(paneFrameError, body)
}

// readClient reads the client's frames until the connection ends.
func (ps *paneStream) readClient(br *bufio.Reader) {
	var head [paneFrameHeader]byte
	for {
		if _, err := io.ReadFull(br, head[:]); err != nil {
			return
		}
		n := binary.BigEndian.Uint32(head[1:5])
		if n > maxPaneInputFrame {
			ps.writeError(newVerbError(ErrVerbInvalidRequest, fmt.Sprintf("a frame of %d bytes is larger than the limit of %d bytes. The stream ends. Send a longer paste as more than one I frame", n, maxPaneInputFrame)))
			_ = ps.cs.conn.Close()
			return
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(br, payload); err != nil {
			return
		}
		switch head[0] {
		case paneFrameInput:
			if verr := ps.input(payload); verr != nil {
				ps.writeError(verr)
			}
		case paneFrameLease:
			if verr := ps.lease(payload); verr != nil {
				ps.writeError(verr)
			}
		default:
			ps.writeError(newVerbError(ErrVerbInvalidRequest, fmt.Sprintf("frame type 0x%02x is not one a client sends; it was skipped", head[0])))
		}
	}
}

// input types bytes into the pane, after every check send-text passes.
func (ps *paneStream) input(data []byte) *verbError {
	if len(data) == 0 {
		return nil
	}
	params, _ := json.Marshal(map[string]string{"session": ps.sess.Name(), "window": ps.window})
	if _, _, verr := ps.d.admitVerb(ps.cs, "send-text", params); verr != nil {
		return verr
	}
	if verr := ps.d.recheckTyping(ps.cs, "send-text", ps.sess, ps.window); verr != nil {
		return verr
	}
	if _, err := ps.pty.Write(data); err != nil {
		return ptyWriteError(err)
	}
	ps.cs.lastInput.Store(time.Now().UnixNano())
	return nil
}

// lease reads an 'L' frame and sets or releases the stream's lease.
func (ps *paneStream) lease(payload []byte) *verbError {
	if len(payload) != 4 {
		return newVerbError(ErrVerbInvalidRequest, "a lease frame is 4 bytes: cols and rows")
	}
	cols := int(binary.BigEndian.Uint16(payload[0:2]))
	rows := int(binary.BigEndian.Uint16(payload[2:4]))
	if verr := checkLeaseSize(cols, rows); verr != nil {
		return verr
	}
	if cols > 0 {
		if verr := ps.admitLease(cols, rows); verr != nil {
			return verr
		}
	}
	if err := ps.setLease(cols, rows); err != nil {
		return newVerbError(ErrVerbInternal, "could not resize the pane: "+err.Error())
	}
	return nil
}

// admitLease runs the checks a resize of the pane passes: a lease changes
// the pane's size for every client, so it needs what resize needs.
func (ps *paneStream) admitLease(cols, rows int) *verbError {
	params, _ := json.Marshal(map[string]any{"session": ps.sess.Name(), "window": ps.window, "width": cols, "height": rows})
	_, _, verr := ps.d.admitVerb(ps.cs, "resize", params)
	return verr
}

// setLease holds the pane at most at cols by rows, or releases it on 0,0.
func (ps *paneStream) setLease(cols, rows int) error {
	ps.leaseMu.Lock()
	defer ps.leaseMu.Unlock()
	if ps.closed {
		return nil
	}
	ps.leased = cols > 0
	return ps.pty.SetLease(ps.subID, cols, rows)
}

// subscribeAt subscribes clientID from exactly fromSeq. It subscribes
// nothing and reports false when the ring no longer holds the bytes from
// fromSeq on, when fromSeq is past what the pane has written, or when the
// pane is closing.
func (p *PTY) subscribeAt(clientID string, fromSeq int64, fromSnapshot bool) (*ptySubscriber, bool) {
	p.subscribersMu.Lock()
	defer p.subscribersMu.Unlock()
	// Close cancels the context before it closes the subscriptions, under
	// this lock, so a subscription made after that would never be closed.
	if p.ctx.Err() != nil {
		return nil, false
	}
	if _, ok := p.subscribers[clientID]; ok {
		return nil, false
	}
	p.outputMu.RLock()
	ahead := fromSeq > p.outputSeq
	p.outputMu.RUnlock()
	if ahead {
		return nil, false
	}
	p.subscribeLocked(clientID, fromSeq, fromSnapshot)
	sub := p.subscribers[clientID]
	if sub.missed {
		close(sub.ch)
		delete(p.subscribers, clientID)
		p.subscriberCount.Store(int32(len(p.subscribers)))
		return nil, false
	}
	return sub, true
}

// sizeAt is the size the pane's stream had at position seq: the newest resize
// mark at or before it, or the size the pane was made at. The ring keeps the
// newest mark behind it, so any seq the ring still holds is answered.
func (p *PTY) sizeAt(seq int64) (int, int) {
	p.outputMu.RLock()
	defer p.outputMu.RUnlock()
	for i := len(p.resizeMarks) - 1; i >= 0; i-- {
		if m := p.resizeMarks[i]; m.seq <= seq {
			return m.width, m.height
		}
	}
	return p.spawnW, p.spawnH
}
