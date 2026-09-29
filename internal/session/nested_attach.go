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
// at 1x1, the outer client draws one cell, and the inner client redraws
// without end (#235). It is also a mistake nobody means to make, since the
// person is already looking at that session.
//
// So the daemon refuses it. paneSession finds the session a client runs in
// with the tests human_origin.go uses to find a pane (the terminal, the
// ancestry, then the environment), and handleAttach refuses an attach to that
// session. An attach to a different session is left alone: a pane of one
// session showing another is the ordinary tmux-like use, and the other
// session's size does not depend on this pane.
//
// Every test can be defeated on purpose (a client started through ssh to this
// machine has none of them), so the size calculation keeps a floor as well.
// See minClientWidth.

// ErrCodeNestedAttach refuses an attach from a pane of the session asked for,
// or an attach that names no session from a pane of this daemon.
const ErrCodeNestedAttach = 11

// NestedAttachError is the refusal of an attach from inside the session. It is
// complete on its own: it names the session and says what to do.
type NestedAttachError struct{ msg string }

func (e *NestedAttachError) Error() string { return e.msg }

// NestedAttachMessage is the text of the refusal, shared by the daemon and the
// client check that runs before it.
func NestedAttachMessage(inside string, unnamed bool) string {
	msg := fmt.Sprintf("You are inside session %q. Attaching to it here shows tuios inside itself. "+
		"To attach to %q, open a new terminal outside tuios.", inside, inside)
	if unnamed {
		return msg + " To show a different session in this pane, run 'tuios attach NAME'."
	}
	return msg + " To show a different session in this pane, name that session."
}

// AsNestedAttach reports whether err is the refusal of an attach from inside
// the session, from the daemon or from the client check.
func AsNestedAttach(err error) (*NestedAttachError, bool) {
	if e, ok := errors.AsType[*NestedAttachError](err); ok {
		return e, true
	}
	if r, ok := errors.AsType[*attachRefused](err); ok && r.code == ErrCodeNestedAttach {
		return &NestedAttachError{msg: r.msg}, true
	}
	return nil, false
}

// CheckNestedAttach is the client's own check, run before it dials, so the
// refusal comes before anything else is printed. It uses the pane's
// environment only, and only when the pane belongs to the daemon this process
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
	path, err := GetSocketPath()
	if err != nil || filepath.Clean(path) != filepath.Clean(sock) {
		return nil
	}
	if target != "" && target != inside {
		return nil
	}
	return &NestedAttachError{msg: NestedAttachMessage(inside, target == "")}
}

// paneSession returns the session one of whose panes the process pid runs in,
// and which test said so, or nil when it runs in none.
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
	// The walk starts at pid itself, which is the shell when a pane runs the
	// client in place of its shell.
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
	return d.sendError(cs, ErrCodeNestedAttach, NestedAttachMessage(inside.Name, unnamed))
}
