package session

import (
	"cmp"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

const herdrEventQueue = 1024

type herdrSubscription struct {
	Type   string `json:"type"`
	PaneID string `json:"pane_id"`
	window string
}

var herdrGlobalEvents = []string{
	"workspace.created", "workspace.updated", "workspace.metadata_updated", "workspace.renamed",
	"workspace.moved", "workspace.reordered", "workspace.closed", "workspace.focused",
	"worktree.created", "worktree.opened", "worktree.removed",
	"tab.created", "tab.closed", "tab.focused", "tab.renamed", "tab.moved",
	"pane.created", "pane.closed", "pane.updated", "pane.focused", "pane.moved", "pane.exited",
	"pane.agent_detected", "layout.updated",
}

var herdrPaneEvents = []string{"pane.agent_status_changed", "pane.scroll_changed", "pane.output_matched"}

var herdrHubTypes = map[string]bool{
	EventSessionCreated: true, EventSessionClosed: true, EventWindowCreated: true,
	EventWindowClosed: true, EventWindowExit: true, EventWindowRetitled: true,
	EventWindowFocused: true, EventWindowMoved: true, EventWorkspaceSwitched: true,
	EventWorkspaceRenamed: true, EventAgentState: true, EventAgentSeen: true,
	EventClientSessionChanged: true, EventSessionLabelled: true, EventWorktreeRemoved: true,
}

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

type herdrEvent struct {
	Event  string          `json:"event"`
	Data   *herdrEventData `json:"data"`
	sub    string
	window string
}

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
	Path                string          `json:"path,omitempty"`
	Pane                *herdrPaneInfo  `json:"pane,omitempty"`
	Tab                 *herdrTab       `json:"tab,omitempty"`
	Workspace           *herdrWorkspace `json:"workspace,omitempty"`
}

type herdrPaneMemo struct {
	sessionID string
	tabKey    int
	agent     string
	status    string
}

type herdrTranslator struct {
	d        *Daemon
	panes    map[string]herdrPaneMemo
	sessions map[string]string
	shows    map[string]string
	in       map[string]string
	labels   map[string]string
}

func (d *Daemon) newHerdrTranslator() *herdrTranslator {
	t := &herdrTranslator{d: d, panes: map[string]herdrPaneMemo{}, sessions: map[string]string{}, shows: map[string]string{}, in: map[string]string{}, labels: map[string]string{}}
	for _, s := range d.manager.AllSessions() {
		t.remember(s)
	}
	d.clientsMu.RLock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		if cs.isTUIClient && cs.attached {
			t.shows[cs.clientID] = cs.sessionID
			t.in[cs.clientID] = cs.sessionID
		}
		cs.mu.Unlock()
	}
	d.clientsMu.RUnlock()
	return t
}

func (t *herdrTranslator) clientMoved(ev streamEvent, sess *Session) []herdrEvent {
	if ev.Attached == nil || !*ev.Attached || sess == nil {
		delete(t.in, ev.ClientID)
		return nil
	}
	t.in[ev.ClientID] = sess.ID
	prev, seen := t.shows[ev.ClientID]
	t.shows[ev.ClientID] = sess.ID
	if !seen || prev == sess.ID {
		return nil
	}
	st := sess.GetState()
	wsID := herdrWorkspaceID(sess.ID)
	out := []herdrEvent{
		global("workspace_focused", &herdrEventData{WorkspaceID: wsID}),
		global("tab_focused", &herdrEventData{TabID: herdrTabID(sess.ID, st.WorkspaceTabKey(max(st.CurrentWorkspace, 1))), WorkspaceID: wsID}),
	}
	if w, ok := findWindowState(st, st.FocusedWindowID); ok && !herdrScratch(&w) {
		out = append(out, global("pane_focused", &herdrEventData{PaneID: herdrPaneID(sess.ID, w.ID), WorkspaceID: wsID}))
	}
	return out
}

func (t *herdrTranslator) active(sessionID string) bool {
	if len(t.in) == 0 {
		return true
	}
	for _, id := range t.in {
		if id == sessionID {
			return true
		}
	}
	return false
}

