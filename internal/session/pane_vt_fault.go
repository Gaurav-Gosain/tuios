package session

import (
	"bytes"
	"log"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"tuios.dev/vt"
)

// A panic in one pane's emulator must not end the daemon and every session
// in it. vtWriter feeds each chunk through feedVT, which recovers a panic,
// lets go of terminalMu, and reports it. vtFault then drops the emulator,
// builds a new one set up as the first was, and puts a reset and a notice
// into the pane's stream, so the new emulator and every attached client agree
// again from that byte. A pane whose emulator faults vtFaultLimit times in
// vtFaultWindow is not fed again: its program and PTY keep running, and the
// clients keep drawing the stream, but the daemon's copy of the screen stops.
//
// Not covered: a fault in libghostty-vt (a signal, not a panic), and a panic
// in another goroutine's call into the emulator. The failure modes are in
// the untangle notes, design/vt-loop-recover.md.

const (
	vtFaultLimit  = 3
	vtFaultWindow = time.Minute
)

// vtTestPanic, read once from TUIOS_TEST_VT_PANIC, makes feedVT panic on a
// chunk that holds it, so the end-to-end tests can drive a fault. Empty in
// every daemon not started for such a test, and then nothing is checked.
var vtTestPanic = []byte(os.Getenv("TUIOS_TEST_VT_PANIC"))

// vtFaultNotice and vtFrozenNotice are the line a pane shows after a fault.
const (
	vtFaultNotice  = "tuios: the terminal emulator of this pane failed, so its screen was reset. The program still runs."
	vtFrozenNotice = "tuios: the terminal emulator of this pane failed again. Until the program restarts, a new client and capture-pane show the screen from before this line. The program still runs."
)

// feedVT applies one chunk to the emulator under terminalMu: a resize, or
// output. It reports whether focus reporting is on after output, and a
// recovered panic with its stack.
func (p *PTY) feedVT(chunk vtChunk, frozen bool) (focusOn bool, fault any, stack []byte) {
	p.terminalMu.Lock()
	defer func() {
		if r := recover(); r != nil {
			fault, stack = r, debug.Stack()
		}
		p.terminalMu.Unlock()
	}()
	if chunk.width > 0 && chunk.height > 0 {
		// The pane's size and its emulator move together, so a snapshot
		// can never report a width the grid it serializes is not at.
		//
		// Skipped at the size the emulator already has, exactly as the
		// client skips it (applyStreamResize): a same-size resize resets
		// the scroll region and the tab stops, and one side doing that
		// while the other declines is a divergence the guest never asked
		// for.
		if p.terminal != nil && (p.terminal.Width() != chunk.width || p.terminal.Height() != chunk.height) {
			p.terminal.Resize(chunk.width, chunk.height)
			p.vtResizes++
		}
		return false, nil, nil
	}
	p.chunkGroup.Store(chunk.group)
	defer p.chunkGroup.Store(0)
	if p.terminal != nil && (!frozen || chunk.notice) {
		if len(vtTestPanic) > 0 && bytes.Contains(chunk.data, vtTestPanic) {
			panic("TUIOS_TEST_VT_PANIC: a chunk held the marker")
		}
		_, _ = p.terminal.Write(chunk.data)
	}
	// Recorded under the same lock the emulator is written and read under,
	// so a state snapshot and the position it was taken at can never
	// disagree. That pairing is what lets a client be resumed exactly where
	// the snapshot it was handed ends.
	p.vtSeq = chunk.seq
	return p.terminal != nil && p.terminal.FocusReportingEnabled(), nil, nil
}

