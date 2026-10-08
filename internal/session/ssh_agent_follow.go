package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
	"github.com/Gaurav-Gosain/tuios/internal/sockpath"
	"github.com/google/uuid"
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
//     before it. With no client left, it points at the daemon's own
//     SSH_AUTH_SOCK when that one passes the same checks, and otherwise the
//     link is removed.
//   - Links are swept at start and stop, and when the option is turned off,
//     so a link never outlives the daemon that kept it.
//
// Which sockets count:
//
//   - Only a client that may act as the person (human_origin.go). A client
//     that runs inside a pane of this daemon does not, and neither does one
//     over a link: its socket is a path on another machine. A process that
//     leaves its pane on purpose is not found in it, as human_origin.go says,
//     so this bounds accidents and agents, not a determined local process.
//   - The path is resolved once (filepath.EvalSymlinks): ~/.ssh/agent.sock
//     and the 1Password agent are links to the real socket. The resolved
//     socket must pass ownedSocket: a Unix socket this user owns, in folders
//     owned by this user or root that no other user can write to, unless
//     sticky. The link points at the resolved path, and the resolved path is
//     what later checks and the dedupe use.
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
	// fallback is the daemon's own SSH_AUTH_SOCK, resolved, or "" when it
	// has none that passes ownedSocket. Read once at start.
	fallback string
	// stopped is set when the daemon stops: no link is made after it.
	stopped bool
	// forwards caches, per host, whether the link's ssh forwards the agent.
	// See hostForwardsAgent.
	forwards map[string]hostForward
}

// hostForward is one cached answer of hostForwardsAgent. key is what the
// answer was for, the address and the options, so a changed host is asked
// again.
type hostForward struct {
	key      string
	forwards bool
}

// resolveAgentSocket resolves sock once and checks the result with
// ownedSocket. It returns the resolved path and whether it may be followed.
func resolveAgentSocket(sock string) (string, bool) {
	if sock == "" || !filepath.IsAbs(sock) || hasControl(sock) {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(sock)
	if err != nil || !ownedSocket(resolved) {
		return "", false
	}
	return resolved, true
}

// SSHAgentLinkPath is the stable agent link of the session with the given id,
// beside the daemon socket at socketPath. The whole id is in the name, so two
// sessions never share a link.
func SSHAgentLinkPath(socketPath, sessionID string) string {
	return filepath.Join(filepath.Dir(socketPath), agentLinkPrefix(socketPath)+sessionID+".sock")
}

// hostAgentLinkPath is the agent link of one host: the socket the link ssh to
// that host forwards, while the host forwards the agent.
func hostAgentLinkPath(socketPath, host string) string {
	return filepath.Join(filepath.Dir(socketPath), agentLinkPrefix(socketPath)+hostLinkName(host)+".sock")
}

// hostLinkKeep is how much of a host name a host's link keeps, so the path
// stays short enough to connect to.
const hostLinkKeep = 32

// hostLinkName is the part of a host's link name after the prefix: link-,
// the host name lowered and cut to hostLinkKeep, and the first eight hex
// digits of the sha256 of the exact name. The hash keeps two hosts apart when
// their names differ only in case, on a filesystem that ignores case, or only
// past the cut.
func hostLinkName(host string) string {
	sum := sha256.Sum256([]byte(host))
	name := strings.ToLower(host)
	if len(name) > hostLinkKeep {
		name = name[:hostLinkKeep]
	}
	return "link-" + name + "-" + hex.EncodeToString(sum[:4])
}

// hostLinkPattern matches what hostLinkName makes.
var hostLinkPattern = lazyre.New(`^link-[a-z0-9][a-z0-9._-]{0,31}-[0-9a-f]{8}$`)

// agentLinkPrefix is how the links of the daemon on socketPath start. The
// default socket's are agent-, and a daemon on another socket in the same
// folder names its own after the socket.
func agentLinkPrefix(socketPath string) string {
	base := filepath.Base(socketPath)
	if base == "tuios.sock" {
		return "agent-"
	}
	return strings.TrimSuffix(base, ".sock") + "-agent-"
}

// isOwnAgentLink reports whether name, a file in the socket folder, is one of
// this daemon's links: the prefix, then a whole session id or link- and a
// host name, then .sock. A daemon on another socket in the same folder has
// links that do not match, whatever its socket is called.
func isOwnAgentLink(socketPath, name string) bool {
	rest, ok := strings.CutPrefix(name, agentLinkPrefix(socketPath))
	if !ok {
		return false
	}
	rest, ok = strings.CutSuffix(rest, ".sock")
	if !ok {
		return false
	}
	if strings.HasPrefix(rest, "link-") {
		return hostLinkPattern().MatchString(rest)
	}
	_, err := uuid.Parse(rest)
	return err == nil && len(rest) == 36
}

// sweepAgentLinks removes every agent link of this daemon in the socket
// folder: the ones a killed daemon left, at start, and its own, at stop.
// Only symlinks are removed.
func (d *Daemon) sweepAgentLinks() {
	sock := d.manager.SocketPath()
	entries, err := os.ReadDir(filepath.Dir(sock))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink == 0 || !isOwnAgentLink(sock, e.Name()) {
			continue
		}
		_ = os.Remove(filepath.Join(filepath.Dir(sock), e.Name()))
	}
}