func (t *herdrTranslator) remember(s *Session) {
	t.sessions[s.Name()] = s.ID
	st := s.GetState()
	t.labels[s.ID] = herdrWorkspaceLabel(s, st)
	for _, w := range st.Windows {
		if !herdrScratch(&w) {
			t.panes[w.ID] = herdrMemoOf(s, st, &w)
		}
	}
}

func herdrMemoOf(sess *Session, st *SessionState, w *WindowState) herdrPaneMemo {
	m := herdrPaneMemo{sessionID: sess.ID, tabKey: st.WorkspaceTabKey(max(w.Workspace, 1)), status: "unknown"}
	if w.AgentHarness != "" && w.AgentState != AgentStateNone {
		m.agent = herdrAgentLabel(w.AgentHarness)
		m.status = herdrStatus(w.AgentState, sess.finishedUnread(w))
	}
	return m
}

func (t *herdrTranslator) view(s *Session) *herdrView {
	v := &herdrView{}
	t.d.addHerdrSession(v, s, s.GetState(), 1, false)
	return v
}

func global(kind string, data *herdrEventData) herdrEvent {
	data.Type = kind
	return herdrEvent{Event: kind, Data: data, sub: strings.Replace(kind, "_", ".", 1)}
}

func (t *herdrTranslator) translate(ev streamEvent) []herdrEvent {
	sess := t.d.manager.GetSession(ev.Session)
	switch ev.Type {
	case EventSessionCreated:
		if sess == nil {
			return nil
		}
		renamed := false
		for name, id := range t.sessions {
			if id == sess.ID {
				renamed = true
				delete(t.sessions, name)
			}
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
		if t.d.manager.GetSessionByID(id) != nil {
			return nil
		}
		delete(t.sessions, ev.Session)
		delete(t.labels, id)
		for w, m := range t.panes {
			if m.sessionID == id {
				delete(t.panes, w)
			}
		}
		return []herdrEvent{global("workspace_closed", &herdrEventData{WorkspaceID: herdrWorkspaceID(id)})}
	case EventSessionLabelled:
		if sess == nil {
			return nil
		}
		label := herdrWorkspaceLabel(sess, sess.GetState())
		if prev, ok := t.labels[sess.ID]; !ok || prev == label {
			return nil
		}
		t.labels[sess.ID] = label
		return []herdrEvent{global("workspace_renamed", &herdrEventData{WorkspaceID: herdrWorkspaceID(sess.ID), Label: label})}
	case EventWindowClosed, EventWindowExit:
		return t.closed(ev)
	case EventWorktreeRemoved:
		data := &herdrEventData{Path: ev.Path}
		if sess != nil {
			data.WorkspaceID = herdrWorkspaceID(sess.ID)
		} else if id, ok := t.sessions[ev.Session]; ok {
			data.WorkspaceID = herdrWorkspaceID(id)
		}
		return []herdrEvent{global("worktree_removed", data)}
	case EventClientSessionChanged:
		return t.clientMoved(ev, sess)
	}
	if sess == nil {
		return nil
	}
	wsID := herdrWorkspaceID(sess.ID)
	if ev.Type == EventWorkspaceSwitched {
		if IsScratchWorkspace(ev.Workspace) {
			return nil
		}
		return []herdrEvent{global("tab_focused", &herdrEventData{TabID: herdrTabID(sess.ID, sess.GetState().WorkspaceTabKey(max(ev.Workspace, 1))), WorkspaceID: wsID})}
	}
	if ev.Type == EventWorkspaceRenamed {
		return t.renamed(sess, ev)
	}
	if ev.Window == "" {
		return nil
	}
	if w, ok := findWindowState(sess.GetState(), ev.Window); ok && herdrScratch(&w) {
		return nil
	}
	paneID := herdrPaneID(sess.ID, ev.Window)
	switch ev.Type {
	case EventWindowFocused:
		if !t.active(sess.ID) {
			return nil
		}
		return []herdrEvent{global("pane_focused", &herdrEventData{PaneID: paneID, WorkspaceID: wsID})}
	case EventAgentState, EventAgentSeen:
		return t.agentState(sess, ev.Window, paneID, wsID)
	}
	v := t.view(sess)
	p := v.pane(paneID)
	if p == nil {
		return nil
	}
	prev := t.panes[ev.Window]
	st := sess.GetState()
	w, _ := findWindowState(st, ev.Window)
	t.panes[ev.Window] = herdrMemoOf(sess, st, &w)
	switch ev.Type {
	case EventWindowCreated:
		out := []herdrEvent{global("pane_created", &herdrEventData{Pane: p})}
		if herdrCount(st, max(w.Workspace, 1)) == 1 {
			if tb := v.tab(p.TabID); tb != nil {
				out = append([]herdrEvent{global("tab_created", &herdrEventData{Tab: tb})}, out...)
			}
		}
		return out
	case EventWindowRetitled:
		return []herdrEvent{global("pane_updated", &herdrEventData{Pane: p})}
	case EventWindowMoved:
		from := cmp.Or(prev.sessionID, sess.ID)
		if from == sess.ID && prev.tabKey == t.panes[ev.Window].tabKey {
			return nil
		}
		return []herdrEvent{global("pane_moved", &herdrEventData{
			PreviousPaneID: herdrPaneID(from, ev.Window), PreviousWorkspaceID: herdrWorkspaceID(from),
			PreviousTabID: herdrTabID(from, prev.tabKey), Pane: p,
		})}
	}
	return nil
}

func (t *herdrTranslator) renamed(sess *Session, ev streamEvent) []herdrEvent {
	st := sess.GetState()
	if IsScratchWorkspace(ev.Workspace) || (herdrCount(st, ev.Workspace) == 0 && ev.Workspace != st.CurrentWorkspace) {
		return nil
	}
	return []herdrEvent{global("tab_renamed", &herdrEventData{
		TabID: herdrTabID(sess.ID, st.WorkspaceTabKey(ev.Workspace)), WorkspaceID: herdrWorkspaceID(sess.ID),
		Label: herdrTabLabel(ev.Title, ev.Workspace),
	})}
}

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
		if ws := st.WorkspaceForTabKey(m.tabKey); ws == 0 || !herdrListedWorkspace(st, ws) {
			out = append(out, global("tab_closed", &herdrEventData{TabID: herdrTabID(m.sessionID, m.tabKey), WorkspaceID: data.WorkspaceID}))
		}
	}
	return out
}

