package session

import (
	"context"
	"errors"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

var errNotQuiescable = errors.New("the pane's output does not come from a descriptor on this machine")

var errRepliesUnwritten = errors.New("the program is not reading its input, and the terminal's replies to it cannot be written")

// Quiesce stops the pane's reader at a point where no byte it read is lost
// when the process execs. Resume undoes it. It is safe to call twice.
func (p *PTY) Quiesce(ctx context.Context) error {
	if p.gate == nil {
		return errNotQuiescable
	}
	p.readMu.Lock()
	if !p.quiesced {
		p.quiesced = true
		p.resumed = make(chan struct{})
	}
	p.readMu.Unlock()

	ack := make(chan struct{})
	p.streamMu.Lock()
	p.terminalMu.Lock()
	p.frozen.Store(true)
	p.terminalMu.Unlock()
	if p.vtClosed {
		p.streamMu.Unlock()
		ack = p.vtDone
	} else {
		select {
		case p.vtWriteChan <- vtChunk{ack: ack}:
		case <-ctx.Done():
			p.streamMu.Unlock()
			return ctx.Err()
		}
		p.streamMu.Unlock()
	}
	select {
	case <-ack:
	case <-ctx.Done():
		return ctx.Err()
	}

	if p.terminal != nil {
		flushed := make(chan struct{})
		go func() {
			p.respMu.Lock()
			p.sendResponsesLocked()
			p.respMu.Unlock()
			close(flushed)
		}()
		select {
		case <-flushed:
		case <-ctx.Done():
			return errfmt.Errorf("%w: %w", errRepliesUnwritten, ctx.Err())
		}
	}
	p.winsizeMu.Lock()
	if p.winsizeHeld.Load() && !p.winsizeClosed {
		p.writeHeldWinsizeLocked()
	}
	p.winsizeMu.Unlock()
	return nil
}

// Resume lets a quiesced pane's reader go on from the next byte the kernel
// holds, after applying the size and the cell size asked for while it was
// frozen.
func (p *PTY) Resume() {
	p.thaw()
	p.readMu.Lock()
	defer p.readMu.Unlock()
	if p.quiesced {
		p.quiesced = false
		close(p.resumed)
	}
}

func (p *PTY) thaw() {
	p.streamMu.Lock()
	if !p.frozen.Load() {
		p.streamMu.Unlock()
		return
	}
	p.terminalMu.Lock()
	p.frozen.Store(false)
	p.terminalMu.Unlock()
	if w, h := p.frozenW, p.frozenH; w > 0 && h > 0 {
		p.frozenW, p.frozenH = 0, 0
		if err := p.resizeStreamLocked(w, h); err != nil {
			debugLog("[DEBUG] PTY %s: the resize to %dx%d held by a handoff failed: %v", shortID(p.ID), w, h, err)
		}
	}
	p.streamMu.Unlock()

	p.winsizeMu.Lock()
	cw, ch := p.frozenCellW, p.frozenCellH
	p.frozenCellW, p.frozenCellH = 0, 0
	p.winsizeMu.Unlock()
	if cw > 0 && ch > 0 {
		if err := p.UpdatePixelDimensions(cw, ch); err != nil {
			debugLog("[DEBUG] PTY %s: the cell size held by a handoff failed: %v", shortID(p.ID), err)
		}
	}
}
