package session

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

const (
	paneFrameSnap    = 'S'
	paneFrameOutput  = 'O'
	paneFrameResize  = 'R'
	paneFrameError   = 'E'
	paneFrameExit    = 'X'
	paneFrameInput   = 'I'
	paneFrameLease   = 'L'
	paneFrameInputAt = 'J'
)

const (
	paneStreamScrollback = 500
	paneFrameHeader      = 5
	maxPaneInputFrame    = largeFrame
	maxPaneOutputFrame   = 256 << 10
	maxLeaseDim          = 4096
	paneInputBytes       = 4 * maxPaneInputFrame
	paneInputFrames      = 4096
	snapCellBytes        = 160
)

var paneLeaseTTL = 30 * time.Second

var errPaneStreamGone = errors.New("pane stream connection gone")

type paneStream struct {
	d            *Daemon
	cs           *connState
	sess         *Session
	window       string
	pty          *PTY
	subID        string
	sub          *ptySubscriber
	pos          int64
	cols         int
	rows         int
	snap         *TerminalState
	fit          bool
	viewer       bool
	viewerReport ViewerReport
	viewerSess   *Session
	leaseMu      sync.Mutex
	leased       bool
	closed       bool
	leaseAt      int64
	done         sync.Once
	admitMu      sync.Mutex
	in           paneInput
	quit         chan struct{}
	snapRelease  func()
}

