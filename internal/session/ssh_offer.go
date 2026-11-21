package session

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

const sshOfferAfter = 5 * time.Second

// SSHOffer is a machine the client may offer to add: Dest is the destination
// as the person typed it, Machine the host part of it to show, and Target the
// user, host and port ssh -G resolves it to, which is what Never stores.
type SSHOffer struct {
	Dest    string `json:"dest"`
	Machine string `json:"machine"`
	Target  string `json:"target"`
}

type sshOfferCheck struct {
	mu     sync.Mutex
	leader int
	comm   string
	seen   time.Time
	found  bool
	login  remoteLogin
	done   bool
	offer  *SSHOffer
	wait   atomic.Bool
}

func (c *sshOfferCheck) reset() {
	c.leader, c.comm, c.seen, c.found = 0, "", time.Time{}, false
	c.login, c.done, c.offer = remoteLogin{}, false, nil
}

func (c *sshOfferCheck) waiting() bool { return c.wait.Load() }

type sshOfferProbe func(pty *PTY, info foregroundInfo, running bool) *SSHOffer

func (s *Session) setSSHOfferProbe(p sshOfferProbe) { s.offerProbe.Store(&p) }

func (s *Session) probeSSHOffer(ptyID string, info foregroundInfo, running bool) (offer *SSHOffer, read bool) {
	p := s.offerProbe.Load()
	if p == nil {
		return nil, false
	}
	pty := s.GetPTY(ptyID)
	if pty == nil {
		return nil, true
	}
	return (*p)(pty, info, running), true
}

func (s *Session) sshOfferWaiting() bool {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	for _, p := range s.ptys {
		if p.sshOffer.waiting() {
			return true
		}
	}
	return false
}

func syncSSHOffer(w *WindowState, r detectReading, write bool) bool {
	if !r.offerRead || sameSSHOffer(w.SSHOffer, r.offer) {
		return false
	}
	if write {
		w.SSHOffer = r.offer
	}
	return true
}

func sameSSHOffer(a, b *SSHOffer) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (s *Session) scanSSHOffers(resolve func(ptyID string) (foregroundInfo, bool), due func(ptyID string) bool) {
	if s.offerProbe.Load() == nil {
		return
	}
	type target struct{ windowID, ptyID string }
	s.stateMu.RLock()
	targets := make([]target, 0, len(s.state.Windows))
	for i := range s.state.Windows {
		if w := &s.state.Windows[i]; w.PTYID != "" {
			targets = append(targets, target{w.ID, w.PTYID})
		}
	}
	s.stateMu.RUnlock()

	readings := make(map[string]detectReading, len(targets))
	for _, t := range targets {
		if due != nil && !due(t.ptyID) {
			continue
		}
		info, running := resolve(t.ptyID)
		offer, read := s.probeSSHOffer(t.ptyID, info, running)
		readings[t.windowID] = detectReading{ptyID: t.ptyID, offer: offer, offerRead: read}
	}
	pass := func(st *SessionState, write bool) int {
		n := 0
		for i := range st.Windows {
			w := &st.Windows[i]
			if r, ok := readings[w.ID]; ok && r.ptyID == w.PTYID && syncSSHOffer(w, r, write) {
				n++
			}
		}
		return n
	}
	s.stateMu.RLock()
	changed := pass(s.state, false)
	s.stateMu.RUnlock()
	if changed == 0 {
		return
	}
	_ = s.mutateState(func(st *SessionState) error {
		if pass(st, true) == 0 {
			return errNoAgentDetectChange
		}
		return nil
	})
}

func (s *Session) clearSSHOffers() {
	_ = s.mutateState(func(st *SessionState) error {
		n := 0
		for i := range st.Windows {
			if st.Windows[i].SSHOffer != nil {
				st.Windows[i].SSHOffer = nil
				n++
			}
		}
		if n == 0 {
			return errNoAgentDetectChange
		}
		return nil
	})
}

// SetMachineOffer turns the offer to add a machine on or off. Off takes every
// offer off the windows. On makes the poll look at once.
func (d *Daemon) SetMachineOffer(on bool) {
	if d.machineOfferOff.Swap(!on) == !on {
		return
	}
	if on {
		d.wakes.detect.Set()
		return
	}
	for _, sess := range d.manager.AllSessions() {
		sess.clearSSHOffers()
	}
}

func (d *Daemon) sshOfferFor(pty *PTY, info foregroundInfo, running bool) *SSHOffer {
	c := &pty.sshOffer
	c.mu.Lock()
	defer c.mu.Unlock()
	defer func() { c.wait.Store(c.found && !c.done) }()
	if d.machineOfferOff.Load() {
		c.reset()
		return nil
	}
	if _, _, remote := pty.remoteForeground(); remote {
		c.reset()
		return nil
	}
	if !running || info.pid <= 0 || info.atShell() {
		c.reset()
		return nil
	}
	if c.leader != info.pid || c.comm != info.comm {
		c.reset()
		c.leader, c.comm, c.seen = info.pid, info.comm, time.Now()
	}
	if c.done {
		return c.offer
	}
	if !c.found {
		login, ok, seen, _ := findRemoteClient(info.shellPID)
		switch {
		case ok && interactiveLogin(login):
			c.found, c.login = true, login
		case seen, time.Since(c.seen) >= sshOfferAfter:
			c.done = true
			return nil
		default:
			return nil
		}
	}
	if time.Since(c.seen) < sshOfferAfter {
		return nil
	}
	c.done = true
	c.offer = d.machineOfferFor(c.login)
	return c.offer
}

func interactiveLogin(l remoteLogin) bool {
	if l.tunnel || l.forward || l.notLogin {
		return false
	}
	return !l.command || l.tty
}

func (d *Daemon) machineOfferFor(login remoteLogin) *SSHOffer {
	t, _, matches, ok := d.loginHosts(login)
	if !ok || len(matches) > 0 || gitHost(t.HostName) || thisMachine(t) {
		return nil
	}
	machine := destHost(login.dest)
	if machine == "" || hasControl(login.dest) {
		return nil
	}
	return &SSHOffer{Dest: login.dest, Machine: ClampDisplayText(machine), Target: sshTargetKey(t)}
}

func sshTargetKey(t federation.SSHTarget) string {
	key := t.User + "@" + t.HostName + ":" + t.Port
	switch {
	case t.ProxyJump != "" && t.ProxyJump != "none":
		key += " via " + t.ProxyJump
	case t.ProxyCommand != "" && t.ProxyCommand != "none":
		key += " via a proxy command"
	}
	return key
}

var gitHosts = []string{"github.com", "gitlab.com", "bitbucket.org", "codeberg.org"}

func gitHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, g := range gitHosts {
		if host == g || strings.HasSuffix(host, "."+g) {
			return true
		}
	}
	return false
}

func thisMachine(t federation.SSHTarget) bool {
	if t.Port != "22" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(t.HostName, "."))
	switch host {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	if strings.HasPrefix(host, "127.") {
		return true
	}
	name, err := os.Hostname()
	if err != nil || name == "" {
		return false
	}
	name = strings.ToLower(name)
	short, _, _ := strings.Cut(name, ".")
	return host == name || host == short
}
