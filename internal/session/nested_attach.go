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
// when any of these holds:
//
//   - its controlling terminal is the pane's;
//   - a pane's shell is one of its ancestors;
//   - its environment names a pane of this daemon;
//   - its nesting probe showed up in the pane's output (nest_probe.go), which
//     catches ssh to this machine, script and other relays that pass bytes.
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
	if inside == "" || sock == "" {
		return nil
	}
	if os.Getenv("TUIOS_PANE_ID") == "" && os.Getenv("TUIOS_WINDOW_ID") == "" {
		return nil
	}
	if target != "" && target != inside {
		return nil
	}
	path, err := GetSocketPath()
	if err != nil || filepath.Clean(path) != filepath.Clean(sock) {
		return nil
	}
	return &NestedAttachError{Session: inside, Unnamed: target == ""}
}

// placeClient returns the session whose pane shows the client on cs, or nil,
// and which test said so. It is worked out once per connection: the process
// and the terminal behind a connection do not change, and a probe is written
// once. A link connection's peer is the proxy, so it is never placed.
func (d *Daemon) placeClient(cs *connState, p *AttachPayload) (*Session, string) {
	cs.mu.Lock()
	done, placed, why := cs.placementDone, cs.placedIn, cs.placedWhy
	cs.mu.Unlock()
	if done {
		if placed == "" {
			return nil, ""
		}
		return d.manager.GetSessionByID(placed), why
	}
	var sess *Session
	if !cs.viaLink {
		if !p.Served {
			sess, why = d.paneSession(cs.peerPID)
		}
		if sess == nil {
			if id := awaitNestProbe(p.NestProbe, p.NestProbeAgeMs); id != "" {
				sess, why = d.manager.GetSessionByID(id), "its output reaches a pane of this daemon"
			}
		}
	}
	cs.mu.Lock()
	cs.placementDone = true
	if sess != nil {
		cs.placedIn, cs.placedWhy = sess.ID, why
	}
	cs.mu.Unlock()
	return sess, why
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
	}
	// A terminal of its own does not clear it: script, or a terminal emulator
	// run from a pane, gives the client one, and its output still lands in the
	// pane. The walk starts at pid itself, which is the shell when a pane runs
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
			if sess := d.manager.GetSession(name); sess != nil {
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

// refuseNestedAttach sends the refusal for an attach that would show a session
// inside itself. target is nil for an unnamed attach, and inside itself when
// the client runs in a pane of the session it asks for.
func (d *Daemon) refuseNestedAttach(cs *connState, inside, target *Session, why string) error {
	unnamed := target == nil
	msg := NestedAttachMessage(inside.Name, unnamed)
	if target != nil && target.ID != inside.ID {
		msg = nestedChainMessage(inside.Name, target.Name)
	}
	LogBasic("Refused attach from client %s (pid %d): it runs in a pane of session %s (%s)",
		cs.clientID, cs.peerPID, inside.Name, why)
	return d.sendMessage(cs, MsgError, &ErrorPayload{
		Code:    ErrCodeNestedAttach,
		Message: msg,
		Session: inside.Name,
		Unnamed: unnamed,
	})
}