// stopSSHAgent runs at the end of shutdown, once every client is gone: no
// link is made after it, and every link is removed, so kill-server leaves
// none behind.
func (d *Daemon) stopSSHAgent() {
	d.sshAgent.mu.Lock()
	d.sshAgent.stopped = true
	d.sshAgent.bySession = nil
	d.sshAgent.target = nil
	d.sshAgent.mu.Unlock()
	d.sweepAgentLinks()
}

// startSSHAgent sweeps what an earlier daemon left, reads the daemon's own
// socket for the fallback, and warns when a link path is too long for a
// client to connect to. It runs once, before any session is restored.
func (d *Daemon) startSSHAgent() {
	d.sweepAgentLinks()
	fallback, _ := resolveAgentSocket(os.Getenv("SSH_AUTH_SOCK"))
	d.sshAgent.mu.Lock()
	d.sshAgent.fallback = fallback
	d.sshAgent.mu.Unlock()
	if !d.sshAgentFollowing() {
		return
	}
	// The longest link: a session's whole id, or a host's name at its cut.
	n := max(len(SSHAgentLinkPath(d.manager.SocketPath(), "00000000-0000-0000-0000-000000000000")),
		len(hostAgentLinkPath(d.manager.SocketPath(), strings.Repeat("h", hostLinkKeep))))
	if n > sockpath.MaxLen() {
		log.Printf("Warning: the ssh agent links are %d characters long, and a Unix socket path can be at most %d. ssh in a pane cannot reach the agent. Set XDG_RUNTIME_DIR to a shorter folder.", n, sockpath.MaxLen())
	}
}

// sshAgentFollowing reports whether [daemon] ssh_agent is follow.
func (d *Daemon) sshAgentFollowing() bool { return d.manager.SSHAgentFollow() }

// SetSSHAgent applies [daemon] ssh_agent. Turning it off removes every link,
// so no pane is left with a socket that stops moving. Turning it on gives
// every session its link to the daemon's own socket until a client counts.
func (d *Daemon) SetSSHAgent(mode string) {
	on := strings.TrimSpace(mode) == config.SSHAgentFollow
	was := d.manager.SSHAgentFollow()
	d.manager.SetSSHAgentFollow(on)
	switch {
	case was && !on:
		d.sshAgent.mu.Lock()
		d.sshAgent.bySession = nil
		d.sshAgent.target = nil
		d.sshAgent.forwards = nil
		d.sshAgent.mu.Unlock()
		d.sweepAgentLinks()
	case !was && on:
		for _, info := range d.manager.ListSessions() {
			d.agentEnsureSession(info.ID)
		}
	}
}

// agentEnsureSession makes the session's link when it has none, for a session
// that was just created or restored.
func (d *Daemon) agentEnsureSession(sessionID string) {
	if !d.sshAgentFollowing() {
		return
	}
	d.sshAgent.mu.Lock()
	defer d.sshAgent.mu.Unlock()
	d.relinkAgentLocked(sessionID)
}

// agentLinkPath is the link of the session with the given id.
func (d *Daemon) agentLinkPath(sessionID string) string {
	return SSHAgentLinkPath(d.manager.SocketPath(), sessionID)
}

