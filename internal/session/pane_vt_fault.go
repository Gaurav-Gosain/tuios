package session

import (
	"bytes"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/scrollclear"
	"github.com/Gaurav-Gosain/tuios/internal/testknob"
	"tuios.dev/vt"
)

const (
	vtFaultLimit  = 3
	vtFaultWindow = time.Minute
)

var vtTestPanic = []byte(testknob.String("vt_panic"))

const (
	vtFaultNotice  = "tuios: the terminal emulator of this pane failed, so its screen was reset. The program still runs."
	vtFrozenNotice = "tuios: the terminal emulator of this pane failed again. Until the program restarts, a new client and capture-pane show the screen from before this line. The program still runs."
)

func (p *PTY) feedVT(chunk vtChunk, frozen bool) (focusOn bool, fault any, stack []byte) {
	p.terminalMu.Lock()
	defer func() {
		if r := recover(); r != nil {
			fault, stack = r, debug.Stack()
		}
		p.terminalMu.Unlock()
	}()
	if chunk.width > 0 && chunk.height > 0 {
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
			panic("vt_panic: a chunk held the marker")
		}
		_, _ = p.terminal.Write(chunk.data)
	}
	p.vtSeq = chunk.seq
	return p.terminal != nil && p.terminal.FocusReportingEnabled(), nil, nil
}

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
	p.queueFaultNotice([]byte("\x1bc" + scrollclear.Drop + "\x1b[2m" + notice + "\x1b[m\r\n"))
	return recent, frozen
}

type faultNoticeQueue struct {
	mu       sync.Mutex
	pending  [][]byte
	draining bool
}

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

func (p *PTY) rebuildTerminal(width, height int, seq int64) {
	s := p.owner()
	if s == nil || width <= 0 || height <= 0 {
		return
	}
	s.ptysMu.Lock()
	fg, bg, pal := s.reportFg, s.reportBg, s.reportPal
	s.ptysMu.Unlock()
	term := vt.NewWithScrollback(width, height, s.scrollbackLines())
	term.SetReportColors(fg, bg, pal)
	p.respMu.Lock()
	p.terminalMu.Lock()
	s.setUpPaneTerminal(p, term)
	p.terminal = term
	p.vtSeq = seq
	p.terminalMu.Unlock()
	p.respMu.Unlock()
	term.OnResponse(p.wakeResponses)
}

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
