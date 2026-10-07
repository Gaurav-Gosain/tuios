package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Following the attached client's ssh agent.
//
// A person who reaches a machine over ssh with agent forwarding and runs
// tuios attach has an agent socket that lives as long as that ssh session.
// The panes were started earlier, by another client or by none, and their
// SSH_AUTH_SOCK names a socket that is gone or belongs to someone else's
// connection. tmux users fix this with a stable symlink that a hook points at
// the newest client's socket (discussion #549). With [daemon] ssh_agent =
// "follow" the daemon keeps that symlink itself:
//
//   - Each session has one link, agent-<id>.sock beside the daemon socket.
//     Every pane the session starts while the option is on gets
//     SSH_AUTH_SOCK set to the link. A shell that already runs keeps its own
//     value, and tuios ssh-agent-path prints the link for a shell rc.
//   - A client sends its SSH_AUTH_SOCK in its hello. The link points at the
//     socket of the client that attached to the session or used it last.
//   - When that client leaves, the link points at the socket of the client
//     before it, and with none left the link is removed.
//
// Which sockets count:
//
//   - Only a client that may act as the person (human_origin.go). A client
//     that runs inside a pane of this daemon does not, and neither does one
//     over a link: its socket is a path on another machine.
//   - Only a socket ownedSocket accepts: an absolute path to a Unix socket,
//     not a link, owned by this user, in a folder this user owns and nobody
//     else may write to. A socket in /tmp itself, or one another user could
//     put in place, is refused.
//
// A client served by tuios's own SSH server or by tuios-web sends no socket:
// that server holds no agent of the person's, so there is nothing to follow.

// agentCandidate is one client's socket for one session.
type agentCandidate struct {
	clientID string
	sock     string
}

// agentFollow holds, for each session, the clients whose sockets the link can
// point at, newest first, and where each link points now.
type agentFollow struct {
	mu        sync.Mutex
	bySession map[string][]agentCandidate
	target    map[string]string
}

// SSHAgentLinkPath is the stable agent link of the session with the given id,
// beside the daemon socket at socketPath. The name is short because a client
// connects to the link by path, and a Unix socket path has a small limit.
func SSHAgentLinkPath(socketPath, sessionID string) string {
	short, _, _ := strings.Cut(sessionID, "-")
	if len(short) > 12 {
		short = short[:12]
	}
	return filepath.Join(filepath.Dir(socketPath), "agent-"+short+".sock")
}

// sshAgentFollowing reports whether [daemon] ssh_agent is follow.
func (d *Daemon) sshAgentFollowing() bool { return d.manager.SSHAgentFollow() }

// SetSSHAgent applies [daemon] ssh_agent. Turning it off removes every link,
// so no pane is left with a socket that stops moving.
func (d *Daemon) SetSSHAgent(mode string) {
	on := strings.TrimSpace(mode) == config.SSHAgentFollow
	was := d.manager.SSHAgentFollow()
	d.manager.SetSSHAgentFollow(on)
	if was && !on {
		d.sshAgent.mu.Lock()
		for id := range d.sshAgent.target {
			_ = os.Remove(d.agentLinkPath(id))
		}
		d.sshAgent.bySession = nil
		d.sshAgent.target = nil
		d.sshAgent.mu.Unlock()
	}
}

// agentLinkPath is the link of the session with the given id.
func (d *Daemon) agentLinkPath(sessionID string) string {
	return SSHAgentLinkPath(d.manager.SocketPath(), sessionID)
}

// agentNoteUse records that the client on cs attached to or used the session
// with the given id, and points the session's link at its socket.
func (d *Daemon) agentNoteUse(cs *connState, sessionID string) {
	if sessionID == "" || !d.sshAgentFollowing() || cs.viaLink || !d.mayActAsHuman(cs) {
		return
	}
	cs.mu.Lock()
	sock := ""
	if cs.hello != nil {
		sock = cs.hello.SSHAuthSock
	}
	cs.mu.Unlock()
	if sock == "" {
		return
	}
	f := &d.sshAgent
	f.mu.Lock()
	defer f.mu.Unlock()
	list := f.bySession[sessionID]
	if len(list) > 0 && list[0].clientID == cs.clientID && list[0].sock == sock && f.target[sessionID] == sock {
		return
	}
	if !ownedSocket(sock) {
		LogBasic("Client %s (pid %d) sent an ssh agent socket that is not followed: it must be a socket this user owns, in a folder no other user can write to", cs.clientID, cs.peerPID)
		return
	}
	list = slices.DeleteFunc(list, func(c agentCandidate) bool { return c.clientID == cs.clientID })
	list = append([]agentCandidate{{clientID: cs.clientID, sock: sock}}, list...)
	if f.bySession == nil {
		f.bySession = make(map[string][]agentCandidate)
	}
	f.bySession[sessionID] = list
	d.relinkAgentLocked(sessionID)
}

