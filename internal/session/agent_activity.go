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
	"github.com/Gaurav-Gosain/tuios/internal/winpath"
)

const (
	activityRingSize          = 256
	agentActivityMaxLimit     = activityRingSize
	agentActivityDefaultLimit = 64
	activityTextMax           = 160
	activityToolMax           = 64
	activityFileMax           = 256
	activityFilesMax          = 16
	recapFilesShown           = 20
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
	AgentMetaModel          = "model"
	agentMetaActivitySource = "activity"
	agentMetaHookSource     = "hook"
)

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

	turns uint64
}

type activityRing struct {
	entries []AgentActivityEntry
	start   int
	seq     uint64
}

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

func (r *activityRing) ordered() []AgentActivityEntry {
	out := make([]AgentActivityEntry, 0, len(r.entries))
	out = append(out, r.entries[r.start:]...)
	return append(out, r.entries[:r.start]...)
}

func (r *activityRing) dropped() bool {
	return r.seq > uint64(len(r.entries))
}

type activityKey struct {
	session string
	window  string
}

type activityStore struct {
	mu      sync.Mutex
	rings   map[activityKey]*activityRing
	publish func(streamEvent)
}

func newActivityStore(publish func(streamEvent)) *activityStore {
	return &activityStore{rings: make(map[activityKey]*activityRing), publish: publish}
}

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
		entry := e
		a.publish(streamEvent{Type: EventAgentActivity, Session: sessionName, Window: windowID, Entry: &entry})
	}
	return true
}

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

func (a *activityStore) has(sessionID, windowID string) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.rings[activityKey{sessionID, windowID}]
	return ok
}

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

func (a *activityStore) forgetWindow(sessionID, windowID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.rings, activityKey{sessionID, windowID})
}

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

var subagentOutcomes = []string{subagentOutcomeStopping, subagentOutcomeFailed}

const subagentToolsMax = 1_000_000

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

func activityEntryOf(r *AgentActivityReport) AgentActivityEntry {
	e := AgentActivityEntry{
		Kind:   r.Event,
		Tool:   attentionText(r.Tool, activityToolMax),
		Target: attentionText(r.Target, activityTextMax),
		Text:   attentionText(integration.FirstLine(r.Text), activityTextMax),
	}
	if subagentEvent(r.Event) {
		e.Text = attentionText(integration.FirstLine(r.AgentType), activityToolMax)
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

func (d *Daemon) recordAgentActivity(sess *Session, windowID string, r *AgentActivityReport, state AgentState) bool {
	e := activityEntryOf(r)
	m := activityMetaFor(e, r.Model, state)
	m.subagents = subagentChangeOf(r)
	if subagentEvent(r.Event) || r.Event == ActivitySubagentUpdate {
		got := sess.applyActivityMeta(windowID, m)
		if got.move == subagentNoMove && !got.ended {
			return false
		}
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

func (d *Daemon) dropRingOfClosedWindow(sess *Session, windowID string) {
	if !sess.holdsWindowID(windowID) {
		d.activity.forgetWindow(sess.ID, windowID)
	}
}

type activityMeta struct {
	keys      []string
	values    []*string
	model     string
	subagents subagentChange
}

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

func agentStateBusy(state AgentState) bool {
	return state == AgentStateWorking || state == AgentStateNeedsInput
}

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

func agentMetaValue(tokens []AgentMetaToken, key string) string {
	for _, t := range tokens {
		if t.Key == key {
			return t.Value
		}
	}
	return ""
}

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
	// Folder is the folder the pane's shell last reported, so a client can
	// name Files relative to it. Empty when the shell reported none.
	Folder string `json:"folder,omitempty"`
}

// AgentActivityTest is the newest test run a recap found.
type AgentActivityTest struct {
	Cmdline string `json:"cmdline"`
	// OK is whether it passed, null when nothing said: the tool call has not
	// finished, or the harness and the shell reported no result.
	OK *bool `json:"ok"`
	At int64 `json:"at"`
}

var shellTools = []string{"Bash", "bash", "shell", "exec_command", "local_shell", "run_shell_command"}

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
	for _, e := range slices.Backward(entries) {
		if e.Kind == ActivityTurnEnd && e.Text != "" {
			rc.LastSaid = e.Text
			break
		}
	}
	rc.Tests = newestTestRun(entries, patterns)
	return rc
}

func newestTestRun(entries []AgentActivityEntry, patterns []string) *AgentActivityTest {
	for _, e := range slices.Backward(entries) {
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

func (d *Daemon) recapTestPatterns() []string {
	if p := d.recapTests.Load(); p != nil {
		return *p
	}
	return config.DefaultRecapTestPatterns
}

func (d *Daemon) verbAgentActivity(cs *connState, params json.RawMessage) (any, *verbError) {
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
	sess, verr := d.resolveVerbSession(cs, p.Session)
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
		since := p.Since
		if len(all) > 0 && all[0].At > since && (since == 0 || dropped) {
			since = all[0].At
		}
		rc := agentActivityRecap(matched, since, w.AgentState, d.recapTestPatterns())
		rc.Folder = cwdPath(w.Cwd)
		out["recap"] = rc
	}
	return out, nil
}

func cwdPath(cwd string) string {
	if strings.HasPrefix(cwd, "file://") {
		_, p, _ := winpath.FileURL(cwd)
		return p
	}
	if strings.HasPrefix(cwd, "/") {
		return cwd
	}
	return ""
}