// hostAgentKey is the key of a host's link in agentFollow. A session id is a
// uuid and has no colon, so the two never meet.
func hostAgentKey(host string) string { return "host:" + host }

// agentLinkFor is the link kept under key: a session's, or a host's.
func (d *Daemon) agentLinkFor(key string) string {
	if host, ok := strings.CutPrefix(key, "host:"); ok {
		return hostAgentLinkPath(d.manager.SocketPath(), host)
	}
	return d.agentLinkPath(key)
}

// keyName is how a log line names key.
func keyName(key string) string {
	if host, ok := strings.CutPrefix(key, "host:"); ok {
		return "host " + host
	}
	return "session " + shortID(key)
}

// agentSockOf is the agent socket the connection offers. A local client sends
// it in its hello. A connection over a link from another machine has the one
// tuios stdio-proxy saw there, which is the agent the link's ssh forwarded:
// the hello's would be a path on the other machine.
func agentSockOf(cs *connState) string {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.viaLink {
		return cs.linkAgentSock
	}
	if cs.hello != nil {
		return cs.hello.SSHAuthSock
	}
	return ""
}

// agentNoteUse records that the client on cs attached to or used the session
// with the given id, and points the session's link at its socket.
func (d *Daemon) agentNoteUse(cs *connState, sessionID string) {
	if sessionID == "" {
		return
	}
	d.agentNote(cs, agentSockOf(cs), sessionID)
}

// agentNote points the links under keys at sock, the socket of the client on
// cs, when the client may act as the person and the socket passes the checks.
func (d *Daemon) agentNote(cs *connState, sock string, keys ...string) {
	if sock == "" || !d.sshAgentFollowing() || !d.mayActAsHuman(cs) {
		return
	}
	f := &d.sshAgent
	f.mu.Lock()
	defer f.mu.Unlock()
	// Checked under f.mu: the close forgets the client under it too
	// (agentForgetHosts), so a note that comes late, from the relay's timer,
	// cannot bring the client back after.
	select {
	case <-cs.done:
		return
	default:
	}
	for _, key := range keys {
		list := f.bySession[key]
		if len(list) > 0 && list[0].clientID == cs.clientID && f.target[key] == list[0].sock {
			continue
		}
		resolved, ok := resolveAgentSocket(sock)
		if !ok {
			// Said once per connection: a client that is refused keeps
			// reporting use once a second.
			if cs.agentRefused.CompareAndSwap(false, true) {
				LogBasic("Client %s (pid %d) sent an ssh agent socket that is not followed: it must resolve to a socket this user owns, in folders no other user can write to", cs.clientID, cs.peerPID)
			}
			return
		}
		list = slices.DeleteFunc(list, func(c agentCandidate) bool { return c.clientID == cs.clientID })
		list = append([]agentCandidate{{clientID: cs.clientID, sock: resolved}}, list...)
		if f.bySession == nil {
			f.bySession = make(map[string][]agentCandidate)
		}
		f.bySession[key] = list
		d.relinkAgentLocked(key)
	}
}

// linkSSHEnv is the environment the link ssh to h runs with. It is nil, the
// daemon's own environment exactly, unless ssh_agent is follow and the link
// to h forwards the agent. Then SSH_AUTH_SOCK names h's own link, which
// follows the clients attached to a session on h, so the agent the link
// forwards is the agent of the person working there. The link is made before
// ssh starts, and moving it does the rest.
//
// A host that does not forward keeps the daemon's environment: its ssh may
// sign in with the daemon's agent, and pointing it elsewhere would change how
// the host authenticates.
func (d *Daemon) linkSSHEnv(h federation.Host) []string {
	if !d.sshAgentFollowing() || !d.hostForwardsAgent(h) {
		return nil
	}
	key := hostAgentKey(h.Name)
	d.agentEnsureSession(key)
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "SSH_AUTH_SOCK=") })
	return append(env, "SSH_AUTH_SOCK="+d.agentLinkFor(key))
}

