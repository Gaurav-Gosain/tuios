package session

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
)

// An agent pane's activity: the prompts, tool calls, tool results, finished
// turns, subagents and conversation starts its hooks reported, kept in a
// bounded ring per pane in daemon memory, and the recap computed from it.
//
// The ring is written only by the pane's own reports: activity rides
// set-agent-state, which is scopeSelf and passes the identity guard first. It
// is display only, and nothing reads it to decide a state, a wait or an alert.
// agent-activity reads it: scopeRead with fan-group reach, and a link needs
// list. Its text is the agent's, cleaned by attentionText and marked
// untrusted.
//
// A pane gets a ring on the first report that carries activity, so a plain
// shell pane, and an agent pane whose harness has no hooks, costs nothing.
// Once it has one, the pane's OSC 133 commands and its agent state changes are
// added to it as well, from the session event sink. The ring dies with the
// window, the session or the daemon; it is never saved.

// Sizes of the activity ring and what one entry may hold.
const (
	// activityRingSize is how many entries one pane keeps. An older entry is
	// dropped for a newer one.
	activityRingSize = 256
	// agentActivityMaxLimit bounds one agent-activity answer.
	agentActivityMaxLimit = activityRingSize
	// agentActivityDefaultLimit is what an agent-activity call without a
	// limit gets.
	agentActivityDefaultLimit = 64
	// activityTextMax bounds an entry's text and target, in bytes.
	activityTextMax = 160
	// activityToolMax bounds a tool name, in bytes.
	activityToolMax = 64
	// activityFileMax bounds one file path, in bytes.
	activityFileMax = 256
	// activityFilesMax is how many files one entry keeps.
	activityFilesMax = 16
	// recapFilesShown is how many files a recap lists. files_total counts all.
	recapFilesShown = 20
)

// Activity entry kinds. They are wire values: agent-activity returns them and
// the agent-activity event carries them.
const (
	// ActivityPrompt is a prompt the person submitted. Text is its first line.
	ActivityPrompt = "prompt"
	// ActivityTool is a tool call starting. Tool names it, Target says what it
	// acts on.
	ActivityTool = "tool"
	// ActivityToolDone is a tool call that finished. OK says how, when known,
	// and Files are the files it wrote.
	ActivityToolDone = "tool_done"
	// ActivityToolFailed is a tool call that failed. Text is the error.
	ActivityToolFailed = "tool_failed"
	// ActivityTurnEnd is the agent finishing a turn. Text is the first line
	// of what it said last.
	ActivityTurnEnd = "turn_end"
	// ActivityCommand is a command the pane's shell ran, from its OSC 133
	// marks. Target is the command line and Exit its status.
	ActivityCommand = "command"
	// ActivityState is the pane's agent state changing. Text is the new state.
	ActivityState = "state"
	// ActivitySubagentStart is a subagent the agent handed work to starting,
	// or an agent-team teammate waking to work. Text names it: its
	// description and type, "Research tmux (general-purpose)", or its type
	// alone when no launch described it.
	ActivitySubagentStart = "subagent_start"
	// ActivitySubagentStop is a subagent that finished, failed or was stopped,
	// or a teammate going idle. Text names it as for a start, Outcome says how
	// it ended, DurationMS how long it ran and Tools how many tool calls it
	// made.
	ActivitySubagentStop = "subagent_stop"
	// ActivitySubagentUpdate is news about a subagent: the tool it runs, its
	// description, how it is ending. report-agent-activity takes it and the
	// ring keeps none, so a busy subagent does not flood its parent's ring.
	ActivitySubagentUpdate = "subagent_update"
	// ActivitySessionStart is the agent starting a conversation: a new one, a
	// resumed one, or a fresh one after a clear. Text says which.
	ActivitySessionStart = "session_start"
)

