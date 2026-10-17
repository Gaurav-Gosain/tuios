package session

import (
	"context"
	"errors"
	"fmt"
)

// Quiescing a pane: stopping its reader with every byte it took accounted for.
//
// A live handoff writes each pane down and execs a new daemon image over this
// one. A chunk the reader has taken from the kernel but not yet put in the
// ring is lost at the exec, and so is a reply the emulator queued that has
// not reached the program. Bytes still in the kernel are not: the new image
// reads them. So the pane is stopped at a point where nothing is in the
// daemon's hands, and Quiesce returns only once:
//
//  1. the reader will not take another byte,
//  2. every byte it took is in the ring and queued for the emulator,
//  3. vtWriter has written all of it, including the work it does after each
//     write (focus reporting, program status), and
//  4. every reply the emulator queued has been written to the program.
//
// From the quiesce to Resume the pane is frozen as well: a resize, a cell
// size in pixels from a client, or a lease's resize is held, not applied.
// Each would change the emulator, the kernel's window size and the replies
// (mode 2048 reports the size) after the pane was written down, so the new
// image would build the emulator at a size the program no longer has, and a
// reply queued after the flush would be lost at the exec. A size held is
// dropped at the exec, since every client attaches again and announces its
// sizes, and applied by Resume when the handoff is put back, since the
// clients still attached would not ask again. A size the coalescing of
// pty_winsize.go held back is written at the quiesce: its timer does not
// cross the exec, and the record carries the size the pane took.
//
// The reader waits for output outside readMu (readGate) and reads under it,
// so taking readMu never waits on a quiet program. vtSeq catching up with
// outputSeq is not enough for 3: vtWriter sets vtSeq before its after-write
// work, so a marker is queued behind the last chunk instead, and its close is
// the acknowledgement.

// errNotQuiescable is a pane whose bytes do not come from a descriptor here.
var errNotQuiescable = errors.New("the pane's output does not come from a descriptor on this machine")

// errRepliesUnwritten is a pane whose program is sent terminal replies and
// does not read them.
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

	// The reader takes streamMu before it lets readMu go, and holds it until
	// its chunk is queued. So once readMu has been seen with the flag set,
	// streamMu is next free only behind the last chunk.
	ack := make(chan struct{})
	p.streamMu.Lock()
	// Under streamMu, so no resize is queued behind the marker, and under
	// terminalMu, so no cell size reaches the emulator after it.
	p.terminalMu.Lock()
	p.frozen.Store(true)
	p.terminalMu.Unlock()
	if p.vtClosed {
		// The reader has ended, and closed the queue behind its last chunk.
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
		// The write of a reply blocks while the program's input queue is
		// full, and a program that never reads keeps it full for good. The
		// flush runs apart so the quiesce gives up at its deadline instead,
		// and the handoff is refused: a reply is never dropped, because the
		// program may be waiting for it, and none is carried, because the
		// write that holds it is still in progress. The flush finishes on its
		// own once the program reads or exits.
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
			return fmt.Errorf("%w: %w", errRepliesUnwritten, ctx.Err())
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

// thaw ends the freeze and applies the last size and cell size asked for
// during it.
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
