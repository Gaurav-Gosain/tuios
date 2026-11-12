package session

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
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
	if err := remotePaneRefusal(d.manager.AllSessions()); err != nil {
		return err
	}
	if what := d.pipes.first(); what != "" {
		return pipeRefusal(what)
	}
	return nil
}

// pipeRefusal is the refusal while what reads a pipe.
func pipeRefusal(what string) error {
	return errfmt.Errorf("%s, and the upgrade would close the pipe it reports on. Try again when it has finished", what)
}

// hostedRefusal is the refusal while n panes run here for another machine. A
// hosted pane's master is not among the descriptors the exec keeps, so the
// exec would close its terminal and end the pane on the machine that opened it.
func hostedRefusal(n int) error {
	return errfmt.Errorf("%d %s here for another machine, and those cannot be handed over yet. Close them first", n, plural.Word(n, "pane runs", "panes run"))
}

// handoffRemote is a pane of a session here that runs on another machine.
type handoffRemote struct {
	session, window, name, host string
	// exited says the daemon has recorded the pane's exit. Such a pane holds
	// nothing a live upgrade must keep, and is closed rather than refused.
	exited bool
	sess   *Session
}

// label is how the pane is named to the person: session, name, short id and
// machine.
func (r handoffRemote) label() string {
	name := r.name
	if name == "" {
		name = "window"
	}
	return fmt.Sprintf("%s: %s (%s) on %s", r.session, name, shortID(r.window), r.host)
}

// remotePanes lists the panes of sessions that run on another machine,
// sorted by session, then window name, then window id, so a refusal reads the
// same from one try to the next.
func handoffRemotes(sessions []*Session) []handoffRemote {
	var out []handoffRemote
	for _, sess := range sessions {
		for _, w := range sess.GetState().Windows {
			if w.PTYID == "" {
				continue
			}
			if p := sess.GetPTY(w.PTYID); p != nil && p.host != "" {
				out = append(out, handoffRemote{session: sess.Name(), window: w.ID, name: windowDisplayName(w), host: p.host, exited: p.IsExited(), sess: sess})
			}
		}
	}
	// sort.Slice, not slices.SortFunc: this runs once per upgrade, and a
	// generic sort costs a copy of the sort per element type (docs/perf.md).
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return cmp.Or(cmp.Compare(a.session, b.session), cmp.Compare(a.name, b.name), cmp.Compare(a.window, b.window)) < 0
	})
	return out
}

// remotePaneRefusal is the refusal while a pane runs on another machine. A
// pane there has no master here to hand over. It names every such pane at
// once, with its session, name and machine, so one try says all there is to
// close, where it used to name the first and the next try the next. A pane
// whose exit the daemon has recorded does not block: the upgrade closes it
// (see closeExitedRemotes).
func remotePaneRefusal(sessions []*Session) error {
	var names []string
	for _, r := range handoffRemotes(sessions) {
		if !r.exited {
			names = append(names, r.label())
		}
	}
	if len(names) == 0 {
		return nil
	}
	return errfmt.Errorf("%d %s on another machine, and a pane there cannot be handed over. Close %s first: %s",
		len(names), plural.Word(len(names), "pane runs", "panes run"), plural.Word(len(names), "it", "them"), strings.Join(names, "; "))
}

// exitedRemoteLabels names the exited panes on other machines that an upgrade
// would close now, for the dialog that asks the person first.
func exitedRemoteLabels(sessions []*Session) []string {
	var out []string
	for _, r := range handoffRemotes(sessions) {
		if r.exited {
			out = append(out, r.label())
		}
	}
	return out
}

// closeExitedRemotes closes the window of every exited pane on another
// machine, and returns their labels. The caller holds the pane lifecycle lock
// and has passed every other refusal check: this is the last thing before the
// sessions are written down. shown, when not nil, is what the dialog showed
// the person; a pane that exited since is not closed unasked, and the upgrade
// is refused naming it.
func closeExitedRemotes(sessions []*Session, shown []string) ([]string, error) {
	var toClose []handoffRemote
	var unasked []string
	for _, r := range handoffRemotes(sessions) {
		if !r.exited {
			continue
		}
		if shown != nil && !slices.Contains(shown, r.label()) {
			unasked = append(unasked, r.label())
			continue
		}
		toClose = append(toClose, r)
	}
	if len(unasked) > 0 {
		return nil, errfmt.Errorf("%d more %s on another machine exited after the dialog was shown, and the upgrade closes only what it showed. Run it again: %s",
			len(unasked), plural.Word(len(unasked), "pane", "panes"), strings.Join(unasked, "; "))
	}
	closed := make([]string, 0, len(toClose))
	for _, r := range toClose {
		if _, err := r.sess.closeDaemonWindow(r.window, "the live upgrade, as its pane on "+r.host+" had exited", r.sess.closePTYHeld); err == nil {
			closed = append(closed, r.label())
		}
	}
	return closed, nil
}
