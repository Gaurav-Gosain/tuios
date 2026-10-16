package session

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
	"github.com/Gaurav-Gosain/tuios/internal/sockpath"
	"github.com/google/uuid"
)

// Following the attached client's display.
//
// A daemon that a host link started over ssh has no WAYLAND_DISPLAY or
// DISPLAY, so its panes have none either. When the person later attaches
// from the desktop of that machine, wl-copy, wl-paste, xclip and a coding
// agent's image paste fail in every pane. With [daemon] display = "follow",
// the default, the daemon gives its panes the display of the local client
// that attached to the session or used it last. It is modelled on the ssh
// agent follow (ssh_agent_follow.go):
//
//   - It does nothing when the daemon has a WAYLAND_DISPLAY or a DISPLAY of
//     its own. A daemon started from the desktop keeps what it has, and its
//     panes see exactly what they saw before the option existed.
//   - A client sends WAYLAND_DISPLAY, XDG_RUNTIME_DIR, DISPLAY and XAUTHORITY
//     in its hello (ClientDisplay). Only tuios attach and tuios new send them.
//   - Each session has one link, wayland-<id>.sock beside the daemon socket,
//     that points at the Wayland socket of the newest client that has one.
//     A new pane started while the session has such a client gets
//     WAYLAND_DISPLAY set to the link's absolute path, which libwayland
//     accepts as is. The link moves when a client comes or goes, so a pane
//     started under one desktop client still reaches the compositor after
//     that client detaches and another attaches.
//   - DISPLAY is a name, :N, and cannot be a link. A new pane gets the
//     newest client's DISPLAY and XAUTHORITY as they are. A shell that
//     already runs keeps its values, and tuios display-env prints the current
//     ones for eval in that shell or a shell rc.
//   - When the client leaves, the session falls back to the client before
//     it. With no client left the link is removed. The daemon has no display
//     of its own to fall back to: if it had one, the follow would be off.
//   - Links are swept at start and stop, and when the option is turned off.
//
// Which clients count. Every rule below fails closed:
//
//   - A client on this daemon's own socket only. A connection over a host
//     link is never followed: its values name a display on another machine.
//   - A client of this daemon's uid, by the kernel's peer credentials. A
//     platform that does not give the uid follows no one.
//   - A client that may act as the person (human_origin.go). A client inside
//     a pane is an agent or a script, not the desktop.
//   - tuios's SSH server and tuios-web serve their clients from a TUIClient
//     that sends no display, so a remote viewer never moves anything.
//
// Which values count:
//
//   - WAYLAND_DISPLAY is an absolute path, or a plain name such as wayland-1
//     with no slash. A name is looked up in the client's XDG_RUNTIME_DIR, then
//     the daemon's, then /run/user/<uid>, so a client started with
//     XDG_RUNTIME_DIR unset still counts. The path is resolved once
//     (filepath.EvalSymlinks) and must pass ownedSocket: a Unix socket this
//     user owns, in folders no other user can write to unless sticky.
//   - DISPLAY is :N or :N.S, a display on this machine. Its socket,
//     /tmp/.X11-unix/XN, must be a Unix socket owned by this user or root
//     (Xwayland or Xorg), in folders no other user can write to unless
//     sticky. A forwarded display such as localhost:10.0 does not count.
//   - XAUTHORITY is an absolute path that resolves to a regular file this
//     user owns, in folders no other user can write to unless sticky.
//   - No value may hold a control character.
//
// A client whose values all fail sends nothing that counts and moves
// nothing. The refusal is logged once per connection.

// ClientDisplay is the display environment a local client runs in, sent in
// its hello. Every field is the variable as the client has it, unchecked: the
// daemon checks them. See display_follow.go.
type ClientDisplay struct {
	WaylandDisplay string `json:"wayland_display,omitempty"`
	XDGRuntimeDir  string `json:"xdg_runtime_dir,omitempty"`
	Display        string `json:"display,omitempty"`
	XAuthority     string `json:"xauthority,omitempty"`
}

// ClientDisplayFromEnv reads the client's display environment.
func ClientDisplayFromEnv() ClientDisplay {
	return ClientDisplay{
		WaylandDisplay: os.Getenv("WAYLAND_DISPLAY"),
		XDGRuntimeDir:  os.Getenv("XDG_RUNTIME_DIR"),
		Display:        os.Getenv("DISPLAY"),
		XAuthority:     os.Getenv("XAUTHORITY"),
	}
}

// empty reports whether the client has no display at all.
func (c ClientDisplay) empty() bool {
	return c.WaylandDisplay == "" && c.Display == ""
}

// shownDisplay is what passed the checks: the resolved Wayland socket, the X11
// display name and the resolved X authority file. Any of them may be empty,
// but not both wayland and x11.
type shownDisplay struct {
	wayland string
	x11     string
	xauth   string
}

