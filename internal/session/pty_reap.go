package session

import (
	"errors"
	"os"
	"sync"
	"syscall"
)

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

func (g *reapGate) enter() {
	g.mu.Lock()
	for g.closed {
		g.condLocked().Wait()
	}
}

func (g *reapGate) leave() { g.mu.Unlock() }

func (g *reapGate) shut() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}

func (g *reapGate) open() {
	g.mu.Lock()
	g.closed = false
	g.condLocked().Broadcast()
	g.mu.Unlock()
}

func (p *PTY) monitorExit() {
	if p.proc == nil {
		return
	}
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

func (p *PTY) reapProcess() (code, sig int) {
	var state *os.ProcessState
	if p.cmd != nil {
		_ = p.cmd.Wait()
		state = p.cmd.ProcessState
	} else {
		var err error
		state, err = p.proc.Wait()
		if err != nil {
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
