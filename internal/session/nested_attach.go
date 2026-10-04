package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Attaching a session from one of its own panes.
//
// A client's size is its terminal's size, and the session's size is the
// smallest size of its clients. A client that runs in a pane of the session it
// shows has a terminal that is that pane, so its size is the session's size
// less the chrome around the pane. Each resize the client reports shrinks the
// session, which shrinks the pane, which shrinks the client: the session ends
// at 1x1 and the outer client draws one cell (#235). Its output also lands in
// the pane it draws, so it redraws its own frames without end. It is a
// mistake nobody means to make, since the person is already looking at that
// session.
//
// So the daemon places every attaching client, and refuses it when it would
// show a session inside itself. A client is placed in the session of a pane
// by the first of these that applies:
//
//   - its controlling terminal is the pane's;
//   - it has a terminal of its own: its nesting probe showed up in the pane's
//     output (nest_probe.go). That is what makes the loop, so it is the whole
//     answer. It catches script, ssh to this machine and other relays that
//     pass bytes, and lets through a terminal window started from a pane,
//     whose ancestry and variables point into the pane but whose output does
//     not;
//   - it has no terminal, so no probe: a pane's shell is one of its
//     ancestors, or its environment names a pane of this daemon.
//
// It is refused when it is placed in the session it asks for, or in a session
// that is itself shown, through a chain of clients, inside the one it asks
// for: session A in a pane of B, and B in a pane of A, is the same loop.
//
// An attach to any other session goes through. A pane of one session showing
// another is the ordinary tmux-like use.
//
// tuios attach --force, or AllowNestedEnv, skips the check, for the person who
// means it. A served client (tuios-web, the SSH server) is not placed by its
// process, because the process is the server: its size comes from the remote
// viewer. It is placed by its probe, which the SSH server writes to the ssh
// channel. Nothing here can see a relay that redraws the screen instead of
// passing bytes (tmux, mosh). The size floor keeps such a loop at 20x6. See
// minClientWidth.

// ErrCodeNestedAttach refuses an attach that would show a session inside
// itself, or an attach that names no session from a pane of this daemon.
const ErrCodeNestedAttach = 11

// AllowNestedEnv, set to 1, lets an attach through from a pane of its own
// session, as tuios attach --force does.
const AllowNestedEnv = "TUIOS_ALLOW_NESTED"

// PaneTTYEnv is set in every local pane to the path of the pane's terminal,
// so the client's own check can tell the pane's terminal from one it only
// inherited the pane's variables into.
const PaneTTYEnv = "TUIOS_PANE_TTY"

// NestedAttachError is the refusal of an attach that would show a session
// inside itself.
type NestedAttachError struct {
	// Session is the session the caller runs in.
	Session string
	// Unnamed is set when the attach named no session.
	Unnamed bool
	// msg is the daemon's wording, when it came from the daemon.
	msg string
}

func (e *NestedAttachError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return NestedAttachMessage(e.Session, e.Unnamed)
}

// NestedAttachMessage is the text of the refusal for a client that can say
// nothing more specific. The CLI says more for an unnamed attach, and the
// session switch says less.
func NestedAttachMessage(inside string, unnamed bool) string {
	if unnamed {
		return fmt.Sprintf("You are inside session %q. "+
			"To show another session in this pane, run 'tuios attach NAME'. "+
			"To start a new session, run 'tuios new NAME'.", inside)
	}
	return fmt.Sprintf("You are inside session %q. Attaching to %q here would show tuios inside itself. "+
		"Open a new terminal outside tuios to attach to it. "+
		"To attach anyway, run 'tuios attach --force %s'.", inside, inside, inside)
}

// nestedChainMessage is the refusal for a chain: the caller is inside session
// inside, which is shown inside session target.
func nestedChainMessage(inside, target string) string {
	return fmt.Sprintf("You are inside session %q, and %q is shown inside session %q. "+
		"Attaching to %q here would show tuios inside itself. "+
		"To attach anyway, run 'tuios attach --force %s'.", inside, inside, target, target, target)
}

// AsNestedAttach reports whether err is the refusal of an attach that would
// show a session inside itself, from the daemon or from the client check.
func AsNestedAttach(err error) (*NestedAttachError, bool) {
	if e, ok := errors.AsType[*NestedAttachError](err); ok {
		return e, true
	}
	if r, ok := errors.AsType[*attachRefused](err); ok && r.code == ErrCodeNestedAttach {
		return &NestedAttachError{Session: r.session, Unnamed: r.unnamed, msg: r.msg}, true
	}
	return nil, false
}