func (t *herdrTranslator) agentState(sess *Session, window, paneID, wsID string) []herdrEvent {
	st := sess.GetState()
	w, ok := findWindowState(st, window)
	if !ok {
		return nil
	}
	prev := t.panes[window]
	now := herdrMemoOf(sess, st, &w)
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

func herdrWanted(subs []herdrSubscription, e herdrEvent) bool {
	for _, s := range subs {
		if s.Type == e.sub && (s.window == "" || s.window == e.window) {
			return true
		}
	}
	return false
}

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

func (d *Daemon) serveHerdrEvents(cs *connState, id string, params json.RawMessage) {
	sub, herr := d.herdrSubscribe(cs)
	if herr != nil {
		writeHerdr(cs.conn, id, nil, herr.code, herr.msg)
		return
	}
	defer d.events.unsubscribe(sub)
	subs, herr := d.herdrParseSubscriptions(params)
	if herr != nil {
		writeHerdr(cs.conn, id, nil, herr.code, herr.msg)
		return
	}
	_ = cs.conn.SetDeadline(time.Time{})
	tr := d.newHerdrTranslator()
	if !d.herdrWriteLine(cs, herdrLine{ID: id, Result: &herdrResult{Type: "subscription_started"}}) {
		return
	}
	gone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, cs.conn)
		close(gone)
	}()
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

type herdrLine struct {
	ID     string       `json:"id"`
	Result *herdrResult `json:"result"`
}

func (d *Daemon) herdrWriteLine(cs *connState, v any) bool {
	data, err := jsonx.Marshal(v)
	if err != nil {
		return false
	}
	_ = cs.conn.SetWriteDeadline(time.Now().Add(herdrIOTimeout))
	_, err = cs.conn.Write(append(data, '\n'))
	return err == nil
}

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
