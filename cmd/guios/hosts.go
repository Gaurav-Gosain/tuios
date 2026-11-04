package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/guios/remote"
	"github.com/Gaurav-Gosain/tuios/internal/guios/settings"
	"github.com/Gaurav-Gosain/tuios/internal/guios/update"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// host is a kerod the app shows: this machine's, first, then those of
// the machines Settings › Hosts lists (the configuration's host keys),
// each with a connection of its own, so that one far away, or gone,
// holds up no other.
type host struct {
	name string        // "" for this machine
	cfg  settings.Host // a remote host's, as configured
	addr remote.Addr

	rc        *remote.Conn // a remote host's connection, nil while there is none
	cl        *client.Client
	conn      connState
	err       string // why the last connection failed
	errKind   error  // remote.ErrNotInstalled, remote.ErrAuth, else nil
	daemonApp string // the version of Kero kerod runs
	daemonFP  string // what its code is made of (proto.Welcome.Build)
	info      proto.HostInfo
	retries   int
	gen       int    // connection attempts: the results of one another followed are dropped
	dialing   bool   // a connection attempt is under way
	kerod     string // which kerod it is (kerodID), once connected
	loaded    bool   // a state came
	// convs are the conversations the host's kerod remembers, for the
	// sidebar, as of its count of their changes convGen; convAsked tells
	// that they're asked for.
	convs     []proto.Conversation
	convGen   uint64
	convKnown bool
	convAsked bool
	viewSent  *string
	// busy is what the app does on the host for the user: install, update
	// or restart.
	busy string
}

func (h *host) remote() bool { return h.name != "" }

// dialer opens connections to the host's kerod: this machine's socket,
// or a remote host's connection, never this machine's for a host the app
// lost.
func (h *host) dialer() client.Dialer {
	switch {
	case h.rc != nil:
		return h.rc
	case h.remote():
		return lostDialer(h.name)
	}
	return client.Local{NoStart: true}
}

// lostDialer is the dialer of a host the app has no connection to.
type lostDialer string

func (d lostDialer) Dial() (net.Conn, error) {
	return nil, errors.New(locales.HostNotConnected(string(d)))
}

// label names the host for the user: its name, "This Computer" for this
// one.
func (h *host) label() string {
	if !h.remote() {
		return locales.HostThisComputer()
	}
	return h.name
}

// home is the host's home directory, as kerod there told.
func (h *host) home() string {
	if !h.remote() {
		return proto.Home()
	}
	return h.info.Home
}

// windows reports whether the host runs Windows, whose paths have drives
// and backslashes.
func (h *host) windows() bool {
	if !h.remote() {
		return runtime.GOOS == "windows"
	}
	return h.info.OS == "windows"
}

