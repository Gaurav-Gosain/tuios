package session

import (
	"cmp"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"time"
)

// herdr's event stream, from tuios's.
//
// events.subscribe keeps the connection open. The server answers once with
// {"id","result":{"type":"subscription_started"}} and then writes one event a
// line: {"event":"<kind>","data":{...}}. A global event's kind is snake_case
// (pane_created) and its data carries the same kind as type. A pane's own
// event, which a subscription names the pane for, is dotted
// (pane.agent_status_changed) and its data has no type. See herdr's
// src/api/schema/events.rs and src/api/server.rs at herdrTargetVersion.
//
// The events come from tuios's event hub (daemon_events.go), read the way a
// subscribe verb reads them: the caller must be allowed subscribe, and a
// pane without admin sees only the sessions it may read (eventInScope).
//
//	tuios event         herdr event
//	session-created     workspace_created
//	session-closed      workspace_closed (workspace_renamed after a rename)
//	window-created      pane_created, and tab_created for a new tab
//	window-closed       pane_closed, and tab_closed for a tab left empty
//	window-exit         pane_exited
//	window-retitled     pane_updated
//	window-focused      pane_focused
//	window-moved        pane_moved
//	workspace-switched  tab_focused
//	agent-state         pane_agent_detected when an agent comes or goes, and
//	                    pane.agent_status_changed when its status changes
//
// herdr has no event for a lost one. When the stream falls behind, tuios
// writes herdr's error line with code events_lost and closes, and the client
// reads the state again, as it does after any reconnect.

// herdrEventQueue is the events one stream may fall behind by.
const herdrEventQueue = 1024

// herdrSubscription is one entry of events.subscribe.
type herdrSubscription struct {
	Type   string `json:"type"`
	PaneID string `json:"pane_id"`
	// window is the tuios window a pane subscription is about.
	window string
}

// herdrGlobalEvents are the subscription types that name no pane.
var herdrGlobalEvents = []string{
	"workspace.created", "workspace.updated", "workspace.metadata_updated", "workspace.renamed",
	"workspace.moved", "workspace.reordered", "workspace.closed", "workspace.focused",
	"worktree.created", "worktree.opened", "worktree.removed",
	"tab.created", "tab.closed", "tab.focused", "tab.renamed", "tab.moved",
	"pane.created", "pane.closed", "pane.updated", "pane.focused", "pane.moved", "pane.exited",
	"pane.agent_detected", "layout.updated",
}

// herdrPaneEvents are the subscription types that name a pane. tuios sends
// only the first: it has no stream of scroll positions, and matches output
// with pane.wait_for_output instead.
var herdrPaneEvents = []string{"pane.agent_status_changed", "pane.scroll_changed", "pane.output_matched"}

// herdrHubTypes are the tuios events the stream reads.
var herdrHubTypes = map[string]bool{
	EventSessionCreated: true, EventSessionClosed: true, EventWindowCreated: true,
	EventWindowClosed: true, EventWindowExit: true, EventWindowRetitled: true,
	EventWindowFocused: true, EventWindowMoved: true, EventWorkspaceSwitched: true,
	EventAgentState: true,
}

// herdrParseSubscriptions reads and checks events.subscribe's list. One
// entry herdr does not know refuses the whole call, as herdr's does.
func (d *Daemon) herdrParseSubscriptions(params json.RawMessage) ([]herdrSubscription, *herdrError) {
	var p struct {
		Subscriptions []herdrSubscription `json:"subscriptions"`
	}
	if herr := herdrDecode(params, &p, "subscriptions"); herr != nil {
		return nil, herr
	}
	for i := range p.Subscriptions {
		s := &p.Subscriptions[i]
		switch {
		case slices.Contains(herdrGlobalEvents, s.Type):
		case slices.Contains(herdrPaneEvents, s.Type):
			if s.PaneID == "" {
				return nil, herdrErr("invalid_request", "invalid request: missing field `pane_id`")
			}
			if s.Type != "pane.agent_status_changed" {
				return nil, herdrErr("unsupported", "tuios does not send "+s.Type+". Use pane.wait_for_output to wait for output")
			}
			_, win, ierr := d.herdrFindPane(s.PaneID)
			if ierr != nil {
				return nil, ierr.herdr()
			}
			s.window = win.ID
		default:
			return nil, herdrErr("invalid_request", "invalid request: unknown variant `"+s.Type+"`")
		}
	}
	return p.Subscriptions, nil
}