// Reserved agent metadata keys. Only the daemon writes them, from hook
// activity, and set-agent-meta refuses them from every caller.
const (
	// AgentMetaNow is what the agent is doing now: the tool it is running and
	// on what, such as "Bash: go test ./...". It is cleared when the tool
	// fails, when a turn ends, when a new prompt starts, and when the pane
	// comes to rest.
	AgentMetaNow = "now"
	// AgentMetaPrompt is the first line of the last prompt submitted.
	AgentMetaPrompt = "prompt"
	// AgentMetaSubagents is how many subagents the agent is running, as a
	// value that reads on its own: "1 subagent", "3 subagents". It is absent
	// while there are none. See agent_subagents.go.
	AgentMetaSubagents = "subagents"
	// AgentMetaModel is the model the harness named. It is not reserved: a
	// status line may write it too.
	AgentMetaModel = "model"
	// agentMetaActivitySource is the source recorded on the reserved keys.
	agentMetaActivitySource = "activity"
	// agentMetaHookSource is the source recorded on a model a hook named.
	agentMetaHookSource = "hook"
)

// reservedAgentMetaKeys are the keys set-agent-meta refuses and its clear
// leaves alone.
var reservedAgentMetaKeys = []string{AgentMetaNow, AgentMetaPrompt, AgentMetaSubagents}

// AgentActivityEntry is one entry of a pane's activity ring.
type AgentActivityEntry struct {
	// Seq numbers the pane's entries from 1. It is per pane and per daemon
	// start.
	Seq uint64 `json:"seq"`
	// At is when the daemon recorded it, in unix nanoseconds.
	At   int64  `json:"at"`
	Kind string `json:"kind"`
	// Tool and Target name a tool call and what it acts on. A command entry
	// carries its command line in Target.
	Tool   string `json:"tool,omitempty"`
	Target string `json:"target,omitempty"`
	// Files are the files a finished tool call wrote.
	Files []string `json:"files,omitempty"`
	// OK says how a tool call ended, nil when nothing said.
	OK *bool `json:"ok,omitempty"`
	// Exit is a command's exit status, nil when the shell sent none.
	Exit *int `json:"exit,omitempty"`
	// Text is a prompt's first line, a failure, the first line a turn ended
	// with, a state entry's new state, or the subagent a subagent entry is
	// about.
	Text string `json:"text,omitempty"`
	// Outcome is how a subagent_stop entry's subagent ended: done, failed
	// or stopped. Empty from a daemon before it.
	Outcome string `json:"outcome,omitempty"`
	// DurationMS is how long that subagent ran, and Tools how many tool
	// calls it made.
	DurationMS int64 `json:"duration_ms,omitempty"`
	Tools      int   `json:"tools,omitempty"`

	// turns is how many turns the pane finished with a state entry: its
	// completion_seq delta. The recap adds them up. Never on the wire.
	turns uint64
}

// activityRing is one pane's entries, oldest first, in a circular buffer.
type activityRing struct {
	entries []AgentActivityEntry
	start   int
	// seq is the Seq of the newest entry, which is also how many entries the
	// pane has had.
	seq uint64
}

// push adds e as the newest entry, dropping the oldest when full, and returns
// it numbered.
func (r *activityRing) push(e AgentActivityEntry) AgentActivityEntry {
	r.seq++
	e.Seq = r.seq
	if len(r.entries) < activityRingSize {
		r.entries = append(r.entries, e)
		return e
	}
	r.entries[r.start] = e
	r.start = (r.start + 1) % len(r.entries)
	return e
}

// ordered returns a copy of the entries, oldest first.
func (r *activityRing) ordered() []AgentActivityEntry {
	out := make([]AgentActivityEntry, 0, len(r.entries))
	out = append(out, r.entries[r.start:]...)
	return append(out, r.entries[:r.start]...)
}

// dropped reports whether the ring has let go of an entry.
func (r *activityRing) dropped() bool {
	return r.seq > uint64(len(r.entries))
}

// activityKey names a pane: its session's id and its window id. The id rather
// than the name, so a renamed session keeps its rings.
type activityKey struct {
	session string
	window  string
}

// activityStore holds every pane's ring. It has its own lock and never calls
// into a session while holding it, so the session event sink, which runs with
// the session's state lock held, may add to it.
type activityStore struct {
	mu      sync.Mutex
	rings   map[activityKey]*activityRing
	publish func(streamEvent)
}

func newActivityStore(publish func(streamEvent)) *activityStore {
	return &activityStore{rings: make(map[activityKey]*activityRing), publish: publish}
}