// shortDir writes a directory of the host with ~ for its home.
func (h *host) shortDir(dir string) string {
	if !h.remote() {
		return proto.ShortDir(dir)
	}
	sep, same := "/", func(a, b string) bool { return a == b }
	if h.windows() {
		sep, same = `\`, strings.EqualFold
	}
	home := strings.TrimSuffix(h.info.Home, sep)
	switch {
	case home == "":
	case same(dir, home):
		return "~"
	case len(dir) > len(home) && same(dir[:len(home)], home) && dir[len(home):len(home)+1] == sep:
		return "~" + dir[len(home):]
	}
	return dir
}

// joinPath joins a directory of the host and a name in it.
func (h *host) joinPath(dir, name string) string {
	if h.windows() {
		return strings.TrimRight(dir, `\/`) + `\` + name
	}
	return path.Join(dir, name)
}

// stale reports whether kerod runs another version of Kero than the one
// installed on the host, as after an update, or, for a remote host, than
// this app.
func (h *host) stale() bool {
	if h.conn != connReady || h.daemonApp == "" {
		return false
	}
	if h.remote() {
		// Older than the bridge that answered, which a later update of the
		// host leaves behind it, never ahead of it.
		return h.info.Kero != "" && update.Compare(h.daemonApp, h.info.Kero) < 0
	}
	return kerodStale(h.daemonApp, h.daemonFP)
}

// kero is the version of Kero on a remote host: kerod's, else the one
// that answered.
func (h *host) kero() string {
	if h.daemonApp != "" {
		return h.daemonApp
	}
	return h.info.Kero
}

// behind reports whether Kero on a remote host is older than this app,
// of a server build that updates itself, which the app offers to update
// to its own version: never back from a newer one.
func (h *host) behind() bool {
	return h.remote() && h.info.Updates && released(mygo.App.Version()) && h.kero() != "" &&
		update.Compare(h.kero(), mygo.App.Version()) < 0
}

// ahead reports whether Kero on a remote host is newer than this app,
// which then wants updating itself.
func (h *host) ahead() bool {
	return h.remote() && h.kero() != "" && released(h.kero()) && released(mygo.App.Version()) &&
		update.Compare(h.kero(), mygo.App.Version()) > 0
}

// relaunchedEnv names the version of kerod the app relaunched for, which
// the app it starts again inherits: should the app on disk be no newer
// after all, it relaunches once, not on and on.
const relaunchedEnv = "GUIOS_RELAUNCHED_FOR"

// relaunchForKerod relaunches the app into a newer Kero that this
// machine's kerod runs, as after Kero on another machine updated the app
// here (remote-updates) and kerod restarted into it.
func relaunchForKerod(v string) {
	app := mygo.App.Version()
	if !released(v) || !released(app) || update.Compare(v, app) <= 0 ||
		!mygo.Updater.Enabled() || os.Getenv(relaunchedEnv) == v {
		return
	}
	os.Setenv(relaunchedEnv, v)
	mygo.App.Relaunch()
}

// released tells a version of Kero that has a release to install: not a
// development build's.
func released(v string) bool { return v != "" && !strings.Contains(v, "dev") && !mygo.IsDev() }

func (a *App) local() *host { return a.hosts[0] }

// hostOf returns the host of a session, a pane or a project, by id: this
// machine when none has it.
func (a *App) hostOf(id string) *host {
	if h := a.owner[id]; h != nil {
		return h
	}
	return a.local()
}

// hostNamed returns a remote host, by name, else nil.
func (a *App) hostNamed(name string) *host {
	for _, h := range a.hosts[1:] {
		if strings.EqualFold(h.name, name) {
			return h
		}
	}
	return nil
}

// selectedHost is the host of the session shown, this machine's when
// none is.
func (a *App) selectedHost() *host { return a.hostOf(a.selected) }

// syncHosts follows the configuration's hosts: it connects to those added,
// lets go of those removed, and again of those whose address changed.
func (a *App) syncHosts() {
	keep := []*host{a.local()}
	for _, cfg := range a.set.Hosts {
		addr, err := remote.ParseAddr(cfg.Addr)
		h := a.hostNamed(cfg.Name)
		if h != nil && (err != nil || addr != h.addr) {
			a.closeHost(h)
			h = nil
		}
		if h == nil {
			h = &host{name: cfg.Name, cfg: cfg, addr: addr, conn: connConnecting}
			if err != nil {
				h.conn, h.err = connFailed, err.Error()
			}
		}
		h.cfg, h.name = cfg, cfg.Name
		keep = append(keep, h)
	}
	for _, h := range a.hosts[1:] {
		if !slices.Contains(keep, h) {
			a.closeHost(h)
		}
	}
	added := []*host{}
	for _, h := range keep[1:] {
		if !slices.Contains(a.hosts, h) {
			added = append(added, h)
		}
	}
	a.hosts = keep
	// The sessions shown follow the hosts' order.
	a.mergeState(a.local(), proto.State{Projects: a.projectsOf(a.local()), Sessions: a.sessionsOf(a.local())})
	for _, h := range added {
		if h.err == "" {
			a.connectHost(h)
		}
	}
	// The session shown may have gone with its host.
	a.reselect()
	a.updateTitle()
}

// closeHost lets go of a host: its connection, its terminals, and its
// sessions in the sidebar.
func (a *App) closeHost(h *host) {
	h.gen++
	a.dropTerms(h)
	if h.cl != nil {
		go h.cl.Close()
		h.cl = nil
	}
	if h.rc != nil {
		go h.rc.Close() // ssh takes a moment to end: never on the main thread
		h.rc = nil
	}
	a.mergeState(h, proto.State{})
	h.loaded = false
	// Its browser panes' pages, which this machine shows, go with it.
	for id := range a.webs {
		if a.sessionOfPane(id) == nil {
			a.dropWeb(id)
		}
	}
}

// connect connects to this machine's kerod, starting it.
func (a *App) connect() { a.connectHost(a.local()) }

// connectHost connects to a host's kerod: through the remote connection
// it has, else a new one.
func (a *App) connectHost(h *host) {
	h.gen++
	gen := h.gen
	if h.conn != connReady {
		h.conn = connConnecting
	}
	h.dialing = true
	rc := h.rc
	go func() {
		var cl *client.Client
		var err error
		if !h.remote() {
			cl, err = client.Connect(false)
		} else {
			if rc == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				rc, err = remote.Dial(ctx, h.addr, remote.Options{App: mygo.App.Version(), Client: appClientName()})
				cancel()
			}
			if err == nil {
				cl, err = client.ConnectVia(rc)
			}
		}
		a.update(func() { a.connected(h, gen, rc, cl, err) })
	}()
}

// appClientName says what the app is, for kero clients on a host.
func appClientName() string {
	s := "guios"
	if v := mygo.App.Version(); v != "" {
		s += " " + v
	}
	if name, err := os.Hostname(); err == nil {
		s += " on " + name
	}
	return s
}

// connected takes the result of connectHost.
func (a *App) connected(h *host, gen int, rc *remote.Conn, cl *client.Client, err error) {
	if h.gen != gen || !slices.Contains(a.hosts, h) {
		if cl != nil {
			go cl.Close()
		}
		if rc != nil && rc != h.rc {
			go rc.Close()
		}
		return
	}
	h.dialing = false
	if err == nil && h.remote() {
		if other := a.sameKerod(h, rc, cl); other != nil {
			// One kerod, reached twice, as over SSH and the tailnet: its
			// sessions would show twice, and pass for one another.
			go cl.Close()
			if rc != h.rc {
				go rc.Close()
			}
			cl, rc, err = nil, h.rc, errors.New(locales.HostSameAs(other.label()))
		}
	}
	if rc != nil && rc != h.rc {
		h.rc, h.info = rc, rc.Welcome.Host
		go func() {
			<-rc.Done()
			a.update(func() { a.lost(h, rc) })
		}()
	}
	if err != nil {
		h.cl = nil
		if errors.Is(err, client.ErrVersion) {
			h.conn, h.err, h.errKind = connVersion, "", nil
			return
		}
		h.conn, h.err, h.errKind = connFailed, err.Error(), nil
		for _, k := range []error{remote.ErrNotInstalled, remote.ErrAuth} {
			if errors.Is(err, k) {
				h.errKind = k
				h.err = strings.TrimPrefix(strings.TrimPrefix(err.Error(), k.Error()), ": ")
			}
		}
		h.retries++
		a.retryLater(h)
		// The session shown last may be of this host, which failed.
		if !h.loaded {
			a.reselect()
		}
		return
	}
	h.cl, h.conn, h.err, h.errKind, h.retries = cl, connReady, "", nil, 0
	h.kerod = kerodID(h, cl)
	h.daemonApp, h.daemonFP = cl.Welcome.App, cl.Welcome.Build
	if !h.remote() {
		relaunchForKerod(h.daemonApp)
	}
	cl.OnEvent(func(m proto.Message) { a.update(func() { a.event(h, cl, m) }) })
	cl.Go("subscribe", nil, nil)
	h.viewSent = nil
	a.reportView()
	go func() {
		<-cl.Done()
		a.update(func() { a.disconnected(h, cl) })
	}()
}

// retryLater connects to a host again after a failure: this machine's a
// few times, as kerod may be starting; another's for as long as the app
// runs, waiting longer each time, up to a minute, and a minute at once
// when it has no Kero, or refuses the app, which the user fixes.
func (a *App) retryLater(h *host) {
	gen := h.gen
	var wait time.Duration
	switch {
	case !h.remote():
		if h.retries >= 4 {
			return
		}
		wait = time.Duration(h.retries) * 400 * time.Millisecond
	case h.errKind != nil:
		wait = time.Minute
	default:
		wait = min(time.Second<<min(h.retries-1, 6), time.Minute)
	}
	time.AfterFunc(wait, func() {
		a.update(func() {
			if h.gen == gen && h.conn == connFailed && slices.Contains(a.hosts, h) {
				a.connectHost(h)
			}
		})
	})
}

// lost follows a remote connection that ended, as on a network gone, or
// a laptop that slept: the host's terminals and kerod are reached again
// through a new one.
func (a *App) lost(h *host, rc *remote.Conn) {
	if h.rc != rc {
		return
	}
	h.rc = nil
	go rc.Close()
	if h.cl == nil && h.conn != connFailed {
		// Nothing else follows the end: kerod was of another version.
		h.conn = connConnecting
		time.AfterFunc(time.Second, func() { a.update(func() { a.reconnect(h) }) })
	}
}

// reconnect connects to a host again, unless it is.
func (a *App) reconnect(h *host) {
	if slices.Contains(a.hosts, h) && h.cl == nil && h.busy == "" && !h.dialing {
		a.connectHost(h)
	}
}

// disconnected follows a host's kerod going away, as when it was
// restarted, or its connection ended: the terminals attached to it are
// done, and the app connects again.
func (a *App) disconnected(h *host, cl *client.Client) {
	if h.cl != cl {
		return
	}
	h.cl = nil
	h.conn = connConnecting
	a.dropTerms(h)
	// What changed while the app was away tells nothing new.
	for _, s := range a.sessionsOf(h) {
		delete(a.statuses, s.ID)
	}
	time.AfterFunc(300*time.Millisecond, func() { a.update(func() { a.reconnect(h) }) })
}

// dropTerms lets go of a host's terminals, which attach again as they
// show.
func (a *App) dropTerms(h *host) {
	for id := range a.terms {
		if a.hostOf(id) == h {
			a.dropTerm(id)
		}
	}
}

// sessionsOf returns a host's sessions, in the state the app shows.
func (a *App) sessionsOf(h *host) []proto.Session {
	var out []proto.Session
	for _, s := range a.st.Sessions {
		if a.hostOf(s.ID) == h {
			out = append(out, s)
		}
	}
	return out
}

func (a *App) projectsOf(h *host) []proto.Project {
	var out []proto.Project
	for _, p := range a.st.Projects {
		if a.hostOf(p.ID) == h {
			out = append(out, p)
		}
	}
	return out
}

// mergeState makes st a host's part of the state the app shows, the
// hosts' parts in their order: what the app changed in the others before
// their kerods' states come stays.
func (a *App) mergeState(h *host, st proto.State) {
	var projects []proto.Project
	var sessions []proto.Session
	owner := map[string]*host{}
	for _, o := range a.hosts {
		ps, ss := st.Projects, st.Sessions
		if o != h {
			ps, ss = a.projectsOf(o), a.sessionsOf(o)
		}
		for _, p := range ps {
			owner[p.ID] = o
		}
		for _, s := range ss {
			owner[s.ID] = o
			for _, p := range s.Panes {
				owner[p.ID] = o
			}
		}
		projects = append(projects, ps...)
		sessions = append(sessions, ss...)
	}
	a.st.Projects, a.st.Sessions, a.owner = projects, sessions, owner
}

// own records a session the app just made as a host's, before its
// kerod's state, which has it, comes.
func (a *App) own(h *host, s proto.Session) {
	if a.owner == nil {
		a.owner = map[string]*host{}
	}
	a.owner[s.ID] = h
	a.owner[s.Project] = h
	for _, p := range s.Panes {
		a.owner[p.ID] = h
	}
}

// remoteControl reports whether a host's programs may drive the app (kero
// focus, kero do): this machine's as Settings › Agents says, another's as
// Settings › Hosts does.
func (a *App) remoteControl(h *host) bool {
	if !h.remote() {
		return a.set.RemoteControl
	}
	return h.cfg.RemoteControl
}

// hostedName is a session's name, with its host's for a remote one, as
// a notification and the window's title say it, so that a host cannot
// pass for this machine.
func (a *App) hostedName(s *proto.Session) string {
	name := s.DisplayName(a.set.SessionName)
	if h := a.hostOf(s.ID); h.remote() {
		return name + " · " + h.name
	}
	return name
}

// allLoaded reports whether every host's state came, or failed to: the
// session the app showed last may be of one still connecting.
func (a *App) allLoaded() bool {
	for _, h := range a.hosts {
		if !h.loaded && h.conn == connConnecting {
			return false
		}
	}
	return true
}

// actionHost is the host an action runs for: the one whose program asked
// for it (kero do), else the shown session's.
func (a *App) actionHost() *host {
	if a.from != nil {
		return a.from
	}
	return a.selectedHost()
}

// kerodID tells a kerod apart from any other: its machine, its pid and
// when it started.
func kerodID(h *host, cl *client.Client) string {
	name := h.info.Hostname
	if !h.remote() {
		name, _ = os.Hostname()
	}
	return fmt.Sprintf("%s/%d/%d", name, cl.Welcome.PID, cl.Welcome.Started)
}

// sameKerod returns the host whose kerod a connection reaches already,
// as this machine's over ssh://localhost, or one host added twice; nil
// for none.
func (a *App) sameKerod(h *host, rc *remote.Conn, cl *client.Client) *host {
	name := h.info.Hostname
	if rc != nil {
		name = rc.Welcome.Host.Hostname
	}
	id := fmt.Sprintf("%s/%d/%d", name, cl.Welcome.PID, cl.Welcome.Started)
	for _, o := range a.hosts {
		if o != h && o.kerod == id {
			return o
		}
	}
	return nil
}

// neighbor is the session that takes a gone one's place: the next in order
// that is still there, else the one before. "" when id was not in order.
func neighbor(order []string, id string, have func(string) *proto.Session) string {
	i := slices.Index(order, id)
	if i < 0 {
		return ""
	}
	for _, x := range order[i+1:] {
		if have(x) != nil {
			return x
		}
	}
	for j := i - 1; j >= 0; j-- {
		if have(order[j]) != nil {
			return order[j]
		}
	}
	return ""
}

// reselect shows another session when the one shown is gone: the first,
// once every host's state came or failed to, as the one the app showed
// last may be of a host still connecting.
func (a *App) reselect() {
	if a.session(a.selected) != nil {
		return
	}
	ids := a.visibleOrder()
	switch {
	case len(ids) == 0 && a.allLoaded():
		a.selected = ""
	case len(ids) > 0 && (a.selected == "" || a.allLoaded()):
		a.selectSession(ids[0])
	}
}