// displayCandidate is one client's display for one session.
type displayCandidate struct {
	clientID string
	shown    shownDisplay
}

// displayFollow holds, for each session, the clients whose display the panes
// can be given, newest first, and where each Wayland link points now.
type displayFollow struct {
	mu        sync.Mutex
	bySession map[string][]displayCandidate
	target    map[string]string
	// stopped is set when the daemon stops: no link is made after it.
	stopped bool
}

// daemonHasDisplay reports whether the daemon's own environment names a
// display. The follow is off for such a daemon.
func daemonHasDisplay() bool {
	return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != ""
}

// x11DisplayPattern is a display on this machine: :N or :N.S.
var x11DisplayPattern = lazyre.New(`^:([0-9]{1,5})(\.[0-9]{1,3})?$`)

// x11SocketDir is where an X server on this machine listens.
const x11SocketDir = "/tmp/.X11-unix"

// resolveWayland finds the client's Wayland socket and checks it.
func resolveWayland(c ClientDisplay) (string, bool) {
	name := c.WaylandDisplay
	if name == "" || hasControl(name) {
		return "", false
	}
	if filepath.IsAbs(name) {
		return resolveAgentSocket(name)
	}
	if strings.ContainsRune(name, '/') {
		return "", false
	}
	var dirs []string
	for _, dir := range []string{c.XDGRuntimeDir, os.Getenv("XDG_RUNTIME_DIR"), "/run/user/" + strconv.Itoa(os.Geteuid())} {
		if dir != "" && filepath.IsAbs(dir) && !hasControl(dir) && !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	for _, dir := range dirs {
		if resolved, ok := resolveAgentSocket(filepath.Join(dir, name)); ok {
			return resolved, true
		}
	}
	return "", false
}

// x11SocketOK reports whether the X11 display name names a server on this
// machine whose socket this user or root owns, in safe folders.
func x11SocketOK(display string) bool {
	m := x11DisplayPattern().FindStringSubmatch(display)
	if m == nil {
		return false
	}
	path := filepath.Join(x11SocketDir, "X"+m[1])
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSocket == 0 {
		return false
	}
	me := os.Geteuid()
	if uid, ok := fileOwner(fi); !ok || (uid != me && uid != 0) {
		return false
	}
	return safeFolders(filepath.Dir(path), me)
}

// safeFolders reports whether dir and every folder above it pass safeFolder.
func safeFolders(dir string, me int) bool {
	for dir = filepath.Clean(dir); ; dir = filepath.Dir(dir) {
		if !safeFolder(dir, me) {
			return false
		}
		if dir == filepath.Dir(dir) {
			return true
		}
	}
}

// resolveXAuthority resolves the client's X authority file and checks it: a
// regular file this user owns, in safe folders.
func resolveXAuthority(path string) (string, bool) {
	if path == "" || !filepath.IsAbs(path) || hasControl(path) {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	fi, err := os.Lstat(resolved)
	if err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	me := os.Geteuid()
	if uid, ok := fileOwner(fi); !ok || uid != me {
		return "", false
	}
	if !safeFolders(filepath.Dir(resolved), me) {
		return "", false
	}
	return resolved, true
}

// checkDisplay keeps the parts of c that pass the checks. ok is false when
// neither a Wayland socket nor an X11 display is left.
func checkDisplay(c ClientDisplay) (shownDisplay, bool) {
	var s shownDisplay
	if w, ok := resolveWayland(c); ok {
		s.wayland = w
	}
	if c.Display != "" && !hasControl(c.Display) && x11SocketOK(c.Display) {
		s.x11 = c.Display
		if xa, ok := resolveXAuthority(c.XAuthority); ok {
			s.xauth = xa
		}
	}
	return s, s.wayland != "" || s.x11 != ""
}

// stillShown reports whether a display that passed the checks still does.
func stillShown(s shownDisplay) bool {
	return (s.wayland != "" && stillOwnedSocket(s.wayland)) || (s.x11 != "" && x11SocketOK(s.x11))
}

// DisplayLinkPath is the stable Wayland link of the session with the given
// id, beside the daemon socket at socketPath.
func DisplayLinkPath(socketPath, sessionID string) string {
	return filepath.Join(filepath.Dir(socketPath), displayLinkPrefix(socketPath)+sessionID+".sock")
}

// displayLinkPrefix is how the Wayland links of the daemon on socketPath
// start: wayland- for the default socket, and named after the socket for a
// daemon on another socket in the same folder.
func displayLinkPrefix(socketPath string) string {
	base := filepath.Base(socketPath)
	if base == "tuios.sock" {
		return "wayland-"
	}
	return strings.TrimSuffix(base, ".sock") + "-wayland-"
}

// isOwnDisplayLink reports whether name, a file in the socket folder, is one
// of this daemon's Wayland links: the prefix, a whole session id, .sock.
func isOwnDisplayLink(socketPath, name string) bool {
	rest, ok := strings.CutPrefix(name, displayLinkPrefix(socketPath))
	if !ok {
		return false
	}
	rest, ok = strings.CutSuffix(rest, ".sock")
	if !ok {
		return false
	}
	_, err := uuid.Parse(rest)
	return err == nil && len(rest) == 36
}

// sweepDisplayLinks removes every Wayland link of this daemon in the socket
// folder. Only symlinks are removed.
func (d *Daemon) sweepDisplayLinks() {
	sock := d.manager.SocketPath()
	entries, err := os.ReadDir(filepath.Dir(sock))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink == 0 || !isOwnDisplayLink(sock, e.Name()) {
			continue
		}
		_ = os.Remove(filepath.Join(filepath.Dir(sock), e.Name()))
	}
}

// startDisplayFollow sweeps the links an earlier daemon left and warns when a
// link path is too long to connect to. It runs once, before any session is
// restored.
func (d *Daemon) startDisplayFollow() {
	d.sweepDisplayLinks()
	d.initDisplayFollow()
}

// initDisplayFollow is startDisplayFollow without the sweep, for a daemon
// image adopting its predecessor's panes, whose WAYLAND_DISPLAY names the
// links.
func (d *Daemon) initDisplayFollow() {
	if !d.displayFollowing() {
		return
	}
	if n := len(DisplayLinkPath(d.manager.SocketPath(), "00000000-0000-0000-0000-000000000000")); n > sockpath.MaxLen() {
		log.Printf("Warning: the Wayland links are %d characters long, and a Unix socket path can be at most %d. Wayland programs in a pane cannot reach the display. Set XDG_RUNTIME_DIR to a shorter folder.", n, sockpath.MaxLen())
	}
}

// stopDisplayFollow runs at the end of shutdown: no link is made after it,
// and every link is removed.
func (d *Daemon) stopDisplayFollow() {
	d.display.mu.Lock()
	d.display.stopped = true
	d.display.bySession = nil
	d.display.target = nil
	d.display.mu.Unlock()
	d.sweepDisplayLinks()
}

// displayFollowing reports whether the follow is on: [daemon] display is
// follow and the daemon has no display of its own.
func (d *Daemon) displayFollowing() bool { return d.manager.displayFollow.Load() }

// SetDisplay applies [daemon] display. Turning it off removes every link and
// forgets every client, so a new pane gets the daemon's environment again.
func (d *Daemon) SetDisplay(mode string) {
	on := config.DisplayFollows(mode) && !daemonHasDisplay()
	was := d.manager.displayFollow.Swap(on)
	if was && !on {
		d.display.mu.Lock()
		d.display.bySession = nil
		d.display.target = nil
		d.display.mu.Unlock()
		d.sweepDisplayLinks()
	}
}

// displayOf is the display the client on cs sent, and whether the client may
// be followed at all: a local, same-uid client that may act as the person.
func (d *Daemon) displayOf(cs *connState) (ClientDisplay, bool) {
	cs.mu.Lock()
	viaLink, hello := cs.viaLink, cs.hello
	cs.mu.Unlock()
	if viaLink || hello == nil || hello.Display.empty() {
		return ClientDisplay{}, false
	}
	if !cs.peerUIDOK || cs.peerUID != os.Geteuid() {
		return ClientDisplay{}, false
	}
	if !d.mayActAsHuman(cs) {
		return ClientDisplay{}, false
	}
	return hello.Display, true
}

// displayNoteUse records that the client on cs attached to or used the
// session with the given id, and gives the session the client's display when
// it counts.
func (d *Daemon) displayNoteUse(cs *connState, sessionID string) {
	if sessionID == "" || !d.displayFollowing() {
		return
	}
	raw, ok := d.displayOf(cs)
	if !ok {
		return
	}
	f := &d.display
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-cs.done:
		return
	default:
	}
	list := f.bySession[sessionID]
	if len(list) > 0 && list[0].clientID == cs.clientID {
		return
	}
	shown, ok := checkDisplay(raw)
	if !ok {
		if cs.displayRefused.CompareAndSwap(false, true) {
			LogBasic("Client %s (pid %d) sent a display that is not followed: the Wayland socket or the X11 socket must be one this user owns, in folders no other user can write to", cs.clientID, cs.peerPID)
		}
		return
	}
	list = slices.DeleteFunc(list, func(c displayCandidate) bool { return c.clientID == cs.clientID })
	list = append([]displayCandidate{{clientID: cs.clientID, shown: shown}}, list...)
	if f.bySession == nil {
		f.bySession = make(map[string][]displayCandidate)
	}
	f.bySession[sessionID] = list
	d.relinkDisplayLocked(sessionID)
}

