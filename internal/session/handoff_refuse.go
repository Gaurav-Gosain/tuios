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

type pipeHolds struct {
	mu     sync.Mutex
	holds  map[*string]struct{}
	closed chan struct{}
}

func (p *pipeHolds) hold(what string) (release func()) {
	for {
		release, wait := p.take(what)
		if wait == nil {
			return release
		}
		<-wait
	}
}

func (p *pipeHolds) tryHold(what string) (release func(), ok bool) {
	release, wait := p.take(what)
	return release, wait == nil
}

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

func (p *pipeHolds) first() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.holds {
		return *k
	}
	return ""
}

func (p *pipeHolds) shut() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.holds {
		return *k
	}
	p.closed = make(chan struct{})
	return ""
}

func (p *pipeHolds) open() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed != nil {
		close(p.closed)
		p.closed = nil
	}
}

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

func pipeRefusal(what string) error {
	return errfmt.Errorf("%s, and the upgrade would close the pipe it reports on. Try again when it has finished", what)
}

func hostedRefusal(n int) error {
	return errfmt.Errorf("%d %s here for another machine, and those cannot be handed over yet. Close them first", n, plural.Word(n, "pane runs", "panes run"))
}

type handoffRemote struct {
	session, window, name, host string
	exited                      bool
	sess                        *Session
}

func (r handoffRemote) label() string {
	name := r.name
	if name == "" {
		name = "window"
	}
	return fmt.Sprintf("%s: %s (%s) on %s", r.session, name, shortID(r.window), r.host)
}

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
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return cmp.Or(cmp.Compare(a.session, b.session), cmp.Compare(a.name, b.name), cmp.Compare(a.window, b.window)) < 0
	})
	return out
}

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

func exitedRemoteLabels(sessions []*Session) []string {
	var out []string
	for _, r := range handoffRemotes(sessions) {
		if r.exited {
			out = append(out, r.label())
		}
	}
	return out
}

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
