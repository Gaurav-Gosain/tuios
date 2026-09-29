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
// at 1x1 and the outer client draws one cell (#235). It is also a mistake
// nobody means to make, since the person is already looking at that session.
//
// So the daemon refuses it. The terminal is what decides, because the
// terminal is what carries the loop:
//
//   - A client whose controlling terminal is a pane of the session is refused.
//   - A client with a terminal of its own is let through, whatever its
//     environment and ancestry say. A GUI terminal or a tmux server started
//     from a pane inherits the pane's variables but not its terminal, and
//     refusing it would lock the person out of their session for good. A
//     client in a PTY of its own inside a pane (script, a terminal emulator
//     run in the pane) is also let through: the size floor stops its loop.
//   - A client with no controlling terminal is placed by its ancestry and then
//     its environment, as human_origin.go places a pane process.
//
// The client does the same check first from its environment, so the refusal
// comes before anything else is printed: it refuses only when its own terminal
// is the one PaneTTYEnv names.
//
// Two ways past it. A served attach (tuios-web, the SSH server) takes its size
// from a remote viewer, not from the pane it was started in, so no loop is
// possible and the check is skipped. A forced attach (tuios attach --force, or
// AllowNestedEnv) skips it on request. Both still meet the size floor, which
// is also what stops the loop for the nesting nothing here can see: ssh to the
// same machine and mosh give the client a terminal of its own. See
// minClientWidth.

// ErrCodeNestedAttach refuses an attach from a pane of the session asked for,
// or an attach that names no session from a pane of this daemon.
const ErrCodeNestedAttach = 11

// PaneTTYEnv is set in every local pane to the path of the pane's terminal.
const PaneTTYEnv = "TUIOS_PANE_TTY"

// AllowNestedEnv, set to 1, lets an attach through from a pane of its own
// session, as tuios attach --force does.
const AllowNestedEnv = "TUIOS_ALLOW_NESTED"

// NestedAttachError is the refusal of an attach from inside the session.
type NestedAttachError struct {
	// Session is the session the caller runs in.
	Session string
	// Unnamed is set when the attach named no session.
	Unnamed bool
}

func (e *NestedAttachError) Error() string { return NestedAttachMessage(e.Session, e.Unnamed) }

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

// AsNestedAttach reports whether err is the refusal of an attach from inside
// the session, from the daemon or from the client check.
func AsNestedAttach(err error) (*NestedAttachError, bool) {
	if e, ok := errors.AsType[*NestedAttachError](err); ok {
		return e, true
	}
	if r, ok := errors.AsType[*attachRefused](err); ok && r.code == ErrCodeNestedAttach {
		return &NestedAttachError{Session: r.session, Unnamed: r.unnamed}, true
	}
	return nil, false
}

// NestedAllowedByEnv reports whether AllowNestedEnv asks to let a nested
// attach through.
func NestedAllowedByEnv() bool { return os.Getenv(AllowNestedEnv) == "1" }

// CheckNestedAttach is the client's own check, run before it dials, so the
// refusal comes before anything else is printed. It refuses only when this
// process's terminal is the pane's terminal, and the pane belongs to the
// daemon this process would reach. A process that only inherited the pane's
// variables is left to the daemon, which lets it through.
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
	if !stdinIsTerminal(paneTTY) {
		return nil
	}
	path, err := GetSocketPath()
	if err != nil || filepath.Clean(path) != filepath.Clean(sock) {
		return nil
	}
	return &NestedAttachError{Session: inside, Unnamed: target == ""}
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
		// The terminal decides: a pane's is nested, any other is the client's
		// own and is not.
		if sess := ttys[tty]; sess != nil {
			return sess, paneOriginTTY
		}
		return nil, ""
	}
	// No terminal. The walk starts at pid itself, which is the shell when a
	// pane runs the client in place of its shell.
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

// refuseNestedAttach sends the refusal for an attach from inside the session.
func (d *Daemon) refuseNestedAttach(cs *connState, inside *Session, why string, unnamed bool) error {
	LogBasic("Refused attach from client %s (pid %d): it runs in a pane of session %s (%s)",
		cs.clientID, cs.peerPID, inside.Name, why)
	return d.sendMessage(cs, MsgError, &ErrorPayload{
		Code:    ErrCodeNestedAttach,
		Message: NestedAttachMessage(inside.Name, unnamed),
		Session: inside.Name,
		Unnamed: unnamed,
	})
}