// add records e for the pane. create says whether a pane with no ring gets
// one; without it an entry for such a pane is dropped. It reports whether the
// entry was kept. Every kept entry is published as an agent-activity event.
func (a *activityStore) add(sessionID, sessionName, windowID string, e AgentActivityEntry, create bool) bool {
	if a == nil || windowID == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := activityKey{sessionID, windowID}
	ring, ok := a.rings[key]
	if !ok {
		if !create {
			return false
		}
		ring = &activityRing{}
		a.rings[key] = ring
	}
	if e.At == 0 {
		e.At = time.Now().UnixNano()
	}
	e = ring.push(e)
	if a.publish != nil {
		// Published under the store's lock, so two entries of one pane reach
		// a subscriber in the order of their seq. The hub's lock is a leaf.
		entry := e
		a.publish(streamEvent{Type: EventAgentActivity, Session: sessionName, Window: windowID, Entry: &entry})
	}
	return true
}

// ensure gives the pane a ring if it has none, and reports whether it made
// one.
func (a *activityStore) ensure(sessionID, windowID string) bool {
	if a == nil || windowID == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := activityKey{sessionID, windowID}
	if _, ok := a.rings[key]; ok {
		return false
	}
	a.rings[key] = &activityRing{}
	return true
}

// has reports whether the pane has a ring.
func (a *activityStore) has(sessionID, windowID string) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.rings[activityKey{sessionID, windowID}]
	return ok
}

// read returns a copy of the pane's entries, oldest first, whether it has a
// ring, and whether the ring has dropped any.
func (a *activityStore) read(sessionID, windowID string) ([]AgentActivityEntry, bool, bool) {
	if a == nil {
		return nil, false, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	ring, ok := a.rings[activityKey{sessionID, windowID}]
	if !ok {
		return nil, false, false
	}
	return ring.ordered(), true, ring.dropped()
}

// forgetWindow drops a closed window's ring.
func (a *activityStore) forgetWindow(sessionID, windowID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.rings, activityKey{sessionID, windowID})
}

// forgetIfEmpty drops the pane's ring when nothing was ever added to it: a
// ring ensure made for a report that then did not record anything.
func (a *activityStore) forgetIfEmpty(sessionID, windowID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := activityKey{sessionID, windowID}
	if ring, ok := a.rings[key]; ok && ring.seq == 0 {
		delete(a.rings, key)
	}
}

// forgetSession drops every ring of a session that ended.
func (a *activityStore) forgetSession(sessionID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for k := range a.rings {
		if k.session == sessionID {
			delete(a.rings, k)
		}
	}
}

// noteSessionEvent adds what a session event says to the ring of a pane that
// has one: a command its shell finished, or its agent state changing. A closed
// window's ring is dropped. It runs in the session event sink, with the
// session's state lock held, so it reads nothing from the session.
func (a *activityStore) noteSessionEvent(s *Session, ev SessionEvent) {
	if a == nil || s == nil {
		return
	}
	switch ev.Type {
	case EventCommandFinished:
		var exit *int
		if ev.ExitCode != nil {
			c := *ev.ExitCode
			exit = &c
		}
		// Checked first, so a plain shell pane costs one map lookup and no
		// text cleaning. The command line is cleaned and cut like every other
		// target: shell_commands.go keeps up to shellCmdlineMax bytes of it,
		// and the ring keeps activityTextMax.
		if !a.has(s.ID, ev.Window) {
			return
		}
		target := attentionText(ev.Cmdline, activityTextMax)
		a.add(s.ID, s.Name(), ev.Window, AgentActivityEntry{Kind: ActivityCommand, Target: target, Exit: exit}, false)
	case EventAgentState:
		e := AgentActivityEntry{Kind: ActivityState, Text: ev.State}
		if ev.completionSeq > ev.prevCompletionSeq {
			e.turns = ev.completionSeq - ev.prevCompletionSeq
		}
		a.add(s.ID, s.Name(), ev.Window, e, false)
	case EventWindowClosed:
		a.forgetWindow(s.ID, ev.Window)
	}
}

