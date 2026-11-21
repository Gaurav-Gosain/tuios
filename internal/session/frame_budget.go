package session

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"golang.org/x/sync/semaphore"
)

const (
	largeFrame = 64 << 10

	firstFrameChunk = 4 << 10

	frameGrowStep = 4 << 20

	personBudgetBytes = 96 << 20
	peerBudgetBytes   = 48 << 20

	readBudgetWait = 2 * time.Second

	frameBodyDeadline = 30 * time.Second

	firstByteDeadline = 30 * time.Second

	maxConnections = 1024

	humanReserve = 128

	maxConnsPerPane = 64

	maxLinkConnections = 256

	connReadBuffer = 4 << 10
)

const frameCopies = 2

type memBudget struct {
	sem  *semaphore.Weighted
	size int64
}

func newMemBudget(size int64) *memBudget {
	return &memBudget{sem: semaphore.NewWeighted(size), size: size}
}

func (b *memBudget) acquire(n int64, wait time.Duration) (func(), bool) {
	n = min(n, b.size)
	if n <= 0 {
		return func() {}, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	if err := b.sem.Acquire(ctx, n); err != nil {
		return nil, false
	}
	return func() { b.sem.Release(n) }, true
}

func (d *Daemon) readBudgetFor(cs *connState) *memBudget {
	d.readBudgetOnce.Do(func() {
		d.personBudget = newMemBudget(personBudgetBytes)
		d.peerBudget = newMemBudget(peerBudgetBytes)
	})
	if cs == nil || cs.paneOnly {
		return d.peerBudget
	}
	if cs.viaLink {
		if cs.linkHuman {
			return d.personBudget
		}
		return d.peerBudget
	}
	if d.manager != nil && (!d.mayActAsHuman(cs) || !d.bufferAccess(cs).person()) {
		return d.peerBudget
	}
	return d.personBudget
}

// FrameBusyError is a frame the daemon had no memory for: other large frames
// held the read budget for longer than it waits, or the pane still has a
// large input it has not read. The reader has skipped the body, so the
// stream is still in step, and the sender may send it again.
type FrameBusyError struct {
	Type MessageType
	Size uint32
	// ReqID is the request id the frame carried, so the refusal can be sent
	// as the answer to it. Zero for an untagged frame.
	ReqID uint64
}

func (e *FrameBusyError) Error() string {
	return fmt.Sprintf("%s message of %d bytes was not read because the daemon is busy with other large messages",
		MessageTypeName(e.Type), e.Size)
}

// FrameForbiddenError is a frame whose type the connection may not send, under
// its link policy or its pane's grants. The body was skipped unread.
type FrameForbiddenError struct {
	ReqID uint64
	Err   *verbError
}

func (e *FrameForbiddenError) Error() string { return e.Err.Message }

func (d *Daemon) readClientFrame(cs *connState, br *bufio.Reader) (*Message, func(), error) {
	noop := func() {}
	totalLen, err := readFrameLength(cs.conn, br, 0, frameBodyDeadline)
	if err != nil {
		return nil, noop, err
	}
	h, err := readFrameHeader(br, totalLen)
	if err != nil {
		return nil, noop, err
	}
	if typeMax := daemonFrameLimit(h.Type); totalLen > typeMax {
		if err := skipPayload(br, h); err != nil {
			return nil, noop, err
		}
		return nil, noop, &FrameTooLargeError{Type: h.Type, Size: totalLen, Limit: typeMax, ReqID: h.ReqID}
	}
	verr := d.checkLinkMessage(cs, h.Type)
	if verr == nil {
		verr = d.checkGrantMessage(cs, h.Type)
	}
	if verr != nil {
		if err := skipPayload(br, h); err != nil {
			return nil, noop, err
		}
		return nil, noop, &FrameForbiddenError{ReqID: h.ReqID, Err: verr}
	}
	release := noop
	if h.PayloadLen > largeFrame {
		var ok bool
		release, ok = d.readBudgetFor(cs).acquire(int64(h.PayloadLen)*frameCopies, readBudgetWait)
		if !ok {
			if err := skipPayload(br, h); err != nil {
				return nil, noop, err
			}
			return nil, noop, &FrameBusyError{Type: h.Type, Size: totalLen, ReqID: h.ReqID}
		}
	}
	msg, err := readFramePayloadOf(br, h)
	if err != nil {
		release()
		return nil, noop, err
	}
	return msg, release, nil
}

const errTooManyConnections = "The tuios daemon has too many connections. Close some clients and try again."

// ErrTooManyConnections is the error a client gets when the daemon refused
// its connection over the cap.
var ErrTooManyConnections = errors.New("the tuios daemon has too many connections")

func (d *Daemon) admitConnection(conn net.Conn, open *atomic.Int64, limit int64) bool {
	if open.Add(1) <= limit {
		return true
	}
	open.Add(-1)
	d.refuseConnection(conn)
	n := d.connRefusals.Add(1)
	now := time.Now().UnixNano()
	last := d.connRefusedLog.Load()
	if now-last >= int64(connRefusedLogEvery) && d.connRefusedLog.CompareAndSwap(last, now) {
		d.connRefusals.Add(-n)
		if n == 1 {
			log.Printf("Refused a new connection. The socket already serves %d connections, which is the maximum.", limit)
		} else {
			log.Printf("Refused %d new connections. The socket already serves %d connections, which is the maximum.", n, limit)
		}
	}
	return false
}

const maxRefusalsTold = 32

func (d *Daemon) refuseConnection(conn net.Conn) {
	d.refuseConnectionWith(conn, errTooManyConnections)
}

func (d *Daemon) refuseConnectionWith(conn net.Conn, why string) {
	if d.refusalsTold.Add(1) > maxRefusalsTold {
		d.refusalsTold.Add(-1)
		_ = conn.Close()
		return
	}
	go func() {
		defer d.refusalsTold.Add(-1)
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		var first [1]byte
		n, _ := conn.Read(first[:])
		_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
		if n == 1 && isJSONStart(first[0]) {
			line, err := jsonx.Marshal(verbResponse{Error: newVerbError(ErrVerbTooManyConnections, why)})
			if err == nil {
				_, _ = conn.Write(append(line, '\n'))
			}
			return
		}
		msg, err := NewMessage(MsgError, &ErrorPayload{Code: ErrCodeBusy, Message: why})
		if err == nil {
			_ = WriteMessage(conn, msg)
		}
	}()
}

const errPaneConnections = "This pane already holds the most connections to the tuios daemon that one pane may hold. Close some, then try again."

const errPanesConnections = "Programs in panes already hold every connection to the tuios daemon that is not kept for the person. Close some, then try again."

type peerConnKey struct {
	window string
	tty    int64
	pid    int
}

func (d *Daemon) admitPeer(cs *connState) (release func(), ok bool) {
	if d.manager == nil || d.mayActAsHuman(cs) {
		return func() {}, true
	}
	if d.openPeerConns.Add(1) > maxConnections-humanReserve {
		d.openPeerConns.Add(-1)
		d.logPeerRefusal("Refused a connection from a pane: panes, hooks and plugins already hold %d connections, every slot not kept for the person.", maxConnections-humanReserve)
		d.refuseConnectionWith(cs.conn, errPanesConnections)
		return nil, false
	}
	key := peerConnKey{pid: cs.peerPID}
	if win, via := d.placePaneWindow(cs); win != "" && via != "env" {
		key = peerConnKey{window: win}
	} else if _, tty, _, ok := d.procLink()(cs.peerPID); ok && tty != 0 {
		key = peerConnKey{tty: tty}
	}
	d.peerConnsMu.Lock()
	if d.peerConns[key] >= maxConnsPerPane {
		d.peerConnsMu.Unlock()
		d.openPeerConns.Add(-1)
		d.logPeerRefusal("Refused a connection from a pane: that pane already holds %d connections, the most one pane may hold.", maxConnsPerPane)
		d.refuseConnectionWith(cs.conn, errPaneConnections)
		return nil, false
	}
	if d.peerConns == nil {
		d.peerConns = make(map[peerConnKey]int)
	}
	d.peerConns[key]++
	d.peerConnsMu.Unlock()
	return func() {
		d.peerConnsMu.Lock()
		if d.peerConns[key]--; d.peerConns[key] <= 0 {
			delete(d.peerConns, key)
		}
		d.peerConnsMu.Unlock()
		d.openPeerConns.Add(-1)
	}, true
}

func (d *Daemon) logPeerRefusal(format string, limit int) {
	now := time.Now().UnixNano()
	last := d.connRefusedLog.Load()
	if now-last >= int64(connRefusedLogEvery) && d.connRefusedLog.CompareAndSwap(last, now) {
		log.Printf(format, limit)
	}
}

const connRefusedLogEvery = 10 * time.Second

var errFirstByteTimeout = errors.New("the connection sent nothing")

// ErrVerbBusy refuses a verb the daemon has no room for now: its request
// line could not get its read budget, or the pane it types into has not read
// the last large input. Nothing was done, and the caller may try again. It is
// the verb protocol's ErrCodeBusy.
const ErrVerbBusy = "busy"

// ErrVerbTooManyConnections is the verb error a JSON client gets when the
// daemon refuses its connection over the cap.
const ErrVerbTooManyConnections = "too_many_connections"

func isJSONStart(b byte) bool {
	switch b {
	case '{', ' ', '\t', '\n', '\r':
		return true
	}
	return false
}
