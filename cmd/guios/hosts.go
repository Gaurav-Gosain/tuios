package main

import (
	"errors"
	"fmt"
	"path"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/guios/settings"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// host is a tuios daemon the app shows: this machine's, first, then those
// of the machines this machine's daemon links (tuios hosts), in its order.
// The daemon holds the links: the app reaches a host's daemon through this
// machine's (client.ConnectHost), and reads how each link stands from
// list-hosts.
type host struct {
	name string        // "" for this machine
	cfg  settings.Host // the app's own choices for a remote host: its color
	// rep is how this machine's daemon says the host stands: its link, its
	// version and its load.
	rep       client.HostReport
	cl        *client.Client
	conn      connState
	err       string // why the host's sessions don't show
	daemonApp string // the tuios version its daemon runs
	daemonFP  string
	info      proto.HostInfo
	retries   int
	gen       int  // connection attempts: the results of one another followed are dropped
	dialing   bool // a connection attempt is under way
	loaded    bool // a state came
	// convs are the conversations the host's daemon remembers, for the
	// sidebar, as of its count of their changes convGen; convAsked tells
	// that they're asked for.
	convs     []proto.Conversation
	convGen   uint64
	convKnown bool
	convAsked bool
	viewSent  *string
	// busy is what the app does on the host for the user: "start",
	// "install" or "update".
	busy string
	// poked is when the app last asked the daemon to dial the host now.
	poked time.Time
}

func (h *host) remote() bool { return h.name != "" }

// dialer names the host's daemon for the panes' streams.
func (h *host) dialer() client.Dialer {
	if h.remote() {
		return client.Host(h.name)
	}
	return client.Local{NoStart: true}
}

// addr is the host's address, as tuios hosts add was given it.
func (h *host) addr() string { return h.rep.Addr }

// label names the host for the user: its name, "This Computer" for this
// one.
func (h *host) label() string {
	if !h.remote() {
		return locales.HostThisComputer()
	}
	return h.name
}

// home is the host's home directory, as far as the app knows it.
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

// stale reports whether this machine's daemon runs another build than the
// app's tuios: never for a remote host, whose version shows on its row.
func (h *host) stale() bool {
	if h.remote() || h.conn != connReady || h.daemonApp == "" {
		return false
	}
	return kerodStale(h.daemonApp, h.daemonFP)
}

// version is the tuios version a host's daemon runs, as its link or its
// hello said.
func (h *host) version() string {
	if h.rep.DaemonVersion != "" {
		return h.rep.DaemonVersion
	}
	return h.daemonApp
}

// behind reports whether tuios on a remote host is of another version than
// this machine's daemon: tuios hosts sync installs this one's there.
func (h *host) behind() bool { return h.remote() && h.rep.Status == client.HostIncompatible }

// relaunchForKerod is kero's relaunch into a newer app after its daemon
// updated it: tuios updates itself otherwise, so nothing happens.
func relaunchForKerod(string) {}

// released tells a version of tuios that has a release to install: not a
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

// syncHosts follows the app's settings for the hosts it shows: their
// colors. Which hosts there are is the daemon's (applyHosts).
func (a *App) syncHosts() {
	for _, h := range a.hosts[1:] {
		h.cfg = a.hostPrefs(h.name)
	}
}

// hostPrefs are the app's own choices for a host.
func (a *App) hostPrefs(name string) settings.Host {
	if cfg := a.set.Host(name); cfg != nil {
		return *cfg
	}
	return settings.Host{Name: name}
}

// hostsEvery is how often the app asks this machine's daemon how its links
// stand, besides the host-changed events: a far machine's load moves
// without one.
const hostsEvery = 60 * time.Second

// refreshHosts asks this machine's daemon how its links stand, off the main
// thread, and follows the answer.
func (a *App) refreshHosts() {
	cl := a.local().cl
	if cl == nil {
		return
	}
	go func() {
		reps, load, err := cl.Hosts()
		if err != nil {
			return
		}
		a.hostsMu.Lock()
		// A link that failed and dials again says connecting for the
		// moment of the dial, about once a minute: the app keeps showing
		// why it failed until the dial ends.
		for i, r := range reps {
			if last, ok := a.hostsLast[r.Host]; ok && r.Status == client.HostConnecting && failedLink(last.Status) {
				reps[i].Status, reps[i].Reason, reps[i].Detail = last.Status, last.Reason, last.Detail
			}
		}
		a.hostsLast = map[string]client.HostReport{}
		for _, r := range reps {
			a.hostsLast[r.Host] = r
		}
		// Nothing the app shows changed: no frame, no work.
		key := hostsShown(reps)
		same := key == a.hostsKey
		a.hostsKey = key
		a.hostsMu.Unlock()
		if same {
			return
		}
		a.update(func() { a.applyHosts(reps, load) })
	}()
}

// failedLink tells a link status that keeps a host's sessions from
// showing until something changes.
func failedLink(status string) bool {
	switch status {
	case client.HostUnreachable, client.HostNoDaemon, client.HostNoTuios, client.HostIncompatible, client.HostSignIn:
		return true
	}
	return false
}

// hostsShown is what the app shows of the daemon's links, as text: two
// answers that differ only in what it does not show draw nothing anew.
func hostsShown(reps []client.HostReport) string {
	var b strings.Builder
	for _, r := range reps {
		fmt.Fprintf(&b, "%s|%s|%s|%s|%s|%s|%s|%d|%.1f|%d|%d|%d|%s\n", r.Host, r.Addr, r.Status, r.Reason, r.Detail,
			r.ApprovalURL, r.DaemonVersion, r.Cores, r.Load1, r.RTTMillis, r.Drops, r.Queued, r.Command)
	}
	return b.String()
}

// watchHosts follows the links of this machine's daemon: on its events,
// and every hostsEvery while it links any machine.
func (a *App) watchHosts(cl *client.Client) {
	cl.WatchHosts(func() { a.update(a.refreshHosts) })
	a.refreshHosts()
	a.hostsGen++
	gen := a.hostsGen
	var tick func()
	tick = func() {
		a.update(func() {
			if gen != a.hostsGen || a.local().cl != cl {
				return
			}
			if len(a.hosts) > 1 || a.ss.page == "hosts" {
				a.refreshHosts()
			}
			time.AfterFunc(hostsEvery, tick)
		})
	}
	time.AfterFunc(hostsEvery, tick)
}

// applyHosts takes the daemon's word on its links: hosts added, removed,
// and how each stands, and connects to those whose link is up.
func (a *App) applyHosts(reps []client.HostReport, load client.HostReport) {
	a.localLoad = load
	keep := []*host{a.local()}
	for _, r := range reps {
		h := a.hostNamed(r.Host)
		if h == nil {
			h = &host{name: r.Host, conn: connConnecting}
		}
		h.rep = r
		h.cfg = a.hostPrefs(r.Host)
		keep = append(keep, h)
	}
	for _, h := range a.hosts[1:] {
		if !slices.Contains(keep, h) {
			a.closeHost(h)
		}
	}
	a.hosts = keep
	a.hostsKnown = true
	// The sessions shown follow the hosts' order.
	a.mergeState(a.local(), proto.State{Projects: a.projectsOf(a.local()), Sessions: a.sessionsOf(a.local())})
	for _, h := range keep[1:] {
		a.followLink(h)
	}
	// The session shown may have gone with its host.
	a.reselect()
	a.updateTitle()
	// Settings › Hosts shows the links too, in a window of its own.
	if a.setWin != nil {
		a.setWin.Invalidate()
	}
}

// followLink sets how the app stands with a remote host from its link, and
// connects to its daemon once the link is up.
func (a *App) followLink(h *host) {
	switch h.rep.Status {
	case client.HostUp:
		client.LinkBack(h.name)
		if h.cl != nil {
			h.conn, h.err = connReady, ""
		} else if !h.dialing && h.busy == "" {
			a.connectHost(h)
		}
	case client.HostIncompatible:
		h.conn, h.err = connVersion, h.rep.Reason
	case client.HostConnecting, client.HostReconnecting, "":
		h.conn = connConnecting
	default:
		h.conn, h.err = connFailed, h.rep.Reason
	}
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
	a.mergeState(h, proto.State{})
	h.loaded = false
	// Its browser panes' pages, which this machine shows, go with it.
	for id := range a.webs {
		if a.sessionOfPane(id) == nil {
			a.dropWeb(id)
		}
	}
}

// connect connects to this machine's daemon, starting it.
func (a *App) connect() { a.connectHost(a.local()) }

// connectHost connects to a host's daemon: this machine's, or a linked
// machine's through this machine's daemon.
func (a *App) connectHost(h *host) {
	h.gen++
	gen := h.gen
	if h.conn != connReady {
		h.conn = connConnecting
	}
	h.dialing = true
	go func() {
		var cl *client.Client
		var err error
		if !h.remote() {
			cl, err = client.Connect(false)
		} else {
			cl, err = client.ConnectHost(h.name)
		}
		a.update(func() { a.connected(h, gen, cl, err) })
	}()
}

// connected takes the result of connectHost.
func (a *App) connected(h *host, gen int, cl *client.Client, err error) {
	if h.gen != gen || !slices.Contains(a.hosts, h) {
		if cl != nil {
			go cl.Close()
		}
		return
	}
	h.dialing = false
	if err != nil {
		h.cl = nil
		if errors.Is(err, client.ErrVersion) {
			h.conn, h.err = connVersion, ""
			return
		}
		h.retries++
		if h.remote() && h.rep.Status == client.HostUp {
			// The link is up and its daemon did not take the app: say
			// why, and try again a little later.
			h.conn, h.err = connFailed, err.Error()
		} else if h.remote() {
			a.followLink(h)
		} else {
			h.conn, h.err = connFailed, err.Error()
		}
		a.retryLater(h)
		// The session shown last may be of this host, which failed.
		if !h.loaded {
			a.reselect()
		}
		return
	}
	h.cl, h.conn, h.err, h.retries = cl, connReady, "", 0
	h.daemonApp, h.daemonFP = cl.Welcome.App, cl.Welcome.Build
	cl.OnEvent(func(m proto.Message) { a.update(func() { a.event(h, cl, m) }) })
	cl.Go("subscribe", nil, nil)
	h.viewSent = nil
	a.reportView()
	if !h.remote() {
		a.watchHosts(cl)
		a.watchTransfers(cl)
	} else {
		// Its OS and home, for its paths: ~ for its home in the sidebar.
		go func() {
			goos, home := cl.HostInfo()
			a.update(func() {
				if h.cl == cl {
					h.info.OS, h.info.Home = goos, home
				}
			})
		}()
	}
	go func() {
		<-cl.Done()
		a.update(func() { a.disconnected(h, cl) })
	}()
}

// retryLater connects to a host again after a failure: this machine's a
// few times, as its daemon may be starting; another's while its link is
// up, waiting longer each time, up to half a minute. A link that is down
// comes back by the daemon's own retries, which applyHosts follows.
func (a *App) retryLater(h *host) {
	gen := h.gen
	var wait time.Duration
	switch {
	case !h.remote():
		if h.retries >= 4 {
			return
		}
		wait = time.Duration(h.retries) * 400 * time.Millisecond
	default:
		wait = min(time.Second<<min(h.retries-1, 5), 30*time.Second)
	}
	time.AfterFunc(wait, func() {
		a.update(func() {
			if h.gen == gen && h.cl == nil && !h.dialing && slices.Contains(a.hosts, h) &&
				(!h.remote() || h.rep.Status == client.HostUp) {
				a.connectHost(h)
			}
		})
	})
}

// reconnect connects to a host again, unless it is.
func (a *App) reconnect(h *host) {
	if slices.Contains(a.hosts, h) && h.cl == nil && h.busy == "" && !h.dialing {
		a.connectHost(h)
	}
}

// disconnected follows a host's daemon going away, as when it was
// restarted, or its link dropped. This machine's terminals are done, and
// attach again. A remote host's keep their last frame and open their
// streams again on their own (client.Stream), so a link that drops and
// comes back never blanks a pane; its sessions stay listed, faded, while
// the app reconnects.
func (a *App) disconnected(h *host, cl *client.Client) {
	if h.cl != cl {
		return
	}
	h.cl = nil
	h.conn = connConnecting
	if !h.remote() {
		a.dropTerms(h)
		// What changed while the app was away tells nothing new.
		for _, s := range a.sessionsOf(h) {
			delete(a.statuses, s.ID)
		}
		time.AfterFunc(300*time.Millisecond, func() { a.update(func() { a.reconnect(h) }) })
		return
	}
	// The link tells when the host is back.
	a.refreshHosts()
	time.AfterFunc(time.Second, func() {
		a.update(func() {
			if h.rep.Status == client.HostUp {
				a.reconnect(h)
			}
		})
	})
}

// newPaneOn opens a pane in a session of this machine whose program runs on
// a linked machine, beside the session's focused pane, as tuios new-window
// --host does. It stays in the session, titled HOST:NAME.
func (a *App) newPaneOn(s *proto.Session, host string) {
	if s == nil {
		return
	}
	at := a.focusOf(s)
	if at == "" {
		at = s.ID
	}
	a.split(s, proto.CreateParams{Split: at, Direction: "right", Host: host})
}

// pokeHost has this machine's daemon dial a machine's link now, at most
// once every pokeEvery: the person looks at its sessions.
func (a *App) pokeHost(h *host) {
	local := a.local().cl
	if local == nil || time.Since(h.poked) < pokeEvery {
		return
	}
	h.poked = time.Now()
	go local.RetryHost(h.name)
}

// pokeWhileLost pokes the machine of a pane whose stream lost its link,
// every pokeEvery while the pane shows and the stream waits. The daemon's own
// redials wait longer each time, up to a minute, and a person who looks at
// the pane wants it back as soon as the machine answers.
func (a *App) pokeWhileLost(id string, st *client.Stream) {
	t := time.NewTicker(pokeEvery)
	defer t.Stop()
	for range t.C {
		if !st.Lost() {
			return
		}
		select {
		case <-st.Gone:
			return
		default:
		}
		a.update(func() {
			if s := a.session(a.selected); s != nil && slices.Contains(shownPanes(s), id) {
				a.pokeHost(a.hostOf(id))
			}
		})
	}
}

// pokeEvery is the least time between two pokes of one machine.
const pokeEvery = 5 * time.Second

// pokeHosts pokes every machine whose link is down, as when the window
// comes back to the front after the computer slept.
func (a *App) pokeHosts() {
	for _, h := range a.hosts[1:] {
		if h.conn != connReady && h.busy == "" && h.rep.Status != client.HostConnecting {
			a.pokeHost(h)
		}
	}
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
	// A program on another machine never drives this app.
	return false
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