func (d *Daemon) verbStreamPane(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string        `json:"session"`
		Window    string        `json:"window"`
		FromSeq   *int64        `json:"from_seq"`
		BootID    string        `json:"boot_id"`
		LeaseCols int           `json:"lease_cols"`
		LeaseRows int           `json:"lease_rows"`
		SessionID string        `json:"session_id"`
		Fit       bool          `json:"fit"`
		Viewer    *ViewerReport `json:"viewer"`
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
	var sess *Session
	if p.SessionID != "" {
		if sess = d.manager.GetSessionByID(p.SessionID); sess == nil {
			return nil, newVerbError(ErrVerbSessionNotFound, "no session has id "+p.SessionID)
		}
	} else {
		var verr *verbError
		if sess, verr = d.resolveVerbSession(cs, p.Session); verr != nil {
			return nil, verr
		}
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
	ps := &paneStream{d: d, cs: cs, sess: sess, window: win.ID, pty: pty, subID: cs.clientID,
		fit: p.Fit, quit: make(chan struct{})}
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

	if p.Viewer != nil {
		ps.viewer = true
		ps.viewerReport, ps.viewerSess = *p.Viewer, sess
		sess.addViewer(ps.subID, *p.Viewer)
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
		"type":          "pane_stream",
		"mode":          mode,
		"seq":           ps.pos,
		"cols":          ps.cols,
		"rows":          ps.rows,
		"boot_id":       boot,
		"input_offsets": true,
		"session":       sess.Name(),
		"session_id":    sess.ID,
		"window":        win.ID,
		"title":         title,
	}, nil
}

func (d *Daemon) bootID() string {
	if d.events == nil {
		return ""
	}
	return d.events.bootIdentity()
}

func checkLeaseSize(cols, rows int) *verbError {
	if cols == 0 && rows == 0 {
		return nil
	}
	if cols < 1 || rows < 1 || cols > maxLeaseDim || rows > maxLeaseDim {
		return invalidParam("lease_cols", "lease_cols and lease_rows must both be between 1 and "+strconv.Itoa(maxLeaseDim)+", or both 0.")
	}
	return nil
}

func (ps *paneStream) setSub(sub *ptySubscriber) {
	sub.noPace.Store(true)
	ps.sub = sub
}

func (ps *paneStream) snapshotSubscribe() *verbError {
	w, h := ps.pty.Size()
	release, ok := ps.d.readBudgetFor(ps.cs).acquire(int64(paneStreamScrollback+h)*int64(w)*snapCellBytes, readBudgetWait)
	if !ok {
		return newVerbError(ErrVerbBusy, "the daemon has no memory free for a snapshot of the pane now. Nothing was sent. Try again in a few seconds")
	}
	for attempt := 0; ; attempt++ {
		st := ps.pty.GetTerminalState(paneStreamScrollback, 0)
		if st == nil {
			release()
			return newVerbError(ErrVerbInternal, "the pane has no terminal to stream")
		}
		sub, ok := ps.pty.subscribeAt(ps.subID, st.Seq, true)
		if !ok && attempt >= 20 {
			LogBasic("Pane %s floods faster than a snapshot is taken; the stream starts at the ring", shortWindowID(ps.window))
			sub, ok = ps.pty.subscribeSub(ps.subID, st.Seq, true), true
		}
		if ok && sub != nil {
			ps.setSub(sub)
			ps.snap, ps.snapRelease = st, release
			ps.pos = st.Seq
			ps.cols, ps.rows = st.Width, st.Height
			return nil
		}
		if ps.pty.ctx.Err() != nil {
			release()
			return newVerbError(ErrVerbUnknownPane, "the pane closed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

const linkLeaseLinger = 15 * time.Second

func (ps *paneStream) close() {
	ps.done.Do(func() {
		if ps.sub != nil {
			ps.pty.unsubscribeSub(ps.subID, ps.sub)
		}
		ps.leaseMu.Lock()
		if ps.leased && ps.cs.viaLink && !ps.cs.linkClosedOnPurposeNow() {
			pty, holder := ps.pty, ps.subID
			time.AfterFunc(linkLeaseLinger, func() { _ = pty.SetLease(holder, 0, 0) })
		} else if ps.leased {
			_ = ps.pty.SetLease(ps.subID, 0, 0)
		}
		if ps.viewer && ps.viewerSess != nil {
			ps.viewerSess.removeViewer(ps.subID)
		}
		ps.leased, ps.closed = false, true
		ps.leaseMu.Unlock()
		close(ps.quit)
		ps.in.close()
		ps.dropSnap()
		LogBasic("Client %s stopped streaming pane %s", ps.cs.clientID, shortWindowID(ps.window))
	})
}

func (ps *paneStream) run(br *bufio.Reader) {
	defer ps.close()
	go ps.writeInput()
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
	if exit, ok := errors.AsType[*paneExit](err); ok {
		_ = ps.writeFrame(paneFrameExit, []byte(exit.reason))
		_ = ps.cs.conn.Close()
	}
}

type paneExit struct{ reason string }

func (e *paneExit) Error() string { return e.reason }

func (ps *paneStream) exitReason() *paneExit {
	if ps.pty.ctx.Err() != nil {
		return &paneExit{reason: "closed"}
	}
	if code, exited := ps.pty.ExitStatus(); exited {
		if sig := ps.pty.ExitSignal(); sig > 0 {
			return &paneExit{reason: "killed " + strconv.Itoa(sig)}
		}
		return &paneExit{reason: "exited " + strconv.Itoa(code)}
	}
	return &paneExit{reason: "closed"}
}

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
			return nil
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
			if verr := ps.recheck(); verr != nil {
				if err := flush(); err != nil {
					return err
				}
				return &paneExit{reason: "refused: " + verr.Message}
			}
			ps.expireLease()
			ps.sub.out.trim()
			if ps.pty.IsExited() && ps.sub.out.len() == 0 {
				if quiet++; quiet >= 2 {
					return ps.exitReason()
				}
			}
		case <-ps.sub.out.wake:
			for {
				c, ok, closed := ps.sub.out.pop()
				if closed {
					if err := flush(); err != nil {
						return err
					}
					return ps.exitReason()
				}
				if !ok {
					break
				}
				quiet = 0
				if err := take(c); err != nil {
					return err
				}
			}
			if err := flush(); err != nil {
				return err
			}
		}
		if ps.sub.gapped.Load() && ps.sub.out.len() == 0 {
			if err := ps.recoverGap(); err != nil {
				return err
			}
		}
	}
}

func (ps *paneStream) recheck() *verbError {
	ps.admitMu.Lock()
	defer ps.admitMu.Unlock()
	params, _ := jsonx.Marshal(map[string]string{"session": ps.home().Name(), "window": ps.window})
	_, _, verr := ps.d.admitVerb(ps.cs, "stream-pane", params)
	return verr
}

func (ps *paneStream) recoverGap() error {
	ps.pty.unsubscribeSub(ps.subID, ps.sub)
	if sub, ok := ps.pty.subscribeAt(ps.subID, ps.pos, false); ok {
		ps.setSub(sub)
		return nil
	}
	if verr := ps.snapshotSubscribe(); verr != nil {
		if verr.Code == ErrVerbBusy {
			return &paneExit{reason: "busy: " + verr.Message}
		}
		return ps.exitReason()
	}
	return ps.writeSnap()
}

func (ps *paneStream) writeSnap() error {
	st := ps.snap
	defer ps.dropSnap()
	body := SnapshotVT(st)
	payload := make([]byte, 12, 12+len(body))
	binary.BigEndian.PutUint64(payload[0:8], uint64(st.Seq))
	binary.BigEndian.PutUint16(payload[8:10], uint16(st.Width))
	binary.BigEndian.PutUint16(payload[10:12], uint16(st.Height))
	payload = append(payload, body...)
	return ps.writeFrame(paneFrameSnap, payload)
}

func (ps *paneStream) dropSnap() {
	ps.snap = nil
	if ps.snapRelease != nil {
		ps.snapRelease()
		ps.snapRelease = nil
	}
}

func (ps *paneStream) writeResize() error {
	var payload [12]byte
	binary.BigEndian.PutUint64(payload[0:8], uint64(ps.pos))
	binary.BigEndian.PutUint16(payload[8:10], uint16(ps.cols))
	binary.BigEndian.PutUint16(payload[10:12], uint16(ps.rows))
	return ps.writeFrame(paneFrameResize, payload[:])
}

func (ps *paneStream) writeSeqFrame(typ byte, seq int64, data []byte) error {
	var head [paneFrameHeader + 8]byte
	head[0] = typ
	binary.BigEndian.PutUint32(head[1:5], uint32(8+len(data)))
	binary.BigEndian.PutUint64(head[5:13], uint64(seq))
	return ps.write(head[:], data)
}

func (ps *paneStream) writeFrame(typ byte, payload []byte) error {
	var head [paneFrameHeader]byte
	head[0] = typ
	binary.BigEndian.PutUint32(head[1:5], uint32(len(payload)))
	return ps.write(head[:], payload)
}

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

func (ps *paneStream) writeError(verr *verbError) {
	body, _ := jsonx.Marshal(map[string]string{"code": verr.Code, "message": verr.Message})
	_ = ps.writeFrame(paneFrameError, body)
}

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
		case paneFrameInput, paneFrameInputAt:
			at, data, ok := inputFrame(head[0], payload)
			if !ok {
				ps.writeError(newVerbError(ErrVerbInvalidRequest, "a J frame starts with its 8-byte stream offset; this one is shorter. It was skipped"))
				continue
			}
			if verr := ps.queueInput(at, data); verr != nil {
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

func (ps *paneStream) queueInput(at int64, data []byte) *verbError {
	if len(data) == 0 {
		return nil
	}
	if verr := ps.admitInput(); verr != nil {
		LogBasic("Stream of pane %s refused an input frame: %s", shortWindowID(ps.window), verr.Message)
		return verr
	}
	accepted, waiting, first, ok := ps.in.push(at, data)
	if ok {
		return nil
	}
	if first {
		LogBasic("Stream of pane %s refuses input: the pane has not read %d bytes. The client is told to send again from byte %d", shortWindowID(ps.window), waiting, accepted)
	}
	msg := fmt.Sprintf("the pane has not read the last %d bytes of input. This frame and every frame after it until the pane reads were not typed. Send again from byte %d of this stream's input", waiting, accepted)
	if at >= 0 && at != accepted {
		msg = fmt.Sprintf("this frame starts at byte %d, and the stream has taken %d bytes. It was not typed. Send again from byte %d", at, accepted, accepted)
	}
	body, _ := jsonx.Marshal(map[string]any{"code": ErrVerbBusy, "accepted": accepted, "message": msg})
	_ = ps.writeFrame(paneFrameError, body)
	return nil
}

func inputFrame(kind byte, payload []byte) (at int64, data []byte, ok bool) {
	if kind == paneFrameInput {
		return -1, payload, true
	}
	if len(payload) < 8 {
		return 0, nil, false
	}
	off := binary.BigEndian.Uint64(payload)
	if off > 1<<62 {
		return 0, nil, false
	}
	return int64(off), payload[8:], true
}

type paneInput struct {
	mu       sync.Mutex
	wake     chan struct{}
	frames   [][]byte
	bytes    int
	accepted int64
	refusing bool
	closed   bool
}

func (q *paneInput) push(at int64, data []byte) (accepted int64, waiting int, first, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return q.accepted, q.bytes, false, true
	}
	if at >= 0 && at != q.accepted {
		return q.accepted, q.bytes, false, false
	}
	if q.refusing || len(q.frames) > 0 && (q.bytes+len(data) > paneInputBytes || len(q.frames) >= paneInputFrames) {
		first = !q.refusing
		q.refusing = true
		return q.accepted, q.bytes, first, false
	}
	q.frames = append(q.frames, data)
	q.bytes += len(data)
	q.accepted += int64(len(data))
	if q.wake == nil {
		q.wake = make(chan struct{}, 1)
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return q.accepted, q.bytes, false, true
}

func (q *paneInput) pop() ([][]byte, chan struct{}) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.wake == nil {
		q.wake = make(chan struct{}, 1)
	}
	frames := q.frames
	q.frames = nil
	return frames, q.wake
}

func (q *paneInput) written(n int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.bytes -= n
	if q.bytes == 0 && len(q.frames) == 0 {
		q.refusing = false
	}
}

func (q *paneInput) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed, q.frames, q.bytes = true, nil, 0
}

func (ps *paneStream) admitInput() *verbError {
	ps.admitMu.Lock()
	defer ps.admitMu.Unlock()
	home := ps.home()
	params, _ := jsonx.Marshal(map[string]string{"session": home.Name(), "window": ps.window})
	if _, _, verr := ps.d.admitVerb(ps.cs, "send-text", params); verr != nil {
		return verr
	}
	return ps.d.recheckTyping(ps.cs, "send-text", home, ps.window)
}

func (ps *paneStream) writeInput() {
	for {
		frames, wake := ps.in.pop()
		for _, data := range frames {
			if _, err := ps.pty.Write(data); err != nil {
				LogBasic("Stream of pane %s could not type %d bytes: %v", shortWindowID(ps.window), len(data), err)
				ps.writeError(ptyWriteError(err))
			} else {
				ps.cs.lastInput.Store(time.Now().UnixNano())
				ps.pty.noteHumanKey(data)
			}
			ps.in.written(len(data))
		}
		if len(frames) > 0 {
			continue
		}
		select {
		case <-ps.quit:
			return
		case <-wake:
		}
	}
}

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

func (ps *paneStream) admitLease(cols, rows int) *verbError {
	ps.admitMu.Lock()
	defer ps.admitMu.Unlock()
	params, _ := jsonx.Marshal(map[string]any{"session": ps.home().Name(), "window": ps.window, "width": cols, "height": rows})
	_, _, verr := ps.d.admitVerb(ps.cs, "resize", params)
	return verr
}

func (ps *paneStream) setLease(cols, rows int) error {
	ps.leaseMu.Lock()
	defer ps.leaseMu.Unlock()
	if ps.closed {
		return nil
	}
	ps.leased = cols > 0
	ps.leaseAt = time.Now().UnixNano()
	home := ps.followHomeLocked()
	if ps.fit {
		ps.pty.setNoClients(home.tuiClients.Load() == 0)
	}
	return ps.pty.SetViewerLease(ps.subID, cols, rows, ps.fit)
}

func (ps *paneStream) home() *Session {
	if s := ps.pty.owner(); s != nil {
		return s
	}
	return ps.sess
}

func (ps *paneStream) followHomeLocked() *Session {
	home := ps.home()
	if ps.viewer && ps.viewerSess != home {
		if ps.viewerSess != nil {
			ps.viewerSess.removeViewer(ps.subID)
		}
		home.addViewer(ps.subID, ps.viewerReport)
		ps.viewerSess = home
	}
	return home
}

func (ps *paneStream) expireLease() {
	ps.leaseMu.Lock()
	if !ps.leased || ps.closed || time.Since(time.Unix(0, ps.leaseAt)) < paneLeaseTTL {
		ps.leaseMu.Unlock()
		return
	}
	ps.leased = false
	err := ps.pty.SetLease(ps.subID, 0, 0)
	ps.leaseMu.Unlock()
	if err != nil {
		LogBasic("Pane %s: an expired lease could not resize the pane: %v", shortWindowID(ps.window), err)
	}
	LogBasic("Client %s let its lease on pane %s expire", ps.cs.clientID, shortWindowID(ps.window))
	ps.writeError(newVerbError(ErrVerbLeaseExpired, fmt.Sprintf("the lease was not renewed for %s and was released. Send an L frame to hold the pane again", paneLeaseTTL)))
}

func (p *PTY) subscribeAt(clientID string, fromSeq int64, fromSnapshot bool) (*ptySubscriber, bool) {
	p.subscribersMu.Lock()
	defer p.subscribersMu.Unlock()
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
		sub.out.close()
		delete(p.subscribers, clientID)
		p.subscriberCount.Store(int32(len(p.subscribers)))
		return nil, false
	}
	return sub, true
}

func (p *PTY) sizeAt(seq int64) (int, int) {
	p.outputMu.RLock()
	defer p.outputMu.RUnlock()
	for _, m := range slices.Backward(p.resizeMarks) {
		if m.seq <= seq {
			return m.width, m.height
		}
	}
	return p.spawnW, p.spawnH
}