// herdrEvent is one line of the stream.
type herdrEvent struct {
	Event string          `json:"event"`
	Data  *herdrEventData `json:"data"`
	// sub is the subscription type that admits it.
	sub string
	// window is the tuios window a pane's own event is about.
	window string
}

// herdrEventData is the data of every event herdr's stream carries, each
// field under herdr's name and left out when the event has none. A global
// event carries its own kind as type; a pane's own event does not.
type herdrEventData struct {
	Type                string          `json:"type,omitempty"`
	PaneID              string          `json:"pane_id,omitempty"`
	WorkspaceID         string          `json:"workspace_id,omitempty"`
	TabID               string          `json:"tab_id,omitempty"`
	Label               string          `json:"label,omitempty"`
	Agent               string          `json:"agent,omitempty"`
	AgentStatus         string          `json:"agent_status,omitempty"`
	Released            bool            `json:"released,omitempty"`
	FinalStatus         string          `json:"final_status,omitempty"`
	PreviousPaneID      string          `json:"previous_pane_id,omitempty"`
	PreviousWorkspaceID string          `json:"previous_workspace_id,omitempty"`
	PreviousTabID       string          `json:"previous_tab_id,omitempty"`
	Pane                *herdrPaneInfo  `json:"pane,omitempty"`
	Tab                 *herdrTab       `json:"tab,omitempty"`
	Workspace           *herdrWorkspace `json:"workspace,omitempty"`
}

// herdrPaneMemo is what the stream remembers of a window, to say what an
// event changed: where the window was, and its agent and status.
type herdrPaneMemo struct {
	sessionID string
	workspace int
	agent     string
	status    string
}

// herdrTranslator turns tuios events into herdr events for one stream.
type herdrTranslator struct {
	d        *Daemon
	panes    map[string]herdrPaneMemo
	sessions map[string]string // session name -> id
}

func (d *Daemon) newHerdrTranslator() *herdrTranslator {
	t := &herdrTranslator{d: d, panes: map[string]herdrPaneMemo{}, sessions: map[string]string{}}
	for _, s := range d.manager.AllSessions() {
		t.remember(s)
	}
	return t
}

// remember records a session and its windows as they are now.
func (t *herdrTranslator) remember(s *Session) {
	t.sessions[s.Name()] = s.ID
	for _, w := range s.GetState().Windows {
		t.panes[w.ID] = herdrMemoOf(s, &w)
	}
}

func herdrMemoOf(s *Session, w *WindowState) herdrPaneMemo {
	m := herdrPaneMemo{sessionID: s.ID, workspace: max(w.Workspace, 1), status: "unknown"}
	if w.AgentHarness != "" && w.AgentState != AgentStateNone {
		m.agent = herdrAgentLabel(w.AgentHarness)
		m.status = herdrStatus(w.AgentState)
	}
	return m
}

// view reads one session for a record.
func (t *herdrTranslator) view(s *Session) *herdrView {
	v := &herdrView{}
	t.d.addHerdrSession(v, s, s.GetState(), 1, false)
	return v
}

// global is a global event: kind in snake_case, and the same kind as the
// data's type.
func global(kind string, data *herdrEventData) herdrEvent {
	data.Type = kind
	return herdrEvent{Event: kind, Data: data, sub: strings.Replace(kind, "_", ".", 1)}
}

