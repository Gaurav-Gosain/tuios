package session

import (
	"fmt"
	"sync"
	"time"
)

const exitNoteDrain = 250 * time.Millisecond

const exitNoteExitWait = 5 * time.Second

type exitNote struct {
	once     sync.Once
	exitOnce sync.Once
	readOnce sync.Once
	exit     chan struct{}
	readDone chan struct{}
	shown    chan struct{}
}

func newExitNote() *exitNote {
	return &exitNote{exit: make(chan struct{}), readDone: make(chan struct{}), shown: make(chan struct{})}
}

const exitNoteShowWait = 2 * time.Second

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

func (n *exitNote) exited(p *PTY) {
	n.exitOnce.Do(func() { close(n.exit) })
	go func() {
		timer := time.NewTimer(exitNoteDrain)
		defer timer.Stop()
		select {
		case <-n.readDone:
		case <-timer.C:
			p.writeExitNote()
		case <-p.ctx.Done():
		}
	}()
}

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

func (p *PTY) writeExitNote() {
	p.keepOpen.once.Do(func() {
		defer close(p.keepOpen.shown)
		seq, ok := p.queueExitNote()
		if !ok {
			return
		}
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

func exitNoteText(p *PTY) string {
	var msg string
	code, _ := p.ExitStatus()
	switch sig := p.ExitSignal(); {
	case p.host != "":
		msg = "The program ended."
		if rp, ok := p.pty.(*remotePane); ok {
			if why := rp.endedWhy(); why != "" {
				msg = "The pane on " + p.host + " ended: " + why + "."
			}
		}
	case sig > 0:
		msg = fmt.Sprintf("The program stopped on signal %d.", sig)
	default:
		msg = fmt.Sprintf("The program exited with status %d.", code)
	}
	return "\x1b[0m\r\n\x1b[2m[" + msg + "]\x1b[0m\r\n"
}

func exitResult(p *PTY, res map[string]any) {
	if p == nil || p.host != "" {
		return
	}
	code, exited := p.ExitStatus()
	if !exited {
		return
	}
	res["exit_code"] = code
	if sig := p.ExitSignal(); sig > 0 {
		res["signal"] = sig
	}
}