// vtFault handles a panic feedVT recovered on chunk. faults holds the times
// of the faults before it; the result adds this one and reports whether the
// pane is now frozen.
func (p *PTY) vtFault(chunk vtChunk, fault any, stack []byte, faults []time.Time, size [2]int) ([]time.Time, bool) {
	log.Printf("Warning: pane %s: the terminal emulator panicked: %v\n%s", p.ID, fault, stack)
	now := time.Now()
	recent := faults[:0]
	for _, at := range faults {
		if now.Sub(at) < vtFaultWindow {
			recent = append(recent, at)
		}
	}
	recent = append(recent, now)
	frozen := len(recent) >= vtFaultLimit
	p.rebuildTerminal(size[0], size[1], chunk.seq)
	notice := vtFaultNotice
	if frozen {
		p.screenStopped.Store(true)
		if s := p.owner(); s != nil {
			s.PublishLiveFacts()
		}
		notice = vtFrozenNotice
		log.Printf("Warning: pane %s: the terminal emulator failed %d times in %v; the daemon stops feeding it", p.ID, len(recent), vtFaultWindow)
	}
	// RIS and ED 3 put the new emulator and every client's at the same
	// blank state, at the same byte of the stream.
	p.queueFaultNotice([]byte("\x1bc\x1b[3J\x1b[2m" + notice + "\x1b[m\r\n"))
	return recent, frozen
}

// faultNoticeQueue holds the notices of faults that are not yet in the
// stream. Each notice starts with a reset, so the last one in the stream is
// the one every screen shows. Two faults close together, such as on a
// program's echo and on its next line, must put their notices in in the
// order they happened: else the reset of the first notice can land after
// the second, and a frozen pane shows the notice of a pane that is not.
type faultNoticeQueue struct {
	mu       sync.Mutex
	pending  [][]byte
	draining bool
}

// queueFaultNotice adds data behind the notices still waiting, and starts
// the one goroutine that puts them into the stream if none runs. vtWriter
// calls it, and must not wait on streamMu, so the notices go in from that
// goroutine.
func (p *PTY) queueFaultNotice(data []byte) {
	q := &p.faultNotices
	q.mu.Lock()
	q.pending = append(q.pending, data)
	start := !q.draining
	q.draining = true
	q.mu.Unlock()
	if start {
		go p.drainFaultNotices()
	}
}

// drainFaultNotices puts the waiting notices into the stream one by one, in
// order, and ends when none is left.
func (p *PTY) drainFaultNotices() {
	q := &p.faultNotices
	for {
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.draining = false
			q.mu.Unlock()
			return
		}
		data := q.pending[0]
		q.pending = q.pending[1:]
		q.mu.Unlock()
		p.injectStream(data)
	}
}

// rebuildTerminal drops the pane's emulator for a new one of the same size,
// set up as the pane's first was, at stream position seq. The old one is not
// touched again: a panic can leave it half changed, with its own lock held.
func (p *PTY) rebuildTerminal(width, height int, seq int64) {
	s := p.owner()
	if s == nil || width <= 0 || height <= 0 {
		return
	}
	// The colours first: applyReportColors takes ptysMu, then terminalMu.
	s.ptysMu.Lock()
	fg, bg, pal := s.reportFg, s.reportBg, s.reportPal
	s.ptysMu.Unlock()
	term := vt.NewWithScrollback(width, height, s.scrollbackLines())
	term.SetReportColors(fg, bg, pal)
	// The reply forwarder reads p.terminal under respMu alone.
	p.respMu.Lock()
	p.terminalMu.Lock()
	s.setUpPaneTerminal(p, term)
	p.terminal = term
	p.vtSeq = seq
	p.terminalMu.Unlock()
	p.respMu.Unlock()
	term.OnResponse(p.wakeResponses)
}

// injectStream puts data into the pane's stream as if the program had written
// it: into the catch-up ring, to every subscriber, and to the emulator, in
// that order and at one position, as ClearHistory does. It runs on its own
// goroutine because vtWriter must never wait on streamMu: the reader holds it
// while it waits for vtWriter to take a chunk.
func (p *PTY) injectStream(data []byte) {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()
	p.outputMu.Lock()
	seq := p.appendToBuffer(data)
	p.outputMu.Unlock()
	p.broadcast(ptyChunk{data: data}, seq)
	if p.vtClosed {
		return
	}
	select {
	case p.vtWriteChan <- vtChunk{data: data, seq: seq, notice: true}:
	case <-p.ctx.Done():
	}
}

// stoppedScreens is the set of panes, by PTY id, whose emulator the daemon
// stopped feeding.
func (s *Session) stoppedScreens() map[string]bool {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	var out map[string]bool
	for id, p := range s.ptys {
		if p.screenStopped.Load() {
			if out == nil {
				out = make(map[string]bool)
			}
			out[id] = true
		}
	}
	return out
}