// hostForwardsAgent reports whether the link ssh to h forwards the agent:
// forwardagent yes in what ssh -G prints for h's address with h's ssh_options,
// which is ssh's own reading of the options, ~/.ssh/config and their
// precedence. The answer is cached per host until the config changes. A
// failed or timed-out ssh -G is not cached, and is asked again at the next
// connect. A ForwardAgent that names a socket forwards that socket, not
// SSH_AUTH_SOCK, so it counts as no.
func (d *Daemon) hostForwardsAgent(h federation.Host) bool {
	key := h.Addr + "\x00" + strings.Join(h.SSHOptions, "\x00")
	f := &d.sshAgent
	f.mu.Lock()
	if c, ok := f.forwards[h.Name]; ok && c.key == key {
		f.mu.Unlock()
		return c.forwards
	}
	f.mu.Unlock()
	fwd, ok := sshConfigForwards(h)
	if !ok {
		return false
	}
	f.mu.Lock()
	if f.forwards == nil {
		f.forwards = make(map[string]hostForward)
	}
	f.forwards[h.Name] = hostForward{key: key, forwards: fwd}
	f.mu.Unlock()
	return fwd
}

// forgetHostForwards drops the cached answers, on a config reload: the
// [hosts] table or ~/.ssh/config may say something else now.
func (d *Daemon) forgetHostForwards() {
	d.sshAgent.mu.Lock()
	d.sshAgent.forwards = nil
	d.sshAgent.mu.Unlock()
	federation.ForgetSSHConfig()
}

// sshConfigForwards asks ssh -G what it would do for h's address with h's
// options, which CheckSSHOptions has passed. fwd is whether that is
// forwardagent yes. ok is false when ssh -G failed or gave no answer.
func sshConfigForwards(h federation.Host) (fwd, ok bool) {
	c, ok := federation.ReadSSHConfig(h)
	if !ok || c.ForwardAgent == "" {
		return false, false
	}
	return strings.EqualFold(c.ForwardAgent, "yes"), true
}

// agentPruneHosts removes the link and the entries of every host that is not
// in hosts, when the config drops one.
func (d *Daemon) agentPruneHosts(hosts []federation.Host) {
	keep := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		keep[hostAgentKey(h.Name)] = true
	}
	f := &d.sshAgent
	f.mu.Lock()
	defer f.mu.Unlock()
	drop := func(key string) {
		if !strings.HasPrefix(key, "host:") || keep[key] {
			return
		}
		if _, linked := f.target[key]; linked {
			_ = os.Remove(d.agentLinkFor(key))
			delete(f.target, key)
		}
		delete(f.bySession, key)
		delete(f.forwards, strings.TrimPrefix(key, "host:"))
	}
	for key := range f.bySession {
		drop(key)
	}
	for key := range f.target {
		drop(key)
	}
}

// agentForgetHosts drops the client with the given id from every host's
// candidates, when its connection closes.
func (d *Daemon) agentForgetHosts(clientID string) {
	f := &d.sshAgent
	f.mu.Lock()
	var keys []string
	for key := range f.bySession {
		if strings.HasPrefix(key, "host:") {
			keys = append(keys, key)
		}
	}
	f.mu.Unlock()
	for _, key := range keys {
		d.agentForget(key, clientID)
	}
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
	if f.stopped {
		return
	}
	list := f.bySession[sessionID]
	for len(list) > 0 && !ownedSocket(list[0].sock) {
		list = list[1:]
	}
	if len(list) == 0 {
		delete(f.bySession, sessionID)
	} else {
		f.bySession[sessionID] = list
	}
	link := d.agentLinkFor(sessionID)
	want, from := "", "the daemon's own socket"
	if len(list) > 0 {
		want, from = list[0].sock, "the socket of client "+list[0].clientID
	} else if f.fallback != "" && ownedSocket(f.fallback) {
		want = f.fallback
	}
	if want == "" {
		if _, ok := f.target[sessionID]; ok {
			_ = os.Remove(link)
			delete(f.target, sessionID)
			LogBasic("Removed the ssh agent link of %s: no attached client has an agent, and the daemon has none", keyName(sessionID))
		}
		return
	}
	if f.target[sessionID] == want {
		if cur, err := os.Readlink(link); err == nil && cur == want {
			return
		}
	}
	if err := replaceSymlink(want, link); err != nil {
		LogBasic("Could not point the ssh agent link of %s at %s: %v", keyName(sessionID), from, err)
		return
	}
	if f.target == nil {
		f.target = make(map[string]string)
	}
	f.target[sessionID] = want
	LogBasic("Pointed the ssh agent link of %s at %s", keyName(sessionID), from)
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
