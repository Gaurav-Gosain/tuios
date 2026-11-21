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
)

type agentCandidate struct {
	clientID string
	sock     string
}

type agentFollow struct {
	mu        sync.Mutex
	bySession map[string][]agentCandidate
	target    map[string]string
	fallback  string
	stopped   bool
	forwards  map[string]hostForward
}

type hostForward struct {
	key      string
	forwards bool
}

func resolveAgentSocket(sock string) (string, bool) {
	if sock == "" || !filepath.IsAbs(sock) || hasControl(sock) {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(sock)
	if err != nil {
		return "", false
	}
	return ownedSocket(resolved)
}

// SSHAgentLinkPath is the stable agent link of the session with the given id,
// beside the daemon socket at socketPath. The whole id is in the name, so two
// sessions never share a link.
func SSHAgentLinkPath(socketPath, sessionID string) string {
	return filepath.Join(filepath.Dir(socketPath), agentLinkPrefix(socketPath)+sessionID+".sock")
}

func hostAgentLinkPath(socketPath, host string) string {
	return filepath.Join(filepath.Dir(socketPath), agentLinkPrefix(socketPath)+hostLinkName(host)+".sock")
}

const hostLinkKeep = 32

func hostLinkName(host string) string {
	sum := sha256.Sum256([]byte(host))
	name := strings.ToLower(host)
	if len(name) > hostLinkKeep {
		name = name[:hostLinkKeep]
	}
	return "link-" + name + "-" + hex.EncodeToString(sum[:4])
}

var hostLinkPattern = lazyre.New(`^link-[a-z0-9][a-z0-9._-]{0,31}-[0-9a-f]{8}$`)

func agentLinkPrefix(socketPath string) string {
	base := filepath.Base(socketPath)
	if base == "tuios.sock" {
		return "agent-"
	}
	return strings.TrimSuffix(base, ".sock") + "-agent-"
}

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
	return isSessionID(rest)
}

func (d *Daemon) sweepAgentLinks() {
	d.sweepOwnLinks(isOwnAgentLink)
}

func (d *Daemon) stopSSHAgent() {
	d.sshAgent.mu.Lock()
	d.sshAgent.stopped = true
	d.sshAgent.bySession = nil
	d.sshAgent.target = nil
	d.sshAgent.mu.Unlock()
	d.sweepAgentLinks()
}

func (d *Daemon) startSSHAgent() {
	d.sweepAgentLinks()
	d.initSSHAgent()
}

func (d *Daemon) initSSHAgent() {
	fallback, _ := resolveAgentSocket(os.Getenv("SSH_AUTH_SOCK"))
	d.sshAgent.mu.Lock()
	d.sshAgent.fallback = fallback
	d.sshAgent.mu.Unlock()
	if !d.sshAgentFollowing() {
		return
	}
	n := max(len(SSHAgentLinkPath(d.manager.SocketPath(), "00000000-0000-0000-0000-000000000000")),
		len(hostAgentLinkPath(d.manager.SocketPath(), strings.Repeat("h", hostLinkKeep))))
	if n > sockpath.MaxLen() {
		log.Printf("Warning: the ssh agent links are %d characters long, and a Unix socket path can be at most %d. ssh in a pane cannot reach the agent. Set XDG_RUNTIME_DIR to a shorter folder.", n, sockpath.MaxLen())
	}
}

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

func (d *Daemon) agentEnsureSession(sessionID string) {
	if !d.sshAgentFollowing() {
		return
	}
	d.sshAgent.mu.Lock()
	defer d.sshAgent.mu.Unlock()
	d.relinkAgentLocked(sessionID)
}

func (d *Daemon) agentLinkPath(sessionID string) string {
	return SSHAgentLinkPath(d.manager.SocketPath(), sessionID)
}

func hostAgentKey(host string) string { return "host:" + host }

func (d *Daemon) agentLinkFor(key string) string {
	if host, ok := strings.CutPrefix(key, "host:"); ok {
		return hostAgentLinkPath(d.manager.SocketPath(), host)
	}
	return d.agentLinkPath(key)
}

func keyName(key string) string {
	if host, ok := strings.CutPrefix(key, "host:"); ok {
		return "host " + host
	}
	return "session " + shortID(key)
}

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

func (d *Daemon) agentNoteUse(cs *connState, sessionID string) {
	if sessionID == "" {
		return
	}
	d.agentNote(cs, agentSockOf(cs), sessionID)
}

func (d *Daemon) agentNote(cs *connState, sock string, keys ...string) {
	if sock == "" || !d.sshAgentFollowing() || !d.mayActAsHuman(cs) {
		return
	}
	f := &d.sshAgent
	f.mu.Lock()
	defer f.mu.Unlock()
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

func (d *Daemon) linkSSHEnv(h federation.Host) []string {
	if !d.sshAgentFollowing() || !d.hostForwardsAgent(h) {
		return nil
	}
	key := hostAgentKey(h.Name)
	d.agentEnsureSession(key)
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "SSH_AUTH_SOCK=") })
	return append(env, "SSH_AUTH_SOCK="+d.agentLinkFor(key))
}

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

func (d *Daemon) forgetHostForwards() {
	d.sshAgent.mu.Lock()
	d.sshAgent.forwards = nil
	d.sshAgent.mu.Unlock()
	federation.ForgetSSHConfig()
}

func sshConfigForwards(h federation.Host) (fwd, ok bool) {
	c, ok := federation.ReadSSHConfig(h)
	if !ok || c.ForwardAgent == "" {
		return false, false
	}
	return strings.EqualFold(c.ForwardAgent, "yes"), true
}

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

func (d *Daemon) relinkAgentLocked(sessionID string) {
	f := &d.sshAgent
	if f.stopped {
		return
	}
	list := f.bySession[sessionID]
	for len(list) > 0 && !stillOwnedSocket(list[0].sock) {
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
	} else if f.fallback != "" && stillOwnedSocket(f.fallback) {
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

func (d *Daemon) verbSSHAgentPath(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if strings.TrimSpace(p.Session) == "" {
		return nil, invalidParam("session", "session is the session whose agent link to print and cannot be empty")
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
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