// translate is the herdr events for one tuios event.
func (t *herdrTranslator) translate(ev streamEvent) []herdrEvent {
	sess := t.d.manager.GetSession(ev.Session)
	switch ev.Type {
	case EventSessionCreated:
		if sess == nil {
			return nil
		}
		renamed := false
		for _, id := range t.sessions {
			renamed = renamed || id == sess.ID
		}
		t.remember(sess)
		w := &t.view(sess).workspaces[0]
		if renamed {
			return []herdrEvent{global("workspace_renamed", &herdrEventData{WorkspaceID: w.WorkspaceID, Label: w.Label})}
		}
		return []herdrEvent{global("workspace_created", &herdrEventData{Workspace: w})}
	case EventSessionClosed:
		id, ok := t.sessions[ev.Session]
		if !ok {
			return nil
		}
		delete(t.sessions, ev.Session)
		if t.d.manager.GetSessionByID(id) != nil {
			// A rename closes the old name. The session lives on, and its
			// session-created under the new name says so.
			return nil
		}
		for w, m := range t.panes {
			if m.sessionID == id {
				delete(t.panes, w)
			}
		}
		return []herdrEvent{global("workspace_closed", &herdrEventData{WorkspaceID: herdrWorkspaceID(id)})}
	case EventWindowClosed, EventWindowExit:
		return t.closed(ev)
	}
	if sess == nil {
		return nil
	}
	wsID := herdrWorkspaceID(sess.ID)
	if ev.Type == EventWorkspaceSwitched {
		return []herdrEvent{global("tab_focused", &herdrEventData{TabID: herdrTabID(sess.ID, max(ev.Workspace, 1)), WorkspaceID: wsID})}
	}
	if ev.Window == "" {
		return nil
	}
	paneID := herdrPaneID(sess.ID, ev.Window)
	switch ev.Type {
	case EventWindowFocused:
		return []herdrEvent{global("pane_focused", &herdrEventData{PaneID: paneID, WorkspaceID: wsID})}
	case EventAgentState:
		return t.agentState(sess, ev.Window, paneID, wsID)
	}
	v := t.view(sess)
	p := v.pane(paneID)
	if p == nil {
		return nil
	}
	prev := t.panes[ev.Window]
	w, _ := findWindowState(sess.GetState(), ev.Window)
	t.panes[ev.Window] = herdrMemoOf(sess, &w)
	switch ev.Type {
	case EventWindowCreated:
		out := []herdrEvent{global("pane_created", &herdrEventData{Pane: p})}
		if countOnWorkspace(sess.GetState(), max(w.Workspace, 1)) == 1 {
			if tb := v.tab(p.TabID); tb != nil {
				out = append([]herdrEvent{global("tab_created", &herdrEventData{Tab: tb})}, out...)
			}
		}
		return out
	case EventWindowRetitled:
		return []herdrEvent{global("pane_updated", &herdrEventData{Pane: p})}
	case EventWindowMoved:
		from := cmp.Or(prev.sessionID, sess.ID)
		return []herdrEvent{global("pane_moved", &herdrEventData{
			PreviousPaneID: herdrPaneID(from, ev.Window), PreviousWorkspaceID: herdrWorkspaceID(from),
			PreviousTabID: herdrTabID(from, max(prev.workspace, 1)), Pane: p,
		})}
	}
	return nil
}

// closed is pane_closed or pane_exited for a window, with tab_closed when
// the window was the last on a workspace that is not a tab any more.
func (t *herdrTranslator) closed(ev streamEvent) []herdrEvent {
	m, ok := t.panes[ev.Window]
	if !ok {
		return nil
	}
	data := &herdrEventData{PaneID: herdrPaneID(m.sessionID, ev.Window), WorkspaceID: herdrWorkspaceID(m.sessionID)}
	if ev.Type == EventWindowExit {
		return []herdrEvent{global("pane_exited", data)}
	}
	delete(t.panes, ev.Window)
	out := []herdrEvent{global("pane_closed", data)}
	if sess := t.d.manager.GetSessionByID(m.sessionID); sess != nil {
		st := sess.GetState()
		if !herdrListedWorkspace(st, m.workspace) {
			out = append(out, global("tab_closed", &herdrEventData{TabID: herdrTabID(m.sessionID, m.workspace), WorkspaceID: data.WorkspaceID}))
		}
	}
	return out
}

// agentState is what one agent-state event says: an agent that came or
// went, and a status that changed.
func (t *herdrTranslator) agentState(sess *Session, window, paneID, wsID string) []herdrEvent {
	w, ok := findWindowState(sess.GetState(), window)
	if !ok {
		return nil
	}
	prev := t.panes[window]
	now := herdrMemoOf(sess, &w)
	t.panes[window] = now
	var out []herdrEvent
	if prev.agent != now.agent {
		data := &herdrEventData{PaneID: paneID, WorkspaceID: wsID, Agent: now.agent}
		if now.agent == "" {
			data.Agent, data.Released, data.FinalStatus = prev.agent, true, prev.status
		}
		out = append(out, global("pane_agent_detected", data))
	}
	if prev.status != now.status {
		out = append(out, herdrEvent{
			Event: "pane.agent_status_changed", sub: "pane.agent_status_changed", window: window,
			Data: &herdrEventData{PaneID: paneID, WorkspaceID: wsID, AgentStatus: now.status, Agent: now.agent},
		})
	}
	return out
}