// AgentActivityReport is set-agent-state's activity parameter: one hook event
// of the pane's own agent.
type AgentActivityReport struct {
	// Event is what happened, one of activityEvents.
	Event string `json:"event"`
	// Tool and Target name the tool and what it acted on, for a tool event.
	Tool   string `json:"tool,omitempty"`
	Target string `json:"target,omitempty"`
	// Text is a prompt's first line, a failure, or what a turn ended with.
	Text string `json:"text,omitempty"`
	// Files are the files a tool call wrote.
	Files []string `json:"files,omitempty"`
	// OK says whether a finished tool call succeeded, nil when unknown.
	OK *bool `json:"ok,omitempty"`
	// Model is the model the harness named, when it did.
	Model string `json:"model,omitempty"`
	// AgentID and AgentType name the subagent of a subagent event: the
	// harness's id for it, which pairs its start with its stop, and its type.
	AgentID   string `json:"agent_id,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
	// CallID is the harness's id for a tool call: on a spawn hint the call
	// that launches a subagent, and on a subagent_update without agent_id
	// the call whose subagent it is about.
	CallID string `json:"call_id,omitempty"`
	// Spawn marks a tool event, or a subagent_update, as the launch of a
	// subagent: AgentType is the type asked for and Target the description,
	// which the next subagent to start on the pane takes.
	Spawn bool `json:"spawn,omitempty"`
	// Outcome is how a subagent_update says its subagent is ending: stopping
	// (the agent asked to stop it) or failed (Text is the error).
	Outcome string `json:"outcome,omitempty"`
	// Tools is how many tool calls the subagent made, when the harness said.
	Tools int `json:"tools,omitempty"`
}

// subagentOutcomes are the outcomes a subagent_update takes.
var subagentOutcomes = []string{subagentOutcomeStopping, subagentOutcomeFailed}

// subagentToolsMax bounds a reported tool count.
const subagentToolsMax = 1_000_000

// checkActivityReport refuses an activity whose event is not one of events,
// the verb's, and a subagent event without an id the daemon can keep. A
// report without one is not checked.
func checkActivityReport(a *AgentActivityReport, events []string) *verbError {
	if a == nil {
		return nil
	}
	if !slices.Contains(events, a.Event) {
		return invalidParam("activity", "activity.event is one of the activity events", events...)
	}
	idRule := "1 to " + strconv.Itoa(integration.SubagentIDMax) + " letters, digits, '_', '.', ':', '@' or '-'"
	if subagentEvent(a.Event) && !integration.ValidSubagentID(a.AgentID) {
		return invalidParam("activity", "a subagent event needs activity.agent_id: "+idRule)
	}
	if a.Event == ActivitySubagentUpdate {
		if a.AgentID == "" && a.CallID == "" {
			return invalidParam("activity", "a subagent_update needs activity.agent_id, or activity.call_id for the call that launched the subagent")
		}
		if a.AgentID != "" && !integration.ValidSubagentID(a.AgentID) {
			return invalidParam("activity", "activity.agent_id is "+idRule)
		}
	}
	if a.CallID != "" && !integration.ValidSubagentID(a.CallID) {
		return invalidParam("activity", "activity.call_id is "+idRule)
	}
	if a.Outcome != "" && (a.Event != ActivitySubagentUpdate || !slices.Contains(subagentOutcomes, a.Outcome)) {
		return invalidParam("activity", "activity.outcome is for a subagent_update, one of the outcomes", subagentOutcomes...)
	}
	if a.Tools < 0 || a.Tools > subagentToolsMax {
		return invalidParam("activity", "activity.tools is 0 to "+strconv.Itoa(subagentToolsMax))
	}
	return nil
}

// firstLine is s up to its first line break, trimmed.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// activityEntryOf cleans a hook's report into a ring entry. Every string is
// the agent's, so each is cut to one line, stripped of control characters and
// masked for secrets before it is kept.
func activityEntryOf(r *AgentActivityReport) AgentActivityEntry {
	e := AgentActivityEntry{
		Kind:   r.Event,
		Tool:   attentionText(r.Tool, activityToolMax),
		Target: attentionText(r.Target, activityTextMax),
		Text:   attentionText(firstLine(r.Text), activityTextMax),
	}
	if subagentEvent(r.Event) {
		// The entry says which kind of agent it was. Its id pairs a start
		// with its stop in the daemon and says nothing to a reader.
		// recordAgentActivity names it better once the daemon knows it.
		e.Text = attentionText(firstLine(r.AgentType), activityToolMax)
	}
	if r.OK != nil {
		ok := *r.OK
		e.OK = &ok
	}
	for _, f := range r.Files {
		f = attentionText(f, activityFileMax)
		if f == "" || slices.Contains(e.Files, f) {
			continue
		}
		if len(e.Files) == activityFilesMax {
			break
		}
		e.Files = append(e.Files, f)
	}
	return e
}

// recordAgentActivity keeps one hook event in the pane's ring and moves the
// reserved metadata keys it implies, and reports whether the ring kept it.
// state is the pane's state after the report the activity rode on, applied
// or not.
//
// A subagent's start or stop is kept only when it moved the pane's
// subagents. A stop for one the pane never saw start, a second start of one
// already running, a start past the cap and a start on a pane with no agent
// state are nothing, and an entry in the ring would say otherwise.
func (d *Daemon) recordAgentActivity(sess *Session, windowID string, r *AgentActivityReport, state AgentState) bool {
	e := activityEntryOf(r)
	m := activityMetaFor(e, r.Model, state)
	m.subagents = subagentChangeOf(r)
	if subagentEvent(r.Event) || r.Event == ActivitySubagentUpdate {
		got := sess.applyActivityMeta(windowID, m)
		if got.move == subagentNoMove && !got.ended {
			return false
		}
		// The ring keeps a subagent's start and its end, named, and not its
		// tool calls. An update that ended it, a failed call, is its end.
		if r.Event == ActivitySubagentUpdate && !got.ended {
			return true
		}
		e = subagentEntry(got)
		d.activity.add(sess.ID, sess.Name(), windowID, e, true)
		d.dropRingOfClosedWindow(sess, windowID)
		return true
	}
	d.activity.add(sess.ID, sess.Name(), windowID, e, true)
	d.dropRingOfClosedWindow(sess, windowID)
	sess.applyActivityMeta(windowID, m)
	return true
}

// subagentEntry is the ring entry of a subagent's start, or of its end when
// the change ended it.
func subagentEntry(got subagentOutcome) AgentActivityEntry {
	sa := got.sa
	if !got.ended {
		return AgentActivityEntry{Kind: ActivitySubagentStart, Text: subagentLabel(sa)}
	}
	e := AgentActivityEntry{Kind: ActivitySubagentStop, Text: subagentLabel(sa), Outcome: sa.state, Tools: sa.tools}
	if sa.endedAt > sa.startedAt {
		e.DurationMS = (sa.endedAt - sa.startedAt) / int64(time.Millisecond)
	}
	return e
}

// dropRingOfClosedWindow forgets the pane's ring when the window is gone. The
// window id was resolved before the report applied, and a window that closed
// since has already had its ring forgotten by the session event sink, so an
// add after that would leave a ring nothing ever drops. The window leaves the
// state and its close event reaches the sink under one hold of the state
// lock, so either the sink forgets the ring after this check or this check
// sees the window gone.
func (d *Daemon) dropRingOfClosedWindow(sess *Session, windowID string) {
	if !sess.holdsWindowID(windowID) {
		d.activity.forgetWindow(sess.ID, windowID)
	}
}

// activityMeta is what one activity entry does to the reserved metadata keys:
// a value to set, or nil to clear, per key it touches.
type activityMeta struct {
	keys   []string
	values []*string
	// model is a model the harness named, empty when it named none.
	model string
	// subagents is what the entry does to the subagents the pane's agent is
	// running, which the subagents key counts.
	subagents subagentChange
}

// activityMetaFor decides the metadata an entry implies. A tool call starting
// sets now; a prompt sets prompt and clears now; a failure or the end of a
// turn clears now; and a pane at rest has nothing to be doing, so now is
// cleared whatever the entry is.
func activityMetaFor(e AgentActivityEntry, model string, state AgentState) activityMeta {
	var m activityMeta
	set := func(key string, v *string) {
		m.keys = append(m.keys, key)
		m.values = append(m.values, v)
	}
	switch e.Kind {
	case ActivityTool:
		if agentStateBusy(state) {
			now := e.Tool
			if e.Target != "" {
				now += ": " + e.Target
			}
			if v, _ := CleanAgentMetaValue(now); v != "" {
				set(AgentMetaNow, &v)
			}
		}
	case ActivityPrompt:
		if v, _ := CleanAgentMetaValue(e.Text); v != "" {
			set(AgentMetaPrompt, &v)
		}
		set(AgentMetaNow, nil)
	case ActivityToolFailed, ActivityTurnEnd:
		set(AgentMetaNow, nil)
	}
	if !agentStateBusy(state) && !slices.Contains(m.keys, AgentMetaNow) {
		set(AgentMetaNow, nil)
	}
	if v, _ := CleanAgentMetaValue(model); v != "" {
		m.model = v
	}
	return m
}

// agentStateBusy reports whether state is one in which the agent is doing
// something, so the reserved now key may stand.
func agentStateBusy(state AgentState) bool {
	return state == AgentStateWorking || state == AgentStateNeedsInput
}

// clearNowAtRestLocked drops the reserved now key from every window whose
// agent state moved, in this mutation, to a state other than working or
// needs_input. It runs inside every daemon-side mutation, so now is cleared
// however the pane came to rest: a report with no activity, a screen rule, an
// OSC sequence, the detector or the silence timer. A new slice is built rather
// than the old one edited, since a published snapshot may share it. The caller
// holds stateMu.
func clearNowAtRestLocked(before lifecycleSnapshot, st *SessionState) {
	for i := range st.Windows {
		w := &st.Windows[i]
		if agentStateBusy(w.AgentState) || len(w.AgentMeta) == 0 {
			continue
		}
		idx, ok := before.index[w.ID]
		if !ok || before.windows[idx].agentState == w.AgentState {
			continue
		}
		if !slices.ContainsFunc(w.AgentMeta, func(t AgentMetaToken) bool { return t.Key == AgentMetaNow }) {
			continue
		}
		kept := make([]AgentMetaToken, 0, len(w.AgentMeta)-1)
		for _, t := range w.AgentMeta {
			if t.Key != AgentMetaNow {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			kept = nil
		}
		w.AgentMeta = kept
	}
}

// applyActivityMeta writes what activityMetaFor decided. A key already
// holding the value, a key already absent, and a model the pane already shows
// from any source change nothing, so a hook firing on every tool call pushes
// state only when what the rail draws moves.
//
// The pane's subagents move in the same mutation as the count and the list
// the window carries and the key that says the count, under the state lock,
// so hooks for subagents started together, which a harness runs at the same
// moment, cannot leave a count the set no longer has. A change to the tool a
// subagent runs alone, within subagentPublishGap of the last subagent change
// shown, is held for subagentFlush. It reports what the subagents did.
func (s *Session) applyActivityMeta(windowID string, m activityMeta) subagentOutcome {
	var got subagentOutcome
	if len(m.keys) == 0 && m.model == "" && m.subagents.op == "" && !m.subagents.spawn {
		return got
	}
	var expiry, flushAt int64
	_ = s.mutateState(func(st *SessionState) error {
		idx, err := findWindowStateIndex(st.Windows, windowID)
		if err != nil {
			return err
		}
		w := &st.Windows[idx]
		now := time.Now().UnixNano()
		cur := liveAgentMeta(w.AgentMeta, now)
		u := AgentMetaUpdate{Source: agentMetaActivitySource}
		for i, key := range m.keys {
			u.Keys = append(u.Keys, key)
			u.Values = append(u.Values, m.values[i])
		}
		got = s.moveSubagentsLocked(w, m.subagents, now)
		if m.subagents.op != "" {
			expiry = s.subagentExpiryLocked()
		}
		show := got.move == subagentListMove
		if got.move == subagentToolMove {
			if gap := int64(subagentPublishGap); now-s.subagentShownAt < gap {
				s.agentSubagents[w.ID].held = true
				flushAt = s.subagentShownAt + gap
			} else {
				show = true
			}
		}
		next, changed, err := applyAgentMeta(cur, u, now)
		if err != nil {
			return err
		}
		if m.model != "" && agentMetaValue(next, AgentMetaModel) != m.model {
			next, _, err = applyAgentMeta(next, AgentMetaUpdate{
				Keys: []string{AgentMetaModel}, Values: []*string{&m.model}, Source: agentMetaHookSource,
			}, now)
			if err != nil {
				return err
			}
			changed = true
		}
		if show {
			var shown bool
			if next, shown = s.showSubagentsLocked(w, next, now); shown {
				s.subagentShownAt = now
				changed = true
			}
		}
		if !changed && len(cur) == len(w.AgentMeta) {
			return errNoAgentMetaChange
		}
		w.AgentMeta = next
		return nil
	})
	s.armSubagentPrune(expiry)
	s.subagentFlush.arm(flushAt, s.flushHeldSubagents)
	return got
}

// agentMetaValue is the value tokens hold for key, or "".
func agentMetaValue(tokens []AgentMetaToken, key string) string {
	for _, t := range tokens {
		if t.Key == key {
			return t.Value
		}
	}
	return ""
}

// agentActivityParams are what agent-activity takes.
type agentActivityParams struct {
	Session  string `json:"session"`
	Window   string `json:"window"`
	Since    int64  `json:"since"`
	SinceSeq uint64 `json:"since_seq"`
	Limit    int    `json:"limit"`
	Recap    bool   `json:"recap"`
}

// AgentActivityRecap summarises a pane's activity since a time.
type AgentActivityRecap struct {
	// Since is when the recap starts: the time asked for, or the oldest entry
	// the ring still holds when it has dropped entries newer than that.
	Since int64 `json:"since"`
	// Turns is how many turns the pane finished: its completion_seq delta.
	Turns uint64 `json:"turns"`
	// Files are the files tool calls wrote, first written first, at most
	// recapFilesShown of them. FilesTotal counts them all.
	Files      []string `json:"files"`
	FilesTotal int      `json:"files_total"`
	// Commands counts shell tool calls and the shell's own commands.
	Commands int `json:"commands"`
	// Tests is the newest command that reads as a test run, nil when none.
	Tests *AgentActivityTest `json:"tests,omitempty"`
	// LastSaid is the first line the newest turn ended with.
	LastSaid string `json:"last_said,omitempty"`
	// State is the pane's agent state now.
	State string `json:"state"`
}

// AgentActivityTest is the newest test run a recap found.
type AgentActivityTest struct {
	Cmdline string `json:"cmdline"`
	// OK is whether it passed, null when nothing said: the tool call has not
	// finished, or the harness and the shell reported no result.
	OK *bool `json:"ok"`
	At int64 `json:"at"`
}

// shellTools are the tool names harnesses give a shell command: Claude Code
// and Codex say Bash, Codex's older builds shell, exec_command and
// local_shell, and Gemini CLI run_shell_command.
var shellTools = []string{"Bash", "bash", "shell", "exec_command", "local_shell", "run_shell_command"}

// agentActivityRecap summarises entries, which are the pane's entries since
// since, oldest first. patterns are the commands that read as a test run.
func agentActivityRecap(entries []AgentActivityEntry, since int64, state AgentState, patterns []string) AgentActivityRecap {
	rc := AgentActivityRecap{Since: since, Files: []string{}, State: state.Name()}
	seen := map[string]bool{}
	for _, e := range entries {
		switch e.Kind {
		case ActivityState:
			rc.Turns += e.turns
		case ActivityTool:
			if slices.Contains(shellTools, e.Tool) {
				rc.Commands++
			}
		case ActivityCommand:
			rc.Commands++
		case ActivityToolDone:
			for _, f := range e.Files {
				if seen[f] {
					continue
				}
				seen[f] = true
				rc.FilesTotal++
				if len(rc.Files) < recapFilesShown {
					rc.Files = append(rc.Files, f)
				}
			}
		}
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if e := entries[i]; e.Kind == ActivityTurnEnd && e.Text != "" {
			rc.LastSaid = e.Text
			break
		}
	}
	rc.Tests = newestTestRun(entries, patterns)
	return rc
}

// newestTestRun finds the newest command that matches a test pattern and
// says how it went: a finished tool call by its result (a failure event is a
// failure, a finished call is a pass unless it said otherwise), a shell
// command by its exit status. A call that started and has not finished, and a
// command that sent no status, pass nothing.
func newestTestRun(entries []AgentActivityEntry, patterns []string) *AgentActivityTest {
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		isShell := e.Kind == ActivityCommand ||
			(slices.Contains(shellTools, e.Tool) && (e.Kind == ActivityTool || e.Kind == ActivityToolDone || e.Kind == ActivityToolFailed))
		if !isShell || !matchesTestPattern(e.Target, patterns) {
			continue
		}
		t := &AgentActivityTest{Cmdline: e.Target, At: e.At}
		switch e.Kind {
		case ActivityToolDone:
			ok := true
			if e.OK != nil {
				ok = *e.OK
			}
			t.OK = &ok
		case ActivityToolFailed:
			ok := false
			t.OK = &ok
		case ActivityCommand:
			if e.Exit != nil {
				ok := *e.Exit == 0
				t.OK = &ok
			}
		}
		return t
	}
	return nil
}

// matchesTestPattern reports whether a command line contains one of patterns.
func matchesTestPattern(cmdline string, patterns []string) bool {
	if cmdline == "" {
		return false
	}
	for _, p := range patterns {
		if p = strings.TrimSpace(p); p != "" && strings.Contains(cmdline, p) {
			return true
		}
	}
	return false
}

// SetRecapTestPatterns sets the commands a recap reads as a test run, from
// [agents.recap] test_patterns. Nil or empty means the defaults.
func (d *Daemon) SetRecapTestPatterns(patterns []string) {
	if len(patterns) == 0 {
		patterns = config.DefaultRecapTestPatterns
	}
	p := slices.Clone(patterns)
	d.recapTests.Store(&p)
}

// recapTestPatterns is what SetRecapTestPatterns set, else the defaults.
func (d *Daemon) recapTestPatterns() []string {
	if p := d.recapTests.Load(); p != nil {
		return *p
	}
	return config.DefaultRecapTestPatterns
}

// verbAgentActivity answers agent-activity: the pane's entries after since
// and since_seq, the newest limit of them, and with recap a summary of every
// one of them.
func (d *Daemon) verbAgentActivity(_ *connState, params json.RawMessage) (any, *verbError) {
	var p agentActivityParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Limit < 0 || p.Limit > agentActivityMaxLimit {
		return nil, invalidParam("limit", "limit is 1 to 256 entries, or 0 for the default")
	}
	if p.Since < 0 {
		return nil, invalidParam("since", "since is a time in unix nanoseconds, or 0 for the whole ring")
	}
	limit := p.Limit
	if limit == 0 {
		limit = agentActivityDefaultLimit
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	st := sess.GetState()
	target := p.Window
	if target == "" {
		id, err := focusedWindowID(st)
		if err != nil {
			return nil, mapResolveErr(err, sess)
		}
		target = id
	}
	idx, err := findWindowStateIndex(st.Windows, target)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	w := st.Windows[idx]

	all, _, dropped := d.activity.read(sess.ID, w.ID)
	var lastSeq uint64
	if n := len(all); n > 0 {
		lastSeq = all[n-1].Seq
	}
	matched := make([]AgentActivityEntry, 0, len(all))
	for _, e := range all {
		if e.Seq > p.SinceSeq && e.At > p.Since {
			matched = append(matched, e)
		}
	}
	entries := matched
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	out := map[string]any{
		"type":      "agent_activity",
		"session":   sess.Name(),
		"window":    w.ID,
		"entries":   entries,
		"last_seq":  lastSeq,
		"untrusted": true,
	}
	if p.Recap {
		// A recap of the whole ring starts at its oldest entry, and so does
		// one asked from before entries the ring has since dropped.
		since := p.Since
		if len(all) > 0 && all[0].At > since && (since == 0 || dropped) {
			since = all[0].At
		}
		out["recap"] = agentActivityRecap(matched, since, w.AgentState, d.recapTestPatterns())
	}
	return out, nil
}
