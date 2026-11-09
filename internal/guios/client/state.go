package client

import (
	"cmp"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The app reads the daemon as kero's proto.State: projects, sessions and
// their panes, sent whole on every change. tuios has no projects: a session's
// space (set-session-space) is its project, and sessions with none share one
// project, defaultProject. A session's panes are those of the workspace it
// shows, laid out by that workspace's tree (layouttree.go). Where the
// workspace keeps no tree, the split tree is the app's own, kept here and
// saved under the app's state folder.

const defaultProject = "sessions"

// sessionRow and windowRow are what list-sessions and list-windows say.
type sessionRow struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Dir         string `json:"dir"`
	Space       string `json:"space"`
	DisplayName string `json:"display_name"`
	Created     int64  `json:"created"`
	LastActive  int64  `json:"last_active"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Worktree    *struct {
		Repo     string `json:"repo"`
		RepoRoot string `json:"repo_root"`
		Branch   string `json:"branch"`
		Path     string `json:"path"`
		Main     bool   `json:"main"`
	} `json:"worktree"`
}

// windowRow is a window as list-windows gives it.
type windowRow = session.WindowRow

// stateKeeper builds the state and keeps it current from the daemon's events.
type stateKeeper struct {
	c *Client

	// convGen counts the events after which the daemon may have recorded
	// an ended conversation (State.Conversations).
	convGen atomic.Uint64

	buildMu sync.Mutex
	mu      sync.Mutex
	state   *proto.State
	layouts map[string]*proto.Layout // by session id
	trees   map[string]treeInfo      // the shown workspace's tree, by session id
	shown   map[string][]windowRow   // the shown workspace's panes, by session id
	// prevFocus is the focus each session had at the last build, which a
	// new pane splits; pending the split guios asked for, by session id.
	prevFocus map[string]string
	pending   map[string]pendingSplit
	// floated is where each pane floated out of the split tree tiled, by
	// pane id, so that tiled again it goes back there (session.FloatSlot).
	floated map[string]session.FloatSlot
	zoom    map[string]string // the pane guios zoomed, by session id, with no TUI to zoom it
	unread  map[string]bool   // pane ids with a finished turn not seen
	viewed  string
	started int64
	// paneSession finds a pane's session, for streams and pane methods.
	paneSession map[string]string
	sessionName map[string]string // session id to name
	// closing are the windows being closed because their program exited
	// (exits.go).
	closing map[string]bool

	kick chan struct{}
}

func newStateKeeper(c *Client) *stateKeeper {
	k := &stateKeeper{c: c, layouts: map[string]*proto.Layout{}, trees: map[string]treeInfo{}, zoom: map[string]string{}, unread: map[string]bool{},
		shown: map[string][]windowRow{}, prevFocus: map[string]string{}, pending: map[string]pendingSplit{},
		floated: map[string]session.FloatSlot{}, paneSession: map[string]string{}, sessionName: map[string]string{}, kick: make(chan struct{}, 1),
		closing: map[string]bool{},
		started: time.Now().UnixMilli()}
	k.loadLayouts()
	return k
}

// lookup returns a pane's session id and name.
func (k *stateKeeper) lookup(pane string) (sid, name string, ok bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	sid, ok = k.paneSession[pane]
	if !ok {
		if _, isSession := k.sessionName[pane]; isSession {
			return pane, k.sessionName[pane], true
		}
		return "", "", false
	}
	return sid, k.sessionName[sid], true
}

// sessionByID returns a session's name.
func (k *stateKeeper) nameOf(sid string) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	if n, ok := k.sessionName[sid]; ok {
		return n
	}
	return sid
}

// build reads the daemon and makes the state anew.
func (k *stateKeeper) build() (*proto.State, error) {
	// One build at a time: a build places a pane the tree lacks and writes
	// the tree before the next build reads it, so two builds never place
	// one pane two ways.
	k.buildMu.Lock()
	defer k.buildMu.Unlock()
	raw, err := k.c.verb("list-sessions", nil)
	if err != nil {
		return nil, err
	}
	var sl struct {
		Sessions []sessionRow `json:"sessions"`
	}
	if err := jsonx.Unmarshal(raw, &sl); err != nil {
		return nil, err
	}
	st := &proto.State{Started: k.started, Conversations: k.convGen.Load() + 1}
	var reaps []reap
	projects := map[string]bool{}
	paneSession := map[string]string{}
	names := map[string]string{}
	home := ""
	if k.c.host == "" {
		// Another machine's home is not this one's.
		home, _ = os.UserHomeDir()
	}
	for _, s := range sl.Sessions {
		names[s.ID] = s.Name
		space := s.Space
		if space == "" {
			space = defaultProject
		}
		project := k.c.projectID(space)
		if !projects[project] {
			projects[project] = true
			name := space
			if space == defaultProject {
				name = "Sessions"
			}
			st.Projects = append(st.Projects, proto.Project{ID: project, Name: name, Dir: home})
		}
		wraw, err := k.c.verb("list-windows", map[string]any{"session": s.Name})
		if err != nil {
			continue
		}
		var wl struct {
			Windows   []windowRow `json:"windows"`
			Focus     string      `json:"focused_window_id"`
			Workspace int         `json:"current_workspace"`
			Shell     string      `json:"shell"`
		}
		_ = jsonx.Unmarshal(wraw, &wl)
		ws := wl.Workspace
		if ws <= 0 {
			ws = 1
		}
		ks := proto.Session{ID: s.ID, Project: project, Name: userName(s.Name), Focus: wl.Focus, Workspace: ws}
		if w := s.Worktree; w != nil && w.Repo != "" {
			ks.Checkout = &proto.Checkout{Repo: w.Repo, RepoRoot: w.RepoRoot, Branch: w.Branch, Path: w.Path, Linked: !w.Main}
		}
		ti := k.readTree(s.Name, ws)
		// A pane whose program exited goes, unless it stays (keep_open):
		// guios closes it, as a TUI client does (exits.go).
		gone, hide, end := exitedIn(wl.Windows, ws)
		goneID := goneIDs(gone)
		if len(gone) > 0 {
			reaps = append(reaps, reap{session: s.Name, windows: gone, end: end && !ti.TUI})
		}
		// The panes of the workspace the session shows; the others are
		// counted on their workspaces.
		var shown []windowRow
		var minimized []minimizedPane
		counts := map[int]*proto.Workspace{}
		for _, w := range wl.Windows {
			paneSession[w.ID] = s.ID
			if w.Scratch && w.ScratchName == config.DefaultScratchName && session.IsScratchWorkspace(w.Workspace) && !(hide && slices.Contains(goneID, w.ID)) {
				// The built-in scratch terminal's group, as the TUI's
				// toggle_scratch shows it: a pane of no workspace's.
				p := k.pane(w)
				p.Workspace = w.Workspace
				ks.Scratch = append(ks.Scratch, p)
			}
			if w.Scratch || w.Rail != "" || hide && slices.Contains(goneID, w.ID) {
				continue
			}
			p := k.pane(w)
			p.Shell = wl.Shell
			cw := counts[w.Workspace]
			if cw == nil {
				cw = &proto.Workspace{N: w.Workspace, Status: proto.Idle}
				counts[w.Workspace] = cw
			}
			cw.Panes++
			if w.Minimized {
				cw.Minimized++
			}
			cw.Unread = cw.Unread || p.Unread
			if statusRank(p.Status) > statusRank(cw.Status) {
				cw.Status = p.Status
			}
			if w.Minimized || w.Workspace != ws {
				// A minimized pane stays out of the panes until it is
				// restored. On the shown workspace it is listed, for the
				// rail's minimized row.
				if w.Minimized && w.Workspace == ws {
					minimized = append(minimized, minimizedPane{p: p, at: w.MinimizedAt})
				}
				if !w.Minimized {
					p.Workspace = w.Workspace
					ks.Others = append(ks.Others, p)
				}
				continue
			}
			if w.Zoomed {
				ti.ZoomByTUI = w.ID
			}
			shown = append(shown, w)
			ks.Panes = append(ks.Panes, p)
		}
		ks.Workspaces = workspaceList(counts, ws, ti)
		ks.Minimized = minimizedOrder(minimized)
		ks.TUI = ti.TUI
		ks.Layout = k.layoutOf(s.ID, s.Name, shown, wl.Focus, &ti)
		ks.LayoutMode = ti.Mode()
		if a := ti.Arrange(shown, wl.Focus); ks.LayoutMode != modeBSP || len(a.Float) > 0 {
			ks.Arrange = a
			k.settle(s.Name, &ti, shown, wl.Focus, a)
		}
		ks.Zoomed = ti.ZoomByTUI
		if ks.Zoomed == "" {
			k.mu.Lock()
			if z := k.zoom[s.ID]; z != "" && contains(ids(shown), z) {
				ks.Zoomed = z
			}
			k.mu.Unlock()
		}
		k.mu.Lock()
		k.trees[s.ID] = ti
		k.shown[s.ID] = shown
		k.mu.Unlock()
		if i := proto.Lead(ks.Panes, ks.Focus); i >= 0 && i < len(ks.Panes) {
			ks.Pane = ks.Panes[i]
			ks.Lead = ks.Panes[i].ID
		}
		if ks.Pane.Dir == "" {
			// A workspace with no pane: where the session's other panes
			// work, else its own folder. list-sessions may give that
			// folder by its last name alone, which is no path to start in.
			for _, w := range wl.Windows {
				if filepath.IsAbs(w.Cwd) {
					ks.Pane.Dir = w.Cwd
					break
				}
			}
		}
		if d := expandHome(s.Dir, home); ks.Pane.Dir == "" && home != "" && filepath.IsAbs(d) {
			ks.Pane.Dir = d
		}
		st.Sessions = append(st.Sessions, ks)
	}
	sort.SliceStable(st.Projects, func(i, j int) bool {
		// The shared project first, then spaces by name.
		a, b := k.c.spaceOf(st.Projects[i].ID), k.c.spaceOf(st.Projects[j].ID)
		if (a == defaultProject) != (b == defaultProject) {
			return a == defaultProject
		}
		return a < b
	})
	k.mu.Lock()
	k.paneSession, k.sessionName = paneSession, names
	st.Viewed = k.viewed
	k.state = st
	k.forgetExits(paneSession)
	k.mu.Unlock()
	k.reap(reaps)
	return st, nil
}

// pane is a tuios window as a kero pane. tuios's agent states map onto
// kero's statuses: needs_input is waiting, errored is error, done is idle
// with the turn unread until the app views the pane.
func (k *stateKeeper) pane(w windowRow) proto.Pane {
	title := w.DisplayName
	if title == "" {
		title = w.Title
	}
	p := proto.Pane{
		ID: w.ID, Title: title, Dir: w.Cwd, Cols: w.Width, Rows: w.Height, PID: w.PID,
		Process: w.Foreground, Agent: w.Harness, Message: w.AgentMsg, Kind: w.AgentKind, Progress: -1,
		Status: proto.Idle, Since: w.AgentAt / int64(time.Millisecond), Host: w.Host,
		Named: w.CustomName != "", Running: w.Foreground != "",
		AgentState: w.Agent, AgentReason: w.AgentReason, AgentAt: w.AgentAt,
	}
	if w.Agent == "working" && w.AgentReason == "silent" {
		p.AgentState = "unknown"
	}
	if w.Kind == "tml" {
		// A TML surface, drawn by the app (cmd/guios/tml_view.go). Its
		// program is the daemon's placeholder, as a web window's is.
		p.Surface = w.Surface
		p.Process, p.Running = "", false
	}
	if w.Kind == "web" {
		// A browser pane, drawn by the app's web view (proto.Pane.Browser).
		// Its program is the daemon's placeholder: its title ("Terminal
		// …") and its process (sleep) say nothing of the page, so the pane
		// is named by its page, unless the person named it.
		p.URL = w.URI
		p.Process, p.Running = "", false
		if !p.Named {
			p.Title = ""
		}
	}
	// A pane named by the person or its program shows that name: kero
	// shows a pane's title while a program runs in it.
	if p.Process == "" && title != "" && !strings.HasPrefix(title, "Terminal ") && !looksLikePrompt(title) {
		p.Process = title
	}
	switch {
	case w.Agent == "working" && w.AgentReason == "silent":
		// It reported working and then went quiet: the daemon shows it
		// as unknown (list-agents), so it spins no more. Not unread: a
		// pane gone quiet has not finished.
	case w.Agent == "working":
		p.Status = proto.Working
	case w.Agent == "needs_input":
		p.Status = proto.Waiting
		p.Unread = true
	case w.Agent == "errored":
		p.Status = proto.Failed
		p.Unread = true
	case w.Agent == "done":
		k.mu.Lock()
		if _, seen := k.unread[w.ID]; !seen {
			k.unread[w.ID] = true
		}
		p.Unread = k.unread[w.ID]
		k.mu.Unlock()
	default:
		k.mu.Lock()
		delete(k.unread, w.ID)
		k.mu.Unlock()
	}
	// The worktree the pane's folder is in, read from its files (no git
	// process but for the upstream counts, which gitstate caches): the
	// Changes and Files tabs show only a pane with one.
	if g, ok := gitstate.Read(p.WorkDir()); ok && p.WorkDir() != "" {
		gi := &proto.GitInfo{Root: g.Root, Upstream: g.Upstream, Ahead: g.Ahead, Behind: g.Behind}
		if !g.Detached {
			gi.Branch = g.Branch
		} else {
			gi.Head = g.Branch
		}
		p.Git = gi
	}
	for _, r := range w.Program {
		rec := proto.Record{Progress: -1}
		b, _ := jsonx.Marshal(r)
		_ = jsonx.Unmarshal(b, &rec)
		p.Records = append(p.Records, rec)
	}
	if w.Exited && w.KeepOpen {
		// A pane that stays after its program exited (keep_open), with the
		// line the daemon wrote under the output: its status, as the
		// terminal plugin counts one a signal ended.
		p.Status, p.ExitCode = proto.Exited, w.ExitCode
		if w.Signal > 0 {
			p.ExitCode = 128 + w.Signal
		}
	}
	return p
}

// seen marks a session's finished turns as read, as the app shows it.
func (k *stateKeeper) seen(sid string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.viewed = sid
	for pane, s := range k.paneSession {
		if s == sid && k.unread[pane] {
			k.unread[pane] = false
		}
	}
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

func layoutsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "guios", "layouts.json")
}

func (k *stateKeeper) loadLayouts() {
	b, err := os.ReadFile(layoutsPath())
	if err == nil {
		_ = jsonx.Unmarshal(b, &k.layouts)
	}
	if k.layouts == nil {
		k.layouts = map[string]*proto.Layout{}
	}
}

// watch subscribes to the daemon's events on a connection of its own and
// sends the state anew after each burst of them. Nothing polls: with no
// event, nothing runs.
func (k *stateKeeper) watch() {
	vc, err := k.c.dial()
	if err != nil {
		k.c.end()
		return
	}
	defer vc.Close()
	// The theme a session was told with set-option before guios came:
	// option-set says the ones after.
	for _, key := range ThemeOptions {
		if v, ok := sessionOption(vc, key); ok {
			k.c.emit(proto.Message{Event: "daemon", Daemon: &proto.DaemonEvent{Type: "option-set", Key: key, Value: v}})
		}
	}
	if err := subscribeKnown(vc, []string{
		"session-created", "session-closed", "client-session-changed", "window-created", "window-closed",
		"window-retitled", "window-exit", "window-moved", "window-focused", "window-minimized", "window-restored",
		"agent-state", "agent-seen", "notification", "bell", "attention", "prompt", "command-finished",
		"workspace-switched", "layout-changed",
		// A command starting or ending in a pane: its name in the panes'
		// headers and the sidebar.
		"window-foreground",
		// A shell that moved to another folder: the folder in the panes'
		// headers and the sidebar. A cd changes no title in a shell that
		// sets none, so nothing else would say so.
		"window-cwd",
		// For the agent cockpit only: they change no pane.
		"transcript", "agent-message", "task-claimed", "task-finished", "task-changed", "agent-activity",
		// A judge's verdict, which the fan comparison reads again on.
		"fan-verdict",
		// tuios set-config on a theme, which guios follows.
		"option-set",
		// A TML surface opened, changed or closed: the app draws it.
		"tml-surface",
	}); err != nil {
		k.c.end()
		return
	}
	go k.emitter()
	for {
		line, err := vc.ReadEventLine(0)
		if err != nil {
			k.c.end()
			return
		}
		var ev struct {
			Type    string `json:"type"`
			Session string `json:"session"`
			Window  string `json:"window"`
			Title   string `json:"title"`
			Body    string `json:"body"`
			Message string `json:"message"`
			Key     string `json:"key"`
			Value   string `json:"value"`
			// TML is a tml-surface event's change.
			TML *proto.TMLChange `json:"tml_surface"`
		}
		_ = jsonx.Unmarshal(line, &ev)
		if ev.Type == "tml-surface" {
			// No pane changed: the window's own events say when one opens
			// or closes. The app keeps the surface's tree and state.
			if ev.TML != nil {
				k.c.emit(proto.Message{Event: "daemon", Daemon: &proto.DaemonEvent{Type: ev.Type, Session: ev.Session, Window: ev.Window, TML: ev.TML}})
			}
			continue
		}
		if ev.Type == "option-set" {
			// No pane changed. Only the theme options reach the app.
			if slices.Contains(ThemeOptions, ev.Key) {
				k.c.emit(proto.Message{Event: "daemon", Daemon: &proto.DaemonEvent{Type: ev.Type, Session: ev.Session, Key: ev.Key, Value: ev.Value}})
			}
			continue
		}
		debugf("event %s: %s", k.c.host, ev.Type)
		if ev.Type == "window-closed" || ev.Type == "agent-state" {
			// The daemon records an ended conversation on either.
			k.convGen.Add(1)
		}
		// A window-exit event only asks for a build, which reads from
		// list-windows whether the window's own PTY exited (exits.go).
		switch ev.Type {
		case "agent-state", "agent-seen", "attention", "window-closed", "window-created", "window-retitled",
			"transcript", "agent-message", "task-claimed", "task-finished", "task-changed", "agent-activity", "fan-verdict":
			k.c.emit(proto.Message{Event: "daemon", Daemon: &proto.DaemonEvent{Type: ev.Type, Session: ev.Session, Window: ev.Window}})
		}
		switch ev.Type {
		case "transcript", "agent-message", "task-claimed", "task-finished", "task-changed", "agent-activity", "fan-verdict":
			// No pane changed: the state stays as it is.
			continue
		}
		if ev.Type == "notification" {
			k.c.emit(proto.Message{Event: "notice", Notice: &proto.Notice{Session: k.sessionOf(ev.Session), Pane: ev.Window, Title: ev.Title, Body: firstOf(ev.Body, ev.Message)}})
		}
		select {
		case k.kick <- struct{}{}:
		default:
		}
	}
}

// emitter builds and sends the state once per burst of events: the first
// event waits 30 ms for the rest of its burst.
func (k *stateKeeper) emitter() {
	for {
		select {
		case <-k.kick:
		case <-k.c.closed:
			return
		}
		time.Sleep(30 * time.Millisecond)
		select {
		case <-k.kick:
		default:
		}
		if st, err := k.build(); err == nil {
			k.c.emit(proto.Message{Event: "state", State: st})
		}
	}
}

func (k *stateKeeper) sessionOf(name string) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	for id, n := range k.sessionName {
		if n == name {
			return id
		}
	}
	return name
}

// projectID is the app's id of a space. A linked machine's spaces are its
// own: their ids carry the host's name, so the "sessions" of two machines
// are two projects.
func (c *Client) projectID(space string) string {
	if c.host == "" {
		return space
	}
	return c.host + "/" + space
}

// spaceOf is the space of a project id.
func (c *Client) spaceOf(id string) string {
	if c.host == "" {
		return id
	}
	return strings.TrimPrefix(id, c.host+"/")
}

func firstOf(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// looksLikePrompt reports a shell's own title, user@host:dir, which names
// no program.
func looksLikePrompt(t string) bool {
	at, colon := strings.IndexByte(t, '@'), strings.IndexByte(t, ':')
	return at > 0 && colon > at
}

// ThemeOptions are the session options that choose the tuios theme.
var ThemeOptions = []string{"appearance.theme", "appearance.theme_light"}

// SetThemeOption gives a theme option (ThemeOptions) value as tuios
// set-config does, on the session the app shows, or the daemon's default
// one. applied tells that a TUI on that session took it: the TUI then
// saved it to config.toml.
func (c *Client) SetThemeOption(key, value string) (applied bool, err error) {
	if !slices.Contains(ThemeOptions, key) {
		return false, errfmt.Errorf("%s is not a theme option", key)
	}
	c.viewMu.Lock()
	sid := c.viewing
	c.viewMu.Unlock()
	name := ""
	if sid != "" {
		name = c.st.nameOf(sid)
	}
	raw, err := c.verb("set-option", map[string]any{"session": name, "key": key, "value": value})
	if err != nil {
		return false, err
	}
	var res struct {
		Applied bool `json:"applied"`
	}
	_ = jsonx.Unmarshal(raw, &res)
	return res.Applied, nil
}

// sessionOption is the value set-option gave key on the daemon's default
// session, and false when no set-option did: a value from config.toml or
// the default is guios's to read from the file.
func sessionOption(vc *session.VerbClient, key string) (string, bool) {
	raw, err := vc.Call("get-option", map[string]any{"key": key})
	if err != nil {
		return "", false
	}
	var res struct {
		Value  string `json:"value"`
		Source string `json:"source"`
	}
	if jsonx.Unmarshal(raw, &res) != nil || res.Source != "session" {
		return "", false
	}
	return res.Value, true
}

// unknownEventType matches the daemon's refusal of an event type it does
// not know: "unknown event type NAME".
var unknownEventType = regexp.MustCompile(`unknown event type ([a-z0-9-]+)`)

// subscribeKnown subscribes to types, leaving out each one an older daemon
// does not know. A daemon refuses the whole subscribe for one unknown type,
// which left guios with no events at all: never connected.
func subscribeKnown(vc *session.VerbClient, types []string) error {
	for range len(types) + 1 {
		_, err := vc.Call("subscribe", map[string]any{"types": types})
		if err == nil {
			return nil
		}
		m := unknownEventType.FindStringSubmatch(err.Error())
		if m == nil || !slices.Contains(types, m[1]) {
			return err
		}
		types = slices.DeleteFunc(slices.Clone(types), func(t string) bool { return t == m[1] })
	}
	return errors.New("subscribe: no event type the daemon knows")
}

// userName is a session's name as the app shows it: "" for a name tuios
// makes up for a session nobody named ("session-" and eight hex digits,
// or a number on older daemons), so the app names it after what runs in
// it, as for an unnamed one.
func userName(name string) string {
	rest, ok := strings.CutPrefix(name, "session-")
	if !ok || rest == "" {
		return name
	}
	digits, hex := true, len(rest) == 8
	for _, r := range rest {
		digits = digits && r >= '0' && r <= '9'
		hex = hex && (r >= '0' && r <= '9' || r >= 'a' && r <= 'f')
	}
	if digits || hex {
		return ""
	}
	return name
}

// Refresh builds the state anew: for what changes with no event, as a
// shell's folder after cd, when the person opens a view that shows it.
func (c *Client) Refresh() {
	select {
	case c.st.kick <- struct{}{}:
	default:
	}
}

// minimizedPane is a minimized pane with when it was minimized, for the
// session's Minimized list.
type minimizedPane struct {
	p  proto.Pane
	at int64
}

// minimizedOrder is the panes in the order they were minimized, oldest first,
// then window order, as the TUI's dock lists them.
func minimizedOrder(in []minimizedPane) []proto.Pane {
	slices.SortStableFunc(in, func(a, b minimizedPane) int { return cmp.Compare(a.at, b.at) })
	out := make([]proto.Pane, len(in))
	for i, m := range in {
		out[i] = m.p
	}
	return out
}
