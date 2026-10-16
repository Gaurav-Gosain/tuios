package session

import (
	"context"
	"errors"
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
// The reader waits for output outside readMu (readGate) and reads under it,
// so taking readMu never waits on a quiet program. vtSeq catching up with
// outputSeq is not enough for 3: vtWriter sets vtSeq before its after-write
// work, so a marker is queued behind the last chunk instead, and its close is
// the acknowledgement.

// errNotQuiescable is a pane whose bytes do not come from a descriptor here.
var errNotQuiescable = errors.New("the pane's output does not come from a descriptor on this machine")

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
		p.respMu.Lock()
		p.sendResponsesLocked()
		p.respMu.Unlock()
	}
	return nil
}

// Resume lets a quiesced pane's reader go on from the next byte the kernel
// holds.
func (p *PTY) Resume() {
	p.readMu.Lock()
	defer p.readMu.Unlock()
	if p.quiesced {
		p.quiesced = false
		close(p.resumed)
	}
}
