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
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
	"github.com/Gaurav-Gosain/tuios/internal/sockpath"
	"github.com/google/uuid"
)

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

func (c ClientDisplay) empty() bool {
	return c.WaylandDisplay == "" && c.Display == ""
}

type shownDisplay struct {
	wayland string
	x11     string
	xauth   string
}

type displayCandidate struct {
	clientID string
	shown    shownDisplay
}

type displayFollow struct {
	mu        sync.Mutex
	bySession map[string][]displayCandidate
	target    map[string]string
	stopped   bool
}

func daemonHasDisplay() bool {
	return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != ""
}

var x11DisplayPattern = lazyre.New(`^:([0-9]{1,5})(\.[0-9]{1,3})?$`)

const x11SocketDir = "/tmp/.X11-unix"

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
	if uid, ok := procinfo.FileOwner(fi); !ok || (uid != me && uid != 0) {
		return false
	}
	return safeFolders(filepath.Dir(path), me)
}

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
	if uid, ok := procinfo.FileOwner(fi); !ok || uid != me {
		return "", false
	}
	if !safeFolders(filepath.Dir(resolved), me) {
		return "", false
	}
	return resolved, true
}

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

func stillShown(s shownDisplay) bool {
	return (s.wayland != "" && stillOwnedSocket(s.wayland)) || (s.x11 != "" && x11SocketOK(s.x11))
}

// DisplayLinkPath is the stable Wayland link of the session with the given
// id, beside the daemon socket at socketPath.
func DisplayLinkPath(socketPath, sessionID string) string {
	return filepath.Join(filepath.Dir(socketPath), displayLinkPrefix(socketPath)+sessionID+".sock")
}

func displayLinkPrefix(socketPath string) string {
	base := filepath.Base(socketPath)
	if base == "tuios.sock" {
		return "wayland-"
	}
	return strings.TrimSuffix(base, ".sock") + "-wayland-"
}

func isOwnDisplayLink(socketPath, name string) bool {
	rest, ok := strings.CutPrefix(name, displayLinkPrefix(socketPath))
	if !ok {
		return false
	}
	rest, ok = strings.CutSuffix(rest, ".sock")
	if !ok {
		return false
	}
	return isSessionID(rest)
}

func (d *Daemon) sweepDisplayLinks() {
	d.sweepOwnLinks(isOwnDisplayLink)
}

func (d *Daemon) sweepOwnLinks(own func(socketPath, name string) bool) {
	sock := d.manager.SocketPath()
	entries, err := os.ReadDir(filepath.Dir(sock))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink == 0 || !own(sock, e.Name()) {
			continue
		}
		_ = os.Remove(filepath.Join(filepath.Dir(sock), e.Name()))
	}
}

func isSessionID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil && len(s) == 36
}

func (d *Daemon) startDisplayFollow() {
	d.sweepDisplayLinks()
	d.initDisplayFollow()
}

func (d *Daemon) initDisplayFollow() {
	if !d.displayFollowing() {
		return
	}
	if n := len(DisplayLinkPath(d.manager.SocketPath(), "00000000-0000-0000-0000-000000000000")); n > sockpath.MaxLen() {
		log.Printf("Warning: the Wayland links are %d characters long, and a Unix socket path can be at most %d. Wayland programs in a pane cannot reach the display. Set XDG_RUNTIME_DIR to a shorter folder.", n, sockpath.MaxLen())
	}
}

func (d *Daemon) stopDisplayFollow() {
	d.display.mu.Lock()
	d.display.stopped = true
	d.display.bySession = nil
	d.display.target = nil
	d.display.mu.Unlock()
	d.sweepDisplayLinks()
}

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

func (d *Daemon) relinkDisplayLocked(sessionID string) {
	f := &d.display
	if f.stopped {
		return
	}
	list := slices.DeleteFunc(f.bySession[sessionID], func(c displayCandidate) bool { return !stillShown(c.shown) })
	if len(list) == 0 {
		delete(f.bySession, sessionID)
	} else {
		f.bySession[sessionID] = list
	}
	link := DisplayLinkPath(d.manager.SocketPath(), sessionID)
	want, by := "", ""
	if i := slices.IndexFunc(list, func(c displayCandidate) bool { return c.shown.wayland != "" }); i >= 0 {
		want, by = list[i].shown.wayland, list[i].clientID
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
		LogBasic("Could not point the Wayland link of session %s at the socket of client %s: %v", shortID(sessionID), by, err)
		return
	}
	if f.target == nil {
		f.target = make(map[string]string)
	}
	f.target[sessionID] = want
	LogBasic("Pointed the Wayland link of session %s at the socket of client %s", shortID(sessionID), by)
}

func (d *Daemon) displayVars(sessionID string) []string {
	if sessionID == "" || !d.displayFollowing() {
		return nil
	}
	f := &d.display
	f.mu.Lock()
	defer f.mu.Unlock()
	list := f.bySession[sessionID]
	x := slices.IndexFunc(list, func(c displayCandidate) bool { return c.shown.x11 != "" })
	var env []string
	if _, linked := f.target[sessionID]; linked || x < 0 {
		env = append(env, "WAYLAND_DISPLAY="+DisplayLinkPath(d.manager.SocketPath(), sessionID))
	}
	if x >= 0 {
		s := list[x].shown
		env = append(env, "DISPLAY="+s.x11)
		if s.xauth != "" {
			env = append(env, "XAUTHORITY="+s.xauth)
		}
	}
	return env
}

func (m *Manager) displayEnv(sessionID string) []string {
	if src := m.displaySource.Load(); src != nil {
		return (*src)(sessionID)
	}
	return nil
}

var displayVarNames = []string{"WAYLAND_DISPLAY", "DISPLAY", "XAUTHORITY"}

func (d *Daemon) verbDisplayEnv(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if strings.TrimSpace(p.Session) == "" {
		return nil, invalidParam("session", "session is the session whose display to print and cannot be empty")
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
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