// agentForget drops the client with the given id from the session's
// candidates, and moves the link to the next socket when it was the newest.
func (d *Daemon) agentForget(sessionID, clientID string) {
	f := &d.sshAgent
	f.mu.Lock()
	defer f.mu.Unlock()
	list, ok := f.bySession[sessionID]
	if !ok {
		return
	}
	list = slices.DeleteFunc(list, func(c agentCandidate) bool { return c.clientID == clientID })
	if len(list) == 0 {
		delete(f.bySession, sessionID)
	} else {
		f.bySession[sessionID] = list
	}
	d.relinkAgentLocked(sessionID)
}

// agentForgetSession removes the session's link when the session ends.
func (d *Daemon) agentForgetSession(sessionID string) {
	f := &d.sshAgent
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.target[sessionID]; ok {
		_ = os.Remove(d.agentLinkPath(sessionID))
		delete(f.target, sessionID)
	}
	delete(f.bySession, sessionID)
}

// relinkAgentLocked points the session's link at the newest socket that still
// passes ownedSocket, dropping the ones that no longer do, and removes the
// link when none is left. f.mu is held.
func (d *Daemon) relinkAgentLocked(sessionID string) {
	f := &d.sshAgent
	list := f.bySession[sessionID]
	for len(list) > 0 && !ownedSocket(list[0].sock) {
		list = list[1:]
	}
	if len(list) == 0 {
		delete(f.bySession, sessionID)
	} else {
		f.bySession[sessionID] = list
	}
	link := d.agentLinkPath(sessionID)
	if len(list) == 0 {
		if _, ok := f.target[sessionID]; ok {
			_ = os.Remove(link)
			delete(f.target, sessionID)
			LogBasic("Removed the ssh agent link of session %s: no attached client has an agent", shortID(sessionID))
		}
		return
	}
	want := list[0].sock
	if f.target[sessionID] == want {
		if cur, err := os.Readlink(link); err == nil && cur == want {
			return
		}
	}
	if err := replaceSymlink(want, link); err != nil {
		LogBasic("Could not point the ssh agent link of session %s at the client's socket: %v", shortID(sessionID), err)
		return
	}
	if f.target == nil {
		f.target = make(map[string]string)
	}
	f.target[sessionID] = want
	LogBasic("Pointed the ssh agent link of session %s at the socket of client %s", shortID(sessionID), list[0].clientID)
}

// replaceSymlink makes link point at target in one step: a new link under a
// random name, renamed over the old one, so a program that opens the link
// while it moves gets the old socket or the new one and never nothing.
func replaceSymlink(target, link string) error {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	tmp := link + "." + hex.EncodeToString(b[:])
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// agentEnv is what a new pane of the session is given while ssh_agent is
// follow: SSH_AUTH_SOCK naming the session's link. The link may not exist
// yet. It appears when a client with an agent attaches.
func (m *Manager) agentEnv(sessionID string) []string {
	if !m.SSHAgentFollow() || sessionID == "" {
		return nil
	}
	return []string{"SSH_AUTH_SOCK=" + SSHAgentLinkPath(m.SocketPath(), sessionID)}
}

// SSHAgentFollow reports whether [daemon] ssh_agent is follow.
func (m *Manager) SSHAgentFollow() bool { return m.sshAgentFollow.Load() }

// SetSSHAgentFollow sets whether new panes get the session's agent link.
func (m *Manager) SetSSHAgentFollow(on bool) { m.sshAgentFollow.Store(on) }

// verbSSHAgentPath reports a session's agent link and where it points.
func (d *Daemon) verbSSHAgentPath(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if strings.TrimSpace(p.Session) == "" {
		return nil, invalidParam("session", "session is the session whose agent link to print and cannot be empty")
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	out := map[string]any{
		"type":    "ssh_agent_path",
		"session": sess.Name(),
		"path":    d.agentLinkPath(sess.ID),
		"follow":  d.sshAgentFollowing(),
	}
	d.sshAgent.mu.Lock()
	if target, ok := d.sshAgent.target[sess.ID]; ok {
		out["target"] = target
	}
	d.sshAgent.mu.Unlock()
	return out, nil
}