// NestedAllowedByEnv reports whether AllowNestedEnv asks to let a nested
// attach through.
func NestedAllowedByEnv() bool { return os.Getenv(AllowNestedEnv) == "1" }

// CheckNestedAttach is the client's own check, run before it dials, so the
// refusal comes before anything else is printed. It reads the pane's
// environment, and only when the pane belongs to the daemon this process
// would reach. The daemon repeats the check with the tests that still hold
// when the environment was cleared.
func CheckNestedAttach(target string) error {
	inside := os.Getenv("TUIOS_SESSION")
	sock := os.Getenv(SocketEnv)
	paneTTY := os.Getenv(PaneTTYEnv)
	if inside == "" || sock == "" || paneTTY == "" {
		return nil
	}
	if target != "" && target != inside {
		return nil
	}
	// A terminal window started from the pane has the pane's variables and a
	// terminal of its own. Only the pane's own terminal counts here.
	if !stdinIsTerminal(paneTTY) {
		return nil
	}
	path, err := GetSocketPath()
	if err != nil || filepath.Clean(path) != filepath.Clean(sock) {
		return nil
	}
	return &NestedAttachError{Session: inside, Unnamed: target == ""}
}

// placeClient returns the session whose pane shows the client on cs, or nil,
// and which test said so. A placement found once is kept for the connection:
// the process and the terminal behind a connection do not change. A client
// not placed yet is looked at again on its next attach, since its probe may
// have been seen since. A link connection's peer is the proxy, so it is never
// placed.
//
// It never waits: a probe that has not been seen yet is watched for after the
// attach instead. See watchNestedAfterAttach.
func (d *Daemon) placeClient(cs *connState, p *AttachPayload) (*Session, string) {
	cs.mu.Lock()
	placed, why := cs.placedIn, cs.placedWhy
	cs.mu.Unlock()
	if placed != "" {
		return d.manager.GetSessionByID(placed), why
	}
	var sess *Session
	if !cs.viaLink {
		if !p.Served {
			sess, why = d.paneSession(cs.peerPID)
		}
		if sess == nil {
			if id := seenNestProbe(p.NestProbe); id != "" {
				sess, why = d.manager.GetSessionByID(id), paneOriginProbe
			}
		}
	}
	if sess != nil {
		cs.mu.Lock()
		cs.placedIn, cs.placedWhy = sess.ID, why
		cs.mu.Unlock()
	}
	return sess, why
}

// paneOriginProbe is the reason for a client placed by its probe.
const paneOriginProbe = "its output reaches a pane of this daemon"

// watchNestedAfterAttach watches for the probe of a client that attached
// before its probe reached any pane, and takes the client off the session if
// the probe turns up in a pane that makes it nested. The attach itself does
// not wait for this, so an attach that is not nested costs nothing. A forced
// attach is placed but not taken off.
func (d *Daemon) watchNestedAfterAttach(cs *connState, targetID, nonce string, forced bool) {
	watchNestProbe(nonce, nestProbeWatch, func(paneSessionID string) {
		cs.mu.Lock()
		current := cs.sessionID
		cs.placedIn, cs.placedWhy = paneSessionID, paneOriginProbe
		cs.mu.Unlock()
		if forced || current != targetID {
			return
		}
		inside := d.manager.GetSessionByID(paneSessionID)
		target := d.manager.GetSessionByID(current)
		if inside == nil || target == nil || !d.shownInside(inside.ID, target.ID, cs.clientID) {
			return
		}
		d.ejectNested(cs, inside, target)
	})
}

// ejectNested takes a client off target because it turned out to show target
// inside itself, and tells it why with the refusal an attach would have got.
// The size it set is dropped with it, as for any client that leaves.
func (d *Daemon) ejectNested(cs *connState, inside, target *Session) {
	LogBasic("Detached client %s (pid %d) from session %s: %s, in session %s",
		cs.clientID, cs.peerPID, target.Name(), paneOriginProbe, inside.Name())
	if !d.detachClient(cs) {
		return
	}
	_ = d.sendMessage(cs, MsgSessionEnded, &SessionEndedPayload{
		SessionName: target.Name(),
		Reason:      nestedRefusalText(inside, target),
		Nested:      true,
	})
}

