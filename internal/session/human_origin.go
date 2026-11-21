package session

import (
	"net"
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

func peerPID(conn net.Conn) int { return procinfo.PeerPID(conn) }

func readEnvVarOf(pid int, name string) (string, bool) { return procinfo.EnvVar(pid, name) }

const paneOriginMaxDepth = 64

const (
	paneOriginAncestor   = "it runs inside a pane of this daemon"
	paneOriginTTY        = "its terminal is a pane of this daemon"
	paneOriginEnv        = "its environment is a pane's of this daemon"
	paneOriginUnreadable = "its process could not be read"
	paneOriginChanged    = "the process that connected has exited"
	paneOriginPlugin     = "it runs in a plugin's process session"
)

func (d *Daemon) pinPeer(cs *connState) {
	if cs == nil || cs.peerPID <= 0 || cs.peerPID == os.Getpid() {
		return
	}
	cs.peerStart, cs.peerStartOK = procinfo.StartTime(cs.peerPID)
	cs.peerEnv, cs.peerEnvOK = procinfo.Environ(cs.peerPID)
	d.bindPeerPluginToken(cs)
	if cs.pluginBound.Load() == nil {
		if p := d.pluginOfPeer(cs); p != nil && p.Format == herdrplugin.FormatTuios {
			id := p.PluginID
			cs.pluginBound.Store(&id)
		}
	}
	d.connFromPane(cs)
}

func (d *Daemon) peerChanged(cs *connState) bool {
	if cs == nil || cs.peerPID <= 0 || cs.peerPID == os.Getpid() {
		return false
	}
	now, ok := procinfo.StartTime(cs.peerPID)
	if !ok {
		return true
	}
	return !cs.peerStartOK || now != cs.peerStart
}

func (d *Daemon) paneOrigin(pid int) (bool, string) {
	env, ok := procinfo.Environ(pid)
	return d.paneOriginWith(pid, env, ok)
}

func (d *Daemon) paneOriginWith(pid int, env procinfo.Env, envOK bool) (bool, string) {
	self := os.Getpid()
	if pid <= 0 || pid == self {
		return false, ""
	}
	ppid, tty, start, ok := d.procLink()(pid)
	if !ok {
		return true, paneOriginUnreadable
	}
	if tty != 0 && d.paneTTYs()[tty] {
		return true, paneOriginTTY
	}
	walk := d.spawnRootFrom(pid, start, ppid, false)
	if walk.found {
		return true, paneOriginAncestor
	}
	if walk.broken {
		return true, paneOriginUnreadable
	}
	for _, name := range []string{"TUIOS_PANE_ID", "TUIOS_WINDOW_ID"} {
		if id, ok := env.Get(name); envOK && ok && id != "" && d.holdsWindow(id) {
			return true, paneOriginEnv
		}
	}
	if sock, ok := env.Get("TUIOS_SOCKET"); envOK && ok && sock != "" && sock == d.manager.SocketPath() {
		return true, paneOriginEnv
	}
	return false, ""
}

func (d *Daemon) paneTTYs() map[int64]bool {
	ttys := make(map[int64]bool)
	add := func(pid int) {
		if pid <= 0 {
			return
		}
		if _, tty, ok := readProcLineage(pid); ok && tty != 0 {
			ttys[tty] = true
		}
	}
	for _, sess := range d.manager.AllSessions() {
		for _, id := range sess.ListPTYIDs() {
			if pty := sess.GetPTY(id); pty != nil {
				add(pty.ShellPID())
			}
		}
	}
	d.hostedPanesMu.Lock()
	for _, hp := range d.hostedPanes {
		if hp.cmd != nil && hp.cmd.Process != nil {
			add(hp.cmd.Process.Pid)
		}
	}
	d.hostedPanesMu.Unlock()
	return ttys
}

func (d *Daemon) holdsWindow(id string) bool {
	for _, sess := range d.manager.AllSessions() {
		st := sess.GetState()
		for i := range st.Windows {
			if st.Windows[i].ID == id {
				return true
			}
		}
	}
	return false
}

func (d *Daemon) connFromPane(cs *connState) bool {
	if cs != nil && cs.paneOnly {
		return true
	}
	if cs == nil || cs.peerPID <= 0 {
		return false
	}
	cs.fromPaneOnce.Do(func() {
		fromPane, why := d.paneOriginWith(cs.peerPID, cs.peerEnv, cs.peerEnvOK)
		if !fromPane && d.peerChanged(cs) {
			fromPane, why = true, paneOriginChanged
		}
		if !fromPane && d.pluginOfPeer(cs) != nil {
			fromPane, why = true, paneOriginPlugin
		}
		cs.fromPane, cs.fromPaneWhy = fromPane, why
		if fromPane {
			LogBasic("Client %s (pid %d) cannot act as the person: %s", cs.clientID, cs.peerPID, why)
		}
	})
	return cs.fromPane
}

func humanForbiddenError(verb string) *verbError {
	return hintedVerbError(ErrVerbForbidden, verb+" from human is refused: the caller runs inside a pane of this daemon, and only the person at an attached client can speak as human", &VerbHint{
		Param:   "from",
		Command: "tuios send-agent-message -w human --from \"$TUIOS_PANE_ID\" '<your question>'",
		Detail:  "Nothing was sent. Send as your own pane, with from set to $TUIOS_PANE_ID. To get an answer from the person, send them a message with -w human and wait for their reply with wait-for agent-message; only a reply marked verified_human is theirs.",
	})
}

func (d *Daemon) mayActAsHuman(cs *connState) bool {
	if cs == nil {
		return true
	}
	if cs.viaLink && !cs.linkHuman {
		return false
	}
	if cs.pluginBound.Load() != nil {
		return false
	}
	return !d.connFromPane(cs)
}
