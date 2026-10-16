package session

import (
	"fmt"
	"sync"
)

// What a live handoff refuses, and why, in one place.
//
// The exec closes every descriptor that is not handed over, and a handoff
// hands over only PTY masters, sockets and the start lock. A pipe the daemon
// reads closes with it, and the program writing it dies of SIGPIPE on its next
// write: a fan check's status pipe and a capturing popup's output are two.
// A new feature that keeps a pipe read end takes a hold here too.

// pipeHolds counts the pipes the daemon is reading that a handoff would
// close, by what they are.
type pipeHolds struct {
	mu    sync.Mutex
	holds map[*string]struct{}
}

// hold records that what is being read from a pipe until the returned
// release runs.
func (p *pipeHolds) hold(what string) (release func()) {
	key := &what
	p.mu.Lock()
	if p.holds == nil {
		p.holds = map[*string]struct{}{}
	}
	p.holds[key] = struct{}{}
	p.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			delete(p.holds, key)
			p.mu.Unlock()
		})
	}
}

// first is one of the holds, empty when there are none.
func (p *pipeHolds) first() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.holds {
		return *k
	}
	return ""
}

// handoffRefusal says why the daemon cannot hand off now, nil when it can.
func (d *Daemon) handoffRefusal() error {
	if !handoffSupported() {
		return ErrHandoffUnsupported
	}
	if d.handoffArgs == nil {
		return errHandoffEmbedded
	}
	d.hostedPanesMu.Lock()
	hosted := len(d.hostedPanes)
	d.hostedPanesMu.Unlock()
	if hosted > 0 {
		return hostedRefusal(hosted)
	}
	for _, sess := range d.manager.AllSessions() {
		for _, p := range sess.allPTYs() {
			if p.host != "" {
				return fmt.Errorf("session %s has a pane running on %s, and a pane on another machine cannot be handed over. Close it first", sess.Name(), p.host)
			}
		}
	}
	if what := d.pipes.first(); what != "" {
		return fmt.Errorf("%s, and the upgrade would close the pipe it reports on. Try again when it has finished", what)
	}
	return nil
}

// hostedRefusal is the refusal while n panes run here for another machine. A
// hosted pane's master is not among the descriptors the exec keeps, so the
// exec would close its terminal and end the pane on the machine that opened it.
func hostedRefusal(n int) error {
	return fmt.Errorf("%d %s here for another machine, and those cannot be handed over yet. Close them first", n, pluralWord(n, "pane runs", "panes run"))
}

// pluralWord is one or many.
func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