// displayForget drops the client from the session's candidates and moves the
// link to the client before it.
func (d *Daemon) displayForget(sessionID, clientID string) {
	f := &d.display
	f.mu.Lock()
	defer f.mu.Unlock()
	list, ok := f.bySession[sessionID]
	if !ok {
		return
	}
	list = slices.DeleteFunc(list, func(c displayCandidate) bool { return c.clientID == clientID })
	if len(list) == 0 {
		delete(f.bySession, sessionID)
	} else {
		f.bySession[sessionID] = list
	}
	d.relinkDisplayLocked(sessionID)
}

// displayForgetSession removes the session's link when the session ends.
func (d *Daemon) displayForgetSession(sessionID string) {
	f := &d.display
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.target[sessionID]; ok {
		_ = os.Remove(DisplayLinkPath(d.manager.SocketPath(), sessionID))
		delete(f.target, sessionID)
	}
	delete(f.bySession, sessionID)
}

// relinkDisplayLocked drops the newest candidates whose display no longer
// passes the checks, then points the session's link at the newest one's
// Wayland socket, or removes the link when it has none. f.mu is held.
func (d *Daemon) relinkDisplayLocked(sessionID string) {
	f := &d.display
	if f.stopped {
		return
	}
	list := f.bySession[sessionID]
	for len(list) > 0 && !stillShown(list[0].shown) {
		list = list[1:]
	}
	if len(list) == 0 {
		delete(f.bySession, sessionID)
	} else {
		f.bySession[sessionID] = list
	}
	link := DisplayLinkPath(d.manager.SocketPath(), sessionID)
	want := ""
	if len(list) > 0 {
		want = list[0].shown.wayland
	}
	if want == "" {
		if _, ok := f.target[sessionID]; ok {
			_ = os.Remove(link)
			delete(f.target, sessionID)
			LogBasic("Removed the Wayland link of session %s: no attached client has a Wayland display", shortID(sessionID))
		}
		return
	}
	if f.target[sessionID] == want {
		if cur, err := os.Readlink(link); err == nil && cur == want {
			return
		}
	}
	if err := replaceSymlink(want, link); err != nil {
		LogBasic("Could not point the Wayland link of session %s at the socket of client %s: %v", shortID(sessionID), list[0].clientID, err)
		return
	}
	if f.target == nil {
		f.target = make(map[string]string)
	}
	f.target[sessionID] = want
	LogBasic("Pointed the Wayland link of session %s at the socket of client %s", shortID(sessionID), list[0].clientID)
}

