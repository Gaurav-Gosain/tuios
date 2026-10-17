package session

import (
	"fmt"
	"sync"
	"time"
)

// A pane kept open after its program exits.
//
// new-window with keep_open keeps the window when its process exits, and
// writes one line with the exit status under the program's last output. A
// script that ran its tests in a pane can then read both the output and the
// status, and the person sees why the pane stopped.
//
// The line has to land after the program's last byte. Two things report the
// end of a pane, and they arrive in either order: the process exit (the
// status) and the end of the read loop (the last output). The line goes when
// both have happened. The read loop does not always end: a child that kept
// the terminal open holds it. So an exit with the read loop still running
// waits a moment for the output to drain and then writes the line anyway.

// exitNoteDrain is how long an exit waits for the read loop to end before the
// line is written with the loop still running.
const exitNoteDrain = 250 * time.Millisecond

// exitNoteExitWait is how long the end of the read loop waits for the process
// exit. A process normally exits a moment after its terminal closes.
const exitNoteExitWait = 5 * time.Second

// exitNote is the state of one kept-open pane's exit line.
type exitNote struct {
	once     sync.Once
	exitOnce sync.Once
	readOnce sync.Once
	exit     chan struct{}
	readDone chan struct{}
	// shown is closed once the line is in the daemon's emulator, or once it
	// is clear the line will not be written.
	shown chan struct{}
}

func newExitNote() *exitNote {
	return &exitNote{exit: make(chan struct{}), readDone: make(chan struct{}), shown: make(chan struct{})}
}

// exitNoteShowWait bounds how long a wait for the exit answers late for the
// line. The line is normally in the emulator a moment after the exit.
const exitNoteShowWait = 2 * time.Second

// awaitExitNote waits until a kept-open pane's exit line is in the daemon's
// emulator. A caller that waited for the exit and reads the pane next then
// finds the line there. It returns at once for any other pane.
func (p *PTY) awaitExitNote() {
	if p == nil || p.keepOpen == nil {
		return
	}
	timer := time.NewTimer(exitNoteShowWait)
	defer timer.Stop()
	select {
	case <-p.keepOpen.shown:
	case <-timer.C:
	case <-p.ctx.Done():
	}
}

// exited is called by noteExit, once the status is known.
func (n *exitNote) exited(p *PTY) {
	n.exitOnce.Do(func() { close(n.exit) })
	go func() {
		timer := time.NewTimer(exitNoteDrain)
		defer timer.Stop()
		select {
		case <-n.readDone:
			// The read loop writes the line on its way out.
		case <-timer.C:
			p.writeExitNote()
		case <-p.ctx.Done():
		}
	}()
}

// noteReadEnd is called by readOutput on its way out. For a kept-open pane it
// waits for the exit status and writes the line, while the emulator's channel
// is still open.
func (p *PTY) noteReadEnd() {
	n := p.keepOpen
	if n == nil {
		return
	}
	n.readOnce.Do(func() { close(n.readDone) })
	if p.ctx.Err() != nil {
		return
	}
	timer := time.NewTimer(exitNoteExitWait)
	defer timer.Stop()
	select {
	case <-n.exit:
		p.writeExitNote()
	case <-timer.C:
	case <-p.ctx.Done():
	}
}

// writeExitNote writes the exit line into the pane's stream, once. It takes
// the path program output takes, so the daemon's emulator, the catch-up ring
// and every subscriber get the same bytes at the same position.
func (p *PTY) writeExitNote() {
	p.keepOpen.once.Do(func() {
		defer close(p.keepOpen.shown)
		seq, ok := p.queueExitNote()
		if !ok {
			return
		}
		// vtWriter applies the line a moment later. Waiting for it here
		// keeps awaitExitNote simple: shown means a capture finds the line.
		for range 200 {
			p.terminalMu.Lock()
			done := p.vtSeq >= seq
			p.terminalMu.Unlock()
			if done || p.ctx.Err() != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
}

// queueExitNote puts the exit line in the pane's stream and reports the
// stream position it ends at.
func (p *PTY) queueExitNote() (int64, bool) {
	if p.ctx.Err() != nil {
		return 0, false
	}
	data := []byte(exitNoteText(p))
	p.streamMu.Lock()
	defer p.streamMu.Unlock()
	if p.vtClosed {
		return 0, false
	}
	p.outputMu.Lock()
	seq := p.appendToBuffer(data)
	p.outputMu.Unlock()
	p.broadcast(ptyChunk{data: data}, seq)
	select {
	case p.vtWriteChan <- vtChunk{data: data, seq: seq}:
		return seq, true
	case <-p.ctx.Done():
		return 0, false
	}
}

// exitNoteText is the line a kept-open pane shows. It resets the attributes
// the program left, starts on a fresh line, and is dim so it does not read as
// the program's own output.
func exitNoteText(p *PTY) string {
	var msg string
	code, _ := p.ExitStatus()
	switch sig := p.ExitSignal(); {
	case p.host != "":
		// A pane on another machine reports only that its stream ended.
		msg = "The program ended."
	case sig > 0:
		msg = fmt.Sprintf("The program stopped on signal %d.", sig)
	case exitStatusLost(code, sig):
		msg = "The program ended. Its exit status is not known."
	default:
		msg = fmt.Sprintf("The program exited with status %d.", code)
	}
	return "\x1b[0m\r\n\x1b[2m[" + msg + "]\x1b[0m\r\n"
}

// exitResult adds a pane's exit status to a verb result: exit_code, and signal
// when a signal ended the process. It adds nothing while the process runs, for
// a pane on another machine, whose status does not cross the link, or when the
// status is lost.
func exitResult(p *PTY, res map[string]any) {
	if p == nil || p.host != "" {
		return
	}
	code, exited := p.ExitStatus()
	sig := p.ExitSignal()
	if !exited || exitStatusLost(code, sig) {
		return
	}
	res["exit_code"] = code
	if sig > 0 {
		res["signal"] = sig
	}
}

// exitStatusLost says a process's recorded end carries no status: a code of
// -1 with no signal, which a signal never leaves. It is what a process this
// daemon cannot reap records when the kernel does not say how it ended. On
// Linux before 6.15 that is a pane whose parent died in a keeper's recovery
// (handoff_keeper.go): see waitForeignExit.
func exitStatusLost(code, sig int) bool { return code < 0 && sig == 0 }
