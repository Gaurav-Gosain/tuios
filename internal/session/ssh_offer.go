package session

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// The offer to add a machine.
//
// A person who runs ssh in a pane to a machine tuios has no [hosts] entry for
// may want that machine on the rail. The daemon already reads each pane's
// foreground process after the pane prints (the detection poll), so it can
// see the login. When the login is one a person types to work on a machine,
// and ssh -G resolves it to a machine no entry reaches, the window state says
// so (WindowState.SSHOffer), and the client shows a message with Add, Not now
// and Never.
//
// What counts as such a login:
//
//   - An ssh or mosh client the pane's shell started, as findRemoteClient
//     reads it. ssh run by scp, rsync, git, sftp or sshfs is a transfer and is
//     never read. A ProxyJump hop is a child of the client, never the pane's
//     foreground program.
//   - A session that reads the pane's keys: no tunnel (-N, -f, -n, -W), no
//     -s or -T, and no remote command unless -t asks for a tty with it.
//   - A client that has run for sshOfferAfter, so a failed login or a password
//     prompt does not bring the message up.
//   - A target that is not this machine on port 22 and not a git host.
//
// It is not an agent feature. With [agents] enabled = false the poll reads
// nothing for agents, and runs this read alone (sshOfferScan). With
// notifications.machine_offer = false nothing here runs.
//
// The cost: a pane's check runs once for each new foreground process, and
// ssh -G runs once for each login that passes the rules above. The check
// rides the poll, which runs only after a pane printed, took input or changed,
// so a daemon at rest runs none of it.

// sshOfferAfter is how long a client must run before its machine is offered.
const sshOfferAfter = 5 * time.Second

// SSHOffer is a machine the client may offer to add: Dest is the destination
// as the person typed it, Machine the host part of it to show, and Target the
// user, host and port ssh -G resolves it to, which is what Never stores.
type SSHOffer struct {
	Dest    string `json:"dest"`
	Machine string `json:"machine"`
	Target  string `json:"target"`
}

// sshOfferCheck is one pane's check: the foreground process it was made for
// (its pid and name: a shell that forked keeps the pid when it execs ssh),
// when the poll first saw it, the login it found, and the answer once known.
type sshOfferCheck struct {
	mu     sync.Mutex
	leader int
	comm   string
	seen   time.Time
	found  bool
	login  remoteLogin
	done   bool
	offer  *SSHOffer
	// wait is found && !done, kept apart so the poll can ask without the
	// lock, which a running ssh -G holds.
	wait atomic.Bool
}

// reset forgets the check. The caller holds mu.
func (c *sshOfferCheck) reset() {
	c.leader, c.comm, c.seen, c.found = 0, "", time.Time{}, false
	c.login, c.done, c.offer = remoteLogin{}, false, nil
}

// waiting reports whether a login was found that has not run long enough
// yet. The poll keeps looking while it waits, and it waits sshOfferAfter at
// most.
func (c *sshOfferCheck) waiting() bool { return c.wait.Load() }

// sshOfferProbe answers the check for a pane on one poll read.
type sshOfferProbe func(pty *PTY, info foregroundInfo, running bool) *SSHOffer

// setSSHOfferProbe points the session at the daemon's check. A session with
// none checks nothing.
func (s *Session) setSSHOfferProbe(p sshOfferProbe) { s.offerProbe.Store(&p) }

// probeSSHOffer runs the check for a pane, if the session has one. read is
// false when it has none, and then the window keeps what it holds.
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

// sshOfferWaiting reports whether a pane of this session has a login that is
// not old enough yet, so the poll looks again with nothing new happening.
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

// syncSSHOffer moves the window's offer to the one the reading found, and
// reports whether it differs.
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

// scanSSHOffers is the poll's read with the agent features off: the
// foreground of each pane that is due, for the offer and nothing else.
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

// clearSSHOffers takes every offer off the session's windows.
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

// sshOfferFor is the daemon's check for one pane on one poll read.
func (d *Daemon) sshOfferFor(pty *PTY, info foregroundInfo, running bool) *SSHOffer {
	c := &pty.sshOffer
	c.mu.Lock()
	defer c.mu.Unlock()
	defer func() { c.wait.Store(c.found && !c.done) }()
	if d.machineOfferOff.Load() {
		c.reset()
		return nil
	}
	// A pane whose program is on another machine is that machine's to offer.
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
			// A client that is not a login decides at once. With no client
			// yet, a wrapper may still be about to start one, so a later
			// read looks again until the client would be old enough.
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

// interactiveLogin reports whether a login is a session that reads the
// pane's keys: no tunnel, no -s, -N or -T, and no remote command unless -t
// asks for a tty with it (ssh -t box tmux).
func interactiveLogin(l remoteLogin) bool {
	if l.tunnel || l.forward || l.notLogin {
		return false
	}
	return !l.command || l.tty
}

// machineOfferFor resolves a login and returns the offer for it, or nil when
// a [hosts] entry reaches it already, ssh cannot say where it goes, or it is
// this machine or a git host.
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

// sshTargetKey names a target in one line: user@host:port, and the hop when
// the login goes through one. The same host behind another hop can be another
// machine.
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

// gitHosts are the hosts a person reaches over ssh for git, never for a
// shell.
var gitHosts = []string{"github.com", "gitlab.com", "bitbucket.org", "codeberg.org"}

// gitHost reports whether host is a git host or a name under one
// (ssh.github.com, altssh.gitlab.com).
func gitHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, g := range gitHosts {
		if host == g || strings.HasSuffix(host, "."+g) {
			return true
		}
	}
	return false
}

// thisMachine reports whether a target is this machine's own ssh server: a
// loopback address or this machine's name, on port 22.
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