// herdrWanted reports whether a subscription list admits an event.
func herdrWanted(subs []herdrSubscription, e herdrEvent) bool {
	for _, s := range subs {
		if s.Type == e.sub && (s.window == "" || s.window == e.window) {
			return true
		}
	}
	return false
}

// herdrSubscribe opens the tuios event stream for a herdr stream, after the
// caller passes the check a subscribe verb passes.
func (d *Daemon) herdrSubscribe(cs *connState) (*eventSub, *herdrError) {
	if _, _, verr := d.admitVerb(cs, "subscribe", nil); verr != nil {
		return nil, herdrFromVerb(verr, "forbidden")
	}
	sub, _, err := d.events.subscribeFrom(eventFilter{types: herdrHubTypes}, herdrEventQueue, nil)
	if err != nil {
		return nil, herdrErr("internal_error", err.Error())
	}
	return sub, nil
}

// serveHerdrEvents answers events.subscribe: the ack, then events until the
// client closes the connection or the daemon stops.
func (d *Daemon) serveHerdrEvents(cs *connState, id string, params json.RawMessage) {
	subs, herr := d.herdrParseSubscriptions(params)
	if herr != nil {
		writeHerdr(cs.conn, id, nil, herr.code, herr.msg)
		return
	}
	sub, herr := d.herdrSubscribe(cs)
	if herr != nil {
		writeHerdr(cs.conn, id, nil, herr.code, herr.msg)
		return
	}
	defer d.events.unsubscribe(sub)
	_ = cs.conn.SetDeadline(time.Time{})
	if !d.herdrWriteLine(cs, herdrLine{ID: id, Result: &herdrResult{Type: "subscription_started"}}) {
		return
	}
	// The client sends nothing more. A read that returns is the client
	// closing the connection.
	gone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, cs.conn)
		close(gone)
	}()
	tr := d.newHerdrTranslator()
	for {
		select {
		case <-gone:
			return
		case <-d.ctx.Done():
			return
		case <-sub.stop:
			return
		case ev := <-sub.ch:
			if sub.dropped.Load() > 0 {
				writeHerdr(cs.conn, id, nil, "events_lost", "the stream fell behind and events were lost. Read the state again and subscribe again")
				return
			}
			if !d.eventInScope(cs, ev) {
				continue
			}
			for _, e := range tr.translate(ev) {
				if herdrWanted(subs, e) && !d.herdrWriteLine(cs, e) {
					return
				}
			}
		}
	}
}

// herdrLine is a reply line.
type herdrLine struct {
	ID     string       `json:"id"`
	Result *herdrResult `json:"result"`
}

// herdrWriteLine writes one JSON line, and reports whether it went.
func (d *Daemon) herdrWriteLine(cs *connState, v any) bool {
	data, err := json.Marshal(v)
	if err != nil {
		return false
	}
	_ = cs.conn.SetWriteDeadline(time.Now().Add(herdrIOTimeout))
	_, err = cs.conn.Write(append(data, '\n'))
	return err == nil
}

// herdrEventsWait answers events.wait. herdr answers only a match on
// pane_agent_status_changed, and so does this: wait-for agent-state on the
// pane, until its status is the one named. Any other match is herdr's
// unsupported_event_wait_match.
func (d *Daemon) herdrEventsWait(cs *connState, in *herdrIn) (*herdrResult, *herdrError) {
	kind, _ := in.MatchEvent["event"].(string)
	paneID, _ := in.MatchEvent["pane_id"].(string)
	status, _ := in.MatchEvent["agent_status"].(string)
	if kind != "pane_agent_status_changed" || paneID == "" || status == "" {
		return nil, herdrErr("unsupported_event_wait_match", "events.wait currently supports pane agent status matches")
	}
	if herr := d.herdrWaitAgent(cs, paneID, []string{status}, in.TimeoutMS); herr != nil {
		return nil, herr
	}
	res, herr := d.herdrPaneResult(cs, "pane_info", paneID)
	if herr != nil {
		return nil, herr
	}
	return &herdrResult{Type: "wait_matched", Event: &herdrEvent{Event: kind, Data: &herdrEventData{
		Type: kind, PaneID: res.Pane.PaneID, WorkspaceID: res.Pane.WorkspaceID, AgentStatus: res.Pane.AgentStatus, Agent: res.Pane.Agent,
	}}}, nil
}
