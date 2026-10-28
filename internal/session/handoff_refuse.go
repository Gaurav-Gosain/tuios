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
// write, and is never reaped: a fan check's status pipe, a capturing popup's
// output, a plugin command's output and a hook's stderr are four. A new
// feature that keeps a pipe read end takes a hold here too.

// pipeHolds counts the pipes the daemon is reading that a handoff would
// close, by what they are. While a handoff is under way they are shut, and no
// new hold is given until it is put back.
type pipeHolds struct {
	mu    sync.Mutex
	holds map[*string]struct{}
	// closed is non-nil while the holds are shut, and is closed when they
	// open again.
	closed chan struct{}
}

// hold records that what is being read from a pipe until the returned
// release runs. While the holds are shut it waits for them to open: a
// handoff that goes through ends the process first, so what waits never
// starts its program.
func (p *pipeHolds) hold(what string) (release func()) {
	for {
		release, wait := p.take(what)
		if wait == nil {
			return release
		}
		<-wait
	}
}

// tryHold is hold for a caller that cannot wait. It reports false, with no
// hold taken, while the holds are shut.
func (p *pipeHolds) tryHold(what string) (release func(), ok bool) {
	release, wait := p.take(what)
	return release, wait == nil
}

// take is a hold, or what to wait for while the holds are shut.
func (p *pipeHolds) take(what string) (release func(), wait <-chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed != nil {
		return nil, p.closed
	}
	key := &what
	if p.holds == nil {
		p.holds = map[*string]struct{}{}
	}
	p.holds[key] = struct{}{}
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			delete(p.holds, key)
			p.mu.Unlock()
		})
	}, nil
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

// shut stops new holds until open, and reports one of the holds instead,
// shutting nothing, when there are any.
func (p *pipeHolds) shut() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.holds {
		return *k
	}
	p.closed = make(chan struct{})
	return ""
}

// open gives holds again, and lets every hold that waited take its own.
func (p *pipeHolds) open() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed != nil {
		close(p.closed)
		p.closed = nil
	}
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
		return pipeRefusal(what)
	}
	return nil
}

// pipeRefusal is the refusal while what reads a pipe.
func pipeRefusal(what string) error {
	return fmt.Errorf("%s, and the upgrade would close the pipe it reports on. Try again when it has finished", what)
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