// shownInside reports whether session from is shown inside session to,
// directly or through a chain of clients: a client attached to from runs in a
// pane of a session that is shown inside to, and so on. The client on skip is
// left out, because it is the one asking and its old attach no longer holds.
func (d *Daemon) shownInside(from, to, skip string) bool {
	edges := make(map[string][]string)
	d.clientsMu.RLock()
	for id, cs := range d.clients {
		if id == skip {
			continue
		}
		cs.mu.Lock()
		if cs.isTUIClient && cs.sessionID != "" && cs.placedIn != "" {
			edges[cs.sessionID] = append(edges[cs.sessionID], cs.placedIn)
		}
		cs.mu.Unlock()
	}
	d.clientsMu.RUnlock()

	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == to {
			return true
		}
		for _, next := range edges[cur] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

// paneSession returns the session one of whose panes the process pid runs in,
// and which test said so, or nil when it runs in none. See the file comment
// for which test applies when.
//
// Unlike paneOrigin it fails open: a process whose record cannot be read is
// placed nowhere. That check guards acting as the person, where a wrong "yes"
// costs a claim; this one guards an attach, where a wrong "yes" locks the
// person out of their session.
func (d *Daemon) paneSession(pid int) (*Session, string) {
	if pid <= 0 || pid == os.Getpid() {
		return nil, ""
	}
	_, tty, ok := readProcLineage(pid)
	if !ok {
		return nil, ""
	}

	sessions := d.manager.AllSessions()
	shells := make(map[int]*Session)
	ttys := make(map[int64]*Session)
	for _, sess := range sessions {
		for _, id := range sess.ListPTYIDs() {
			pty := sess.GetPTY(id)
			if pty == nil {
				continue
			}
			shell := pty.ShellPID()
			if shell <= 0 {
				continue
			}
			shells[shell] = sess
			if _, t, ok := readProcLineage(shell); ok && t != 0 {
				ttys[t] = sess
			}
		}
	}

	if tty != 0 {
		if sess := ttys[tty]; sess != nil {
			return sess, paneOriginTTY
		}
		// A terminal of its own. Whether its output reaches a pane is what
		// the probe answers, and it answers it for this client: script in a
		// pane is caught there, and a terminal window started from a pane,
		// whose ancestry and variables point into the pane, is let through.
		return nil, ""
	}
	// No terminal, so no probe either: ancestry and variables are all there
	// is. The walk starts at pid itself, which is the shell when a pane runs
	// the client in place of its shell.
	cur := pid
	for range paneOriginMaxDepth {
		if sess := shells[cur]; sess != nil {
			return sess, paneOriginAncestor
		}
		if cur <= 1 {
			break
		}
		next, _, ok := readProcLineage(cur)
		if !ok {
			break
		}
		cur = next
	}
	for _, name := range []string{"TUIOS_PANE_ID", "TUIOS_WINDOW_ID"} {
		id, ok := readProcEnvVar(pid, name)
		if !ok || id == "" {
			continue
		}
		for _, sess := range sessions {
			if sess.holdsWindowID(id) {
				return sess, paneOriginEnv
			}
		}
	}
	if sock, ok := readProcEnvVar(pid, SocketEnv); ok && sock != "" && sock == d.manager.SocketPath() {
		if name, ok := readProcEnvVar(pid, "TUIOS_SESSION"); ok && name != "" {
			// A pane started before a rename holds the old name.
			if sess, _ := d.manager.ResolveSession(name); sess != nil {
				return sess, paneOriginEnv
			}
		}
	}
	return nil, ""
}

// holdsWindowID reports whether the session has a window with id.
func (s *Session) holdsWindowID(id string) bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.state == nil {
		return false
	}
	for i := range s.state.Windows {
		if s.state.Windows[i].ID == id {
			return true
		}
	}
	return false
}

// nestedRefusalText is the refusal for a client inside session inside that
// asks for session target.
func nestedRefusalText(inside, target *Session) string {
	if target.ID != inside.ID {
		return nestedChainMessage(inside.Name(), target.Name())
	}
	return NestedAttachMessage(inside.Name(), false)
}

// refuseNestedAttach sends the refusal for an attach that would show a session
// inside itself. target is nil for an unnamed attach, and inside itself when
// the client runs in a pane of the session it asks for.
func (d *Daemon) refuseNestedAttach(cs *connState, req *Message, inside, target *Session, why string) error {
	unnamed := target == nil
	msg := NestedAttachMessage(inside.Name(), true)
	if !unnamed {
		msg = nestedRefusalText(inside, target)
	}
	LogBasic("Refused attach from client %s (pid %d): it runs in a pane of session %s (%s)",
		cs.clientID, cs.peerPID, inside.Name(), why)
	return d.reply(cs, req, MsgError, &ErrorPayload{
		Code:    ErrCodeNestedAttach,
		Message: msg,
		Session: inside.Name(),
		Unnamed: unnamed,
	})
}
