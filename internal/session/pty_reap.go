package session

import (
	"errors"
	"os"
	"sync"
	"syscall"
)

// Waiting for a pane's process, in two steps.
//
// A pane's exit status exists once: the reap that collects it removes it from
// the kernel. A plain blocking wait reaps the moment the process exits, and it
// cannot be told to stop waiting. So a pane that exits while a handoff is
// writing the panes down would be reaped by this image, its status kept only
// in this image's memory, and gone at the exec; the new image would then find
// no child to wait on.
//
// monitorExit therefore waits without reaping first (peekExit, waitid with
// WNOWAIT), and reaps only through the pane's reapGate. A handoff shuts every
// gate before it writes a record. A process that exits after that stays a
// zombie of this pid, which the exec keeps, and the new image reaps it with
// its real status. One reaped before the gate shut is written down as exited,
// with its status.

// reapGate lets a reap through only while it is open. Shutting it waits for a
// reap already in progress, so a pane's record is never written halfway
// through one.
type reapGate struct {
	mu     sync.Mutex
	cond   *sync.Cond
	closed bool
}

func (g *reapGate) condLocked() *sync.Cond {
	if g.cond == nil {
		g.cond = sync.NewCond(&g.mu)
	}
	return g.cond
}

// enter blocks while the gate is shut and returns holding it open. The caller
// reaps and records the exit, then calls leave.
func (g *reapGate) enter() {
	g.mu.Lock()
	for g.closed {
		g.condLocked().Wait()
	}
}

func (g *reapGate) leave() { g.mu.Unlock() }

// shut stops reaps from starting, once any in progress has finished.
func (g *reapGate) shut() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}

// open lets held reaps go on.
func (g *reapGate) open() {
	g.mu.Lock()
	g.closed = false
	g.condLocked().Broadcast()
	g.mu.Unlock()
}

// monitorExit waits for the pane's process to exit and records how it ended.
func (p *PTY) monitorExit() {
	if p.proc == nil {
		// A pane on another machine. There is no process here to wait on, so
		// its exit is heard where its bytes stop instead: see readOutput.
		return
	}
	// ECHILD for an adopted pane is a process that is not this one's child:
	// the daemon a handoff's keeper started has a pid of its own, and the
	// panes it took back belong to init. Their exit is still heard, through
	// the kernel's process events, with the status only where those carry
	// it. Any other error is answered by the reap below, which fails the same
	// way and records the status as unknown.
	foreign := canPeekExit && errors.Is(peekExit(p.proc.Pid), syscall.ECHILD) && p.cmd == nil
	var code, sig int
	if foreign {
		code, sig = waitForeignExit(p.proc.Pid)
	}

	p.reap.enter()
	defer p.reap.leave()
	if !foreign {
		code, sig = p.reapProcess()
	}
	if sig > 0 {
		p.exitSignal.Store(int32(sig))
	}
	p.noteExit(code)
}

// reapProcess collects the exit status of the pane's process: the code, -1 for
// a process ended by a signal or one whose status is lost, and the signal.
func (p *PTY) reapProcess() (code, sig int) {
	var state *os.ProcessState
	if p.cmd != nil {
		_ = p.cmd.Wait()
		state = p.cmd.ProcessState
	} else {
		var err error
		state, err = p.proc.Wait()
		if err != nil {
			// ECHILD: the process was reaped before this image adopted it,
			// and its status went with the image that did.
			if !errors.Is(err, syscall.ECHILD) {
				debugLog("[DEBUG] PTY %s: wait for adopted pid %d: %v", shortID(p.ID), p.proc.Pid, err)
			}
			return -1, 0
		}
	}
	if state == nil {
		return 0, 0
	}
	return state.ExitCode(), exitSignal(state)
}