// displayVars is the session's display as pane variables: WAYLAND_DISPLAY
// naming the link while it points somewhere, and the newest client's DISPLAY
// and XAUTHORITY. Nil when the follow is off or no client counts.
func (d *Daemon) displayVars(sessionID string) []string {
	if sessionID == "" || !d.displayFollowing() {
		return nil
	}
	f := &d.display
	f.mu.Lock()
	defer f.mu.Unlock()
	list := f.bySession[sessionID]
	if len(list) == 0 {
		return nil
	}
	var env []string
	if _, linked := f.target[sessionID]; linked {
		env = append(env, "WAYLAND_DISPLAY="+DisplayLinkPath(d.manager.SocketPath(), sessionID))
	}
	if s := list[0].shown; s.x11 != "" {
		env = append(env, "DISPLAY="+s.x11)
		if s.xauth != "" {
			env = append(env, "XAUTHORITY="+s.xauth)
		}
	}
	return env
}

// displayEnv is what a new pane of the session is given. See displayVars.
func (m *Manager) displayEnv(sessionID string) []string {
	if src := m.displaySource.Load(); src != nil {
		return (*src)(sessionID)
	}
	return nil
}

// displayVarNames are the variables the display follow sets.
var displayVarNames = []string{"WAYLAND_DISPLAY", "DISPLAY", "XAUTHORITY"}

// verbDisplayEnv reports the display a new pane of the session gets now.
func (d *Daemon) verbDisplayEnv(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if strings.TrimSpace(p.Session) == "" {
		return nil, invalidParam("session", "session is the session whose display to print and cannot be empty")
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	out := map[string]any{
		"type":    "display_env",
		"session": sess.Name(),
		"follow":  d.displayFollowing(),
		"own":     daemonHasDisplay(),
		"link":    DisplayLinkPath(d.manager.SocketPath(), sess.ID),
	}
	vars := map[string]string{}
	for _, kv := range d.displayVars(sess.ID) {
		k, v, _ := strings.Cut(kv, "=")
		vars[k] = v
	}
	out["env"] = vars
	d.display.mu.Lock()
	if target, ok := d.display.target[sess.ID]; ok {
		out["target"] = target
	}
	d.display.mu.Unlock()
	return out, nil
}
