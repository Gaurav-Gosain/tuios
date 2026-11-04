package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The app reads the daemon as kero's proto.State: projects, sessions and
// their panes, sent whole on every change. tuios has no projects: a session's
// space (set-session-space) is its project, and sessions with none share one
// project, defaultProject. The split tree of each session's panes is the
// app's own (tuios keeps its layouts in its terminal clients), kept here and
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
}

type windowRow struct {
	ID          string           `json:"window_id"`
	PTY         string           `json:"pty_id"`
	Title       string           `json:"title"`
	DisplayName string           `json:"display_name"`
	Name        string           `json:"name"`
	Cwd         string           `json:"cwd"`
	Width       int              `json:"width"`
	Height      int              `json:"height"`
	PID         int              `json:"pid"`
	Focused     bool             `json:"focused"`
	Minimized   bool             `json:"minimized"`
	Workspace   int              `json:"workspace"`
	Agent       string           `json:"agent_state"`
	AgentMsg    string           `json:"agent_message"`
	AgentKind   string           `json:"agent_kind"`
	AgentAt     int64            `json:"agent_state_at"`
	Harness     string           `json:"agent_harness"`
	Foreground  string           `json:"foreground"`
	Program     []map[string]any `json:"program_status"`
}

// stateKeeper builds the state and keeps it current from the daemon's events.
type stateKeeper struct {
	c *Client

	mu      sync.Mutex
	state   *proto.State
	layouts map[string]*proto.Layout // by session id
	unread  map[string]bool          // pane ids with a finished turn not seen
	viewed  string
	started int64
	// paneSession finds a pane's session, for streams and pane methods.
	paneSession map[string]string
	sessionName map[string]string // session id to name

	kick chan struct{}
}

func newStateKeeper(c *Client) *stateKeeper {
	k := &stateKeeper{c: c, layouts: map[string]*proto.Layout{}, unread: map[string]bool{},
		paneSession: map[string]string{}, sessionName: map[string]string{}, kick: make(chan struct{}, 1),
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
	raw, err := k.c.verb("list-sessions", nil)
	if err != nil {
		return nil, err
	}
	var sl struct {
		Sessions []sessionRow `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &sl); err != nil {
		return nil, err
	}
	st := &proto.State{Started: k.started}
	projects := map[string]bool{}
	paneSession := map[string]string{}
	names := map[string]string{}
	home, _ := os.UserHomeDir()
	for _, s := range sl.Sessions {
		names[s.ID] = s.Name
		project := s.Space
		if project == "" {
			project = defaultProject
		}
		if !projects[project] {
			projects[project] = true
			name := project
			if project == defaultProject {
				name = "Sessions"
			}
			st.Projects = append(st.Projects, proto.Project{ID: project, Name: name, Dir: home})
		}
		wraw, err := k.c.verb("list-windows", map[string]any{"session": s.Name})
		if err != nil {
			continue
		}
		var wl struct {
			Windows []windowRow `json:"windows"`
			Focus   string      `json:"focused_window_id"`
		}
		_ = json.Unmarshal(wraw, &wl)
		ks := proto.Session{ID: s.ID, Project: project, Name: s.Name, Focus: wl.Focus}
		for _, w := range wl.Windows {
			paneSession[w.ID] = s.ID
			ks.Panes = append(ks.Panes, k.pane(w))
		}
		ks.Layout = k.layoutFor(s.ID, ks.Panes, wl.Focus)
		if i := proto.Lead(ks.Panes, ks.Focus); i >= 0 && i < len(ks.Panes) {
			ks.Pane = ks.Panes[i]
			ks.Lead = ks.Panes[i].ID
		}
		if s.Dir != "" && ks.Pane.Dir == "" {
			ks.Pane.Dir = expandHome(s.Dir, home)
		}
		st.Sessions = append(st.Sessions, ks)
	}
	sort.SliceStable(st.Projects, func(i, j int) bool {
		// The shared project first, then spaces by name.
		a, b := st.Projects[i].ID, st.Projects[j].ID
		if (a == defaultProject) != (b == defaultProject) {
			return a == defaultProject
		}
		return a < b
	})
	k.mu.Lock()
	k.paneSession, k.sessionName = paneSession, names
	st.Viewed = k.viewed
	k.state = st
	k.mu.Unlock()
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
		Status: proto.Idle, Since: w.AgentAt / int64(time.Millisecond),
	}
	switch w.Agent {
	case "working":
		p.Status = proto.Working
	case "needs_input":
		p.Status = proto.Waiting
		p.Unread = true
	case "errored":
		p.Status = proto.Failed
		p.Unread = true
	case "done":
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
	for _, r := range w.Program {
		rec := proto.Record{Progress: -1}
		b, _ := json.Marshal(r)
		_ = json.Unmarshal(b, &rec)
		p.Records = append(p.Records, rec)
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

// layoutFor keeps the session's split tree in step with its panes: a new
// pane goes to the right of the focused one, a gone one leaves the tree.
func (k *stateKeeper) layoutFor(sid string, panes []proto.Pane, focus string) *proto.Layout {
	k.mu.Lock()
	defer k.mu.Unlock()
	l := k.layouts[sid]
	have := map[string]bool{}
	for _, p := range panes {
		have[p.ID] = true
	}
	if l != nil {
		for _, id := range l.Panes() {
			if !have[id] {
				l = l.Remove(id)
			}
		}
	}
	for _, p := range panes {
		if l == nil {
			l = &proto.Layout{Pane: p.ID}
			continue
		}
		if !contains(l.Panes(), p.ID) {
			at := focus
			if at == "" || !contains(l.Panes(), at) {
				ids := l.Panes()
				at = ids[len(ids)-1]
			}
			l = l.Split(at, p.ID, "right", 0.5)
		}
	}
	k.layouts[sid] = l
	if l == nil || l.Leaf() {
		return nil
	}
	return l.Clone()
}

func (k *stateKeeper) setLayout(sid string, l *proto.Layout) {
	k.mu.Lock()
	k.layouts[sid] = l
	k.mu.Unlock()
	k.saveLayouts()
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
		_ = json.Unmarshal(b, &k.layouts)
	}
	if k.layouts == nil {
		k.layouts = map[string]*proto.Layout{}
	}
}

func (k *stateKeeper) saveLayouts() {
	p := layoutsPath()
	if p == "" {
		return
	}
	k.mu.Lock()
	b, err := json.Marshal(k.layouts)
	k.mu.Unlock()
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, b, 0o600)
}

// watch subscribes to the daemon's events on a connection of its own and
// sends the state anew after each burst of them. Nothing polls: with no
// event, nothing runs.
func (k *stateKeeper) watch() {
	vc, err := session.DialVerbClientAs(Version)
	if err != nil {
		k.c.end()
		return
	}
	defer vc.Close()
	if _, err := vc.Call("subscribe", map[string]any{"types": []string{
		"session-created", "session-closed", "client-session-changed", "window-created", "window-closed",
		"window-retitled", "window-exit", "window-moved", "window-focused", "window-minimized", "window-restored",
		"agent-state", "agent-seen", "notification", "bell", "attention", "prompt", "command-finished",
		"workspace-switched", "layout-changed",
	}}); err != nil {
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
		}
		_ = json.Unmarshal(line, &ev)
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

func firstOf(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}
