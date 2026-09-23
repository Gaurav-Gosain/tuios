package session

import (
	"os"
)

// Who may act as the person.
//
// The attach nonce (human_sender.go) proves that a sender holds a live attach.
// It does not prove the sender is the person, because any process of the same
// user can attach, and an agent in a pane is such a process. An agent that was
// told, by a file it read or a page it fetched, to "reply to yourself as the
// human" could run tuios attach in a pseudo-terminal, take the nonce from the
// reply and send a verified answer to its own question.
//
// What separates the person from an agent is where the process runs, and the
// kernel says that on every unix socket connection: the peer's pid, recorded at
// connect time (SO_PEERCRED on Linux, LOCAL_PEERPID on macOS). A process is
// counted as inside a pane of this daemon when any of these holds:
//
//   - the daemon is one of its ancestors. Every pane shell, hook command and
//     popup is a child of the daemon, so this covers all of them and what they
//     start.
//   - its controlling terminal is one of this daemon's pane terminals. That
//     still holds for a process that was orphaned out of a pane, which the
//     ancestry walk loses when the orphan is reparented to init.
//   - its environment names a window of this daemon in TUIOS_PANE_ID or
//     TUIOS_WINDOW_ID, or this daemon's socket in TUIOS_SOCKET. That still
//     holds for an orphan that also left the terminal, and it also places the
//     commands the client starts, its hooks and dock components, which are
//     automation and not the person.
//
// A process that is inside a pane cannot act as the person: send-agent-message
// and ask-agent refuse from=human with forbidden, an attach from it is issued no
// nonce, reading the person's inbox from it does not mark anything read, and a
// state push from a client it runs does not mark a finished turn seen.
//
// A process whose record cannot be read is counted as inside a pane: the
// checks fail closed. A platform where the kernel does not give the peer's pid
// at all (Windows, the BSDs) is the exception: there every caller is treated as
// it was before, and the nonce is the only proof.
//
// This is not a sandbox. A process that leaves the pane on purpose, through a
// service manager, a scheduler, or setsid with a cleaned environment, is not
// inside it by any of these tests. What the checks stop is an agent acting as
// the person through tuios itself, which is the thing a prompt injection can
// talk it into in one line. docs/AGENT_STATE.md has the whole threat model.

// paneOriginMaxDepth bounds the ancestry walk. Real process trees are a dozen
// deep at most; the bound only matters for a pid table that changes under the
// walk.
const paneOriginMaxDepth = 64

// Pane origin reasons, for the log and the refusal message.
const (
	paneOriginAncestor   = "it runs inside a pane of this daemon"
	paneOriginTTY        = "its terminal is a pane of this daemon"
	paneOriginEnv        = "its environment is a pane's of this daemon"
	paneOriginUnreadable = "its process could not be read"
)

// paneOrigin reports whether the process pid runs inside a pane of this
// daemon, and which test said so. pid must be a known peer pid.
func (d *Daemon) paneOrigin(pid int) (bool, string) {
	self := os.Getpid()
	if pid <= 0 || pid == self {
		// The daemon itself, which is what a client in the daemon's own
		// process is: tests, and embedders that run both in one binary.
		return false, ""
	}
	ppid, tty, ok := readProcLineage(pid)
	if !ok {
		return true, paneOriginUnreadable
	}
	if tty != 0 && d.paneTTYs()[tty] {
		return true, paneOriginTTY
	}
	// The comparison comes before the stop at init, because in a container
	// the daemon can be pid 1 itself, and then every orphan is its child too.
	for depth := 0; depth < paneOriginMaxDepth; depth++ {
		if ppid == self {
			return true, paneOriginAncestor
		}
		if ppid <= 1 {
			break
		}
		next, _, ok := readProcLineage(ppid)
		if !ok {
			break
		}
		ppid = next
	}
	// TUIOS_WINDOW_ID is also what the client's hooks are started with, so a
	// hook command, which is automation and not the person, is placed the same
	// way as a pane.
	for _, name := range []string{"TUIOS_PANE_ID", "TUIOS_WINDOW_ID"} {
		if id, ok := readProcEnvVar(pid, name); ok && id != "" && d.holdsWindow(id) {
			return true, paneOriginEnv
		}
	}
	if sock, ok := readProcEnvVar(pid, "TUIOS_SOCKET"); ok && sock != "" && sock == d.manager.SocketPath() {
		return true, paneOriginEnv
	}
	return false, ""
}

// paneTTYs returns the controlling terminals of this daemon's pane shells and
// hosted panes, read from each shell process. It is read on demand: the
// checks that need it run on a send from human, an attach, and a read of the
// person's inbox, none of which is frequent.
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

// holdsWindow reports whether any session of this daemon has a window with id.
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

// connFromPane reports whether the process on the other end of cs runs inside
// a pane of this daemon, computing it once per connection. A connection whose
// peer pid is unknown is not: see the file comment.
func (d *Daemon) connFromPane(cs *connState) bool {
	if cs == nil || cs.peerPID <= 0 {
		return false
	}
	cs.fromPaneOnce.Do(func() {
		fromPane, why := d.paneOrigin(cs.peerPID)
		cs.fromPane = fromPane
		if fromPane {
			LogBasic("Client %s (pid %d) cannot act as the person: %s", cs.clientID, cs.peerPID, why)
		}
	})
	return cs.fromPane
}

// humanForbiddenError is the refusal a pane gets for speaking as the person.
func humanForbiddenError(verb string) *verbError {
	return hintedVerbError(ErrVerbForbidden, verb+" from human is refused: the caller runs inside a pane of this daemon, and only the person at an attached client can speak as human", &VerbHint{
		Param:   "from",
		Command: "tuios send-agent-message -w human --from \"$TUIOS_PANE_ID\" '<your question>'",
		Detail:  "Nothing was sent. Send as your own pane, with from set to $TUIOS_PANE_ID. To get an answer from the person, send them a message with -w human and wait for their reply with wait-for agent-message; only a reply marked verified_human is theirs.",
	})
}

// mayActAsHuman reports whether the caller on cs may act as the person: send
// or ask from human, be issued an attach nonce, mark the person's mail read, or
// mark a finished turn seen.
//
//   - A local connection may, unless its process runs inside a pane of this
//     daemon.
//   - A link connection may only when it arrived on the link-human socket,
//     which the proxy dials only for a stream the hub vouched for, and the
//     process that dialed it here, the proxy, is not inside a pane here either.
//     The plain link socket never may: its stream came from a hub that did not
//     vouch, either because the caller there runs inside one of the hub's panes
//     or because the hub predates the check.
//   - A call with no connection is the daemon itself, and may.
func (d *Daemon) mayActAsHuman(cs *connState) bool {
	if cs == nil {
		return true
	}
	if cs.viaLink && !cs.linkHuman {
		return false
	}
	return !d.connFromPane(cs)
}
