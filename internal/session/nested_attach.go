package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

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
	msg     string
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
	if !stdinIsTerminal(paneTTY) {
		return nil
	}
	path, err := GetSocketPath()
	if err != nil || filepath.Clean(path) != filepath.Clean(sock) {
		return nil
	}
	return &NestedAttachError{Session: inside, Unnamed: target == ""}
}

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

const paneOriginProbe = "its output reaches a pane of this daemon"

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
		return nil, ""
	}
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
		id, ok := procinfo.EnvVar(pid, name)
		if !ok || id == "" {
			continue
		}
		for _, sess := range sessions {
			if sess.holdsWindowID(id) {
				return sess, paneOriginEnv
			}
		}
	}
	if sock, ok := procinfo.EnvVar(pid, SocketEnv); ok && sock != "" && sock == d.manager.SocketPath() {
		if name, ok := procinfo.EnvVar(pid, "TUIOS_SESSION"); ok && name != "" {
			if sess, _ := d.manager.ResolveSession(name); sess != nil {
				return sess, paneOriginEnv
			}
		}
	}
	return nil, ""
}

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

func nestedRefusalText(inside, target *Session) string {
	if target.ID != inside.ID {
		return nestedChainMessage(inside.Name(), target.Name())
	}
	return NestedAttachMessage(inside.Name(), false)
}

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
