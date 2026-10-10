package session

import (
	"cmp"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The subagents an agent pane is running. A harness that hands work to
// subagents, or to the teammates of an agent team, and then ends its turn
// reports done, and its pane comes to rest, while that work goes on. So the
// daemon keeps, per pane, the subagents the pane's own hooks reported: each
// one's type and description, the tool it runs now, how many tools it ran,
// and once it stopped, how it ended. The window carries the running count
// (AgentSubagents) and the list (AgentSubagentList), which the rail draws, and
// the reserved metadata key subagents says the count in words for
// get-agent-state and list-agents.
//
// It is display only, like the rest of the pane's metadata: nothing reads it
// to decide a state, a wait or an alert, and a subagent never changes the
// pane's state, its message or now. The reports come with
// report-agent-activity, which passes the identity guard first, so a nested
// run's subagents are not the pane's.
//
// Pairing. Claude Code's SubagentStart names the subagent's id and type and
// nothing else. The description is on the PreToolUse of the Agent tool that
// launched it, which comes first, so that call leaves a spawn hint, and a
// start takes the oldest hint of its type (else the oldest hint). Two
// subagents of one type launched together can take each other's description;
// the PostToolUse of a background launch and the background_tasks list of
// every Stop name the right one by id and correct it. A foreground subagent
// is corrected when its call completes.
//
// End states. A stop with a final message is done. A stop with none, or one
// the main agent asked for (TaskStop), is stopped. A failed Agent call is
// failed. A finished subagent stays on the list, with its end state, for
// subagentFade (done and stopped) or five times that (failed), and then goes.
//
// The set is forgotten when the agent starts a conversation (the session_start
// activity of a new, resumed or cleared session), when the pane's state goes
// to none (the agent ended its session or left the pane), and when the window
// closes. A running subagent the pane hears nothing more of for subagentQuiet
// is dropped as well: a stop that never came, after an interrupt, would
// otherwise leave a count on the row for as long as the agent runs. It is
// daemon memory only, so a daemon restart, which ends every process in every
// pane, starts it empty.

// Bounds of a pane's subagents.
const (
	// subagentsMax bounds the running subagents one pane keeps. A harness
	// runs a handful at once, and an agent team's lead a few teammates; the
	// cap only stops a caller growing a map without end. A start past it is
	// not kept, so the count stays at the cap until one stops.
	subagentsMax = 64
	// subagentsFinishedMax is how many finished subagents a pane keeps on
	// its list while they fade. An older one goes for a newer one.
	subagentsFinishedMax = 8
	// subagentListMax bounds the list a window carries to its clients.
	subagentListMax = 16
	// subagentTextMax bounds a description and a result on the wire, in
	// bytes.
	subagentTextMax = 80
	// subagentToolMax bounds now and last, "Tool: target", in bytes.
	subagentToolMax = 80
	// subagentSpawnsMax bounds the spawn hints a pane holds, and
	// subagentSpawnTTL is how long one waits for its subagent to start.
	subagentSpawnsMax = 8
	subagentSpawnTTL  = 30 * time.Second
	// subagentPublishGap is the least time between two pushes of a
	// session's state for a subagent's tool calls alone. A change in between
	// is held and pushed by a trailing timer, so subagents busy with tools
	// do not push state on every call.
	subagentPublishGap = 250 * time.Millisecond
)

// subagentQuiet is how long a running subagent stays counted after the last
// event the pane reported for it. An hour is past what a subagent or a
// teammate's stretch of work takes without a tool call, and short enough that
// a missed stop does not outlive the afternoon.
const subagentQuiet = time.Hour

// defaultSubagentFade is how long a finished subagent that was done or
// stopped stays on the list. A failed one stays five times as long.
const defaultSubagentFade = 60 * time.Second

// subagentFade is defaultSubagentFade, or TUIOS_SUBAGENT_FADE_SECONDS when it
// is a positive number, which a test uses to see the fade in seconds.
var subagentFade = sync.OnceValue(func() time.Duration {
	if s := strings.TrimSpace(os.Getenv("TUIOS_SUBAGENT_FADE_SECONDS")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return defaultSubagentFade
})

// Subagent states, the values of SubagentInfo.State.
const (
	SubagentRunning = "running"
	SubagentDone    = "done"
	SubagentFailed  = "failed"
	SubagentStopped = "stopped"
)

// Subagent outcomes a subagent_update may report.
const (
	subagentOutcomeStopping = "stopping"
	subagentOutcomeFailed   = "failed"
)

// SubagentInfo is one subagent of a pane, as the window carries it
// (WindowState.AgentSubagentList) and list-agents and get-agent-state return
// it (subagent_list). Every string is the agent's, cleaned to one line with
// likely secrets masked. Daemon-owned and never set by a client.
type SubagentInfo struct {
	// ID is the harness's id for the subagent.
	ID string `json:"id"`
	// Type is the subagent's type, such as general-purpose or Explore.
	Type string `json:"type,omitempty"`
	// Description is what the subagent was asked to do, as the call that
	// launched it described it. Empty when no launch named one.
	Description string `json:"description,omitempty"`
	// State is running, done, failed or stopped.
	State string `json:"state"`
	// StartedAt and EndedAt are unix nanoseconds. EndedAt is 0 while it
	// runs.
	StartedAt int64 `json:"started_at"`
	EndedAt   int64 `json:"ended_at,omitempty"`
	// Now is the tool it runs, "Bash: go test ./...", empty between calls
	// and once it ended. Last is the tool it ran last.
	Now  string `json:"now,omitempty"`
	Last string `json:"last,omitempty"`
	// Tools is how many tool calls it made.
	Tools int `json:"tools,omitempty"`
	// Result is the first line of what it said last, or for a failed one
	// the error.
	Result string `json:"result,omitempty"`
}

// subagent is one subagent a pane is running or ran.
type subagent struct {
	id          string
	agentType   string
	description string
	callID      string
	// seq is the subagent's place in the pane's start order.
	seq uint64
	// startedAt, seen and endedAt are unix nanoseconds. seen is when the
	// pane last reported it; endedAt is 0 while it runs.
	startedAt, seen, endedAt int64
	// tool and target are the call it runs, tool empty between calls.
	tool, target string
	// last is the call it ran last, "Tool: target".
	last  string
	tools int
	// stopping says the main agent asked to stop it.
	stopping bool
	// state is one of the Subagent* states.
	state  string
	result string
}

// spawnHint is a launch the pane's agent made whose subagent has not started.
type spawnHint struct {
	agentType   string
	description string
	callID      string
	at          int64
}

// paneSubagents is one pane's subagents and spawn hints.
type paneSubagents struct {
	byID    map[string]*subagent
	spawns  []spawnHint
	nextSeq uint64
	// held says a tool change is not on the window yet: see
	// subagentPublishGap.
	held bool
}

// running is how many of the pane's subagents have not ended.
func (ps *paneSubagents) running() int {
	if ps == nil {
		return 0
	}
	n := 0
	for _, sa := range ps.byID {
		if sa.endedAt == 0 {
			n++
		}
	}
	return n
}

// empty reports whether the pane holds nothing worth keeping.
func (ps *paneSubagents) empty() bool {
	return ps == nil || (len(ps.byID) == 0 && len(ps.spawns) == 0)
}

// subagentEvent reports whether an activity event is a subagent's start or
// stop, which the ring keeps.
func subagentEvent(event string) bool {
	return event == ActivitySubagentStart || event == ActivitySubagentStop
}

// subagentChange is what one activity does to a pane's subagents. The zero
// value does nothing.
type subagentChange struct {
	// op is ActivitySubagentStart, ActivitySubagentStop,
	// ActivitySubagentUpdate or ActivitySessionStart, or empty for nothing.
	op        string
	id        string
	agentType string
	// text is a stop's final message, an update's description, or with
	// outcome failed the error.
	text   string
	callID string
	// tool and target are an update's tool call; ok is nil while it starts
	// and says how it ended after.
	tool, target string
	ok           *bool
	outcome      string
	tools        int
	// spawn is a launch hint: agentType, the description in spawnText and
	// callID name a subagent about to start. It rides a tool event or an
	// update.
	spawn     bool
	spawnText string
}

// subagentChangeOf is the change an activity report makes.
func subagentChangeOf(r *AgentActivityReport) subagentChange {
	clean := func(s string) string { return attentionText(firstLine(s), subagentTextMax) }
	c := subagentChange{}
	switch r.Event {
	case ActivitySubagentStart, ActivitySubagentStop, ActivitySubagentUpdate:
		c = subagentChange{
			op:        r.Event,
			id:        r.AgentID,
			agentType: attentionText(firstLine(r.AgentType), activityToolMax),
			text:      clean(r.Text),
			callID:    r.CallID,
			tool:      attentionText(r.Tool, activityToolMax),
			target:    attentionText(r.Target, activityTextMax),
			ok:        r.OK,
			outcome:   r.Outcome,
			tools:     r.Tools,
		}
	case ActivitySessionStart:
		return subagentChange{op: r.Event}
	case ActivityTool:
		if !r.Spawn {
			return c
		}
	default:
		return c
	}
	if r.Spawn {
		c.spawn = true
		c.agentType = attentionText(firstLine(r.AgentType), activityToolMax)
		c.spawnText = clean(r.Target)
		c.callID = r.CallID
	}
	return c
}

// subagentMove is what a change did to a pane's subagents.
type subagentMove int

const (
	// subagentNoMove changed nothing a client draws.
	subagentNoMove subagentMove = iota
	// subagentToolMove changed only the tool a subagent runs, which may be
	// held for subagentPublishGap.
	subagentToolMove
	// subagentListMove changed the count or a row, pushed at once.
	subagentListMove
)

// subagentOutcome is what applyActivityMeta reports about a subagent change:
// whether it moved anything, and the subagent it was about, for the ring.
type subagentOutcome struct {
	move subagentMove
	// ended says this change finished the subagent.
	ended bool
	sa    subagent
}

// paneSubagentsLocked is window id's subagents, made when create is true. The
// caller holds stateMu.
func (s *Session) paneSubagentsLocked(id string, create bool) *paneSubagents {
	ps := s.agentSubagents[id]
	if ps == nil && create {
		ps = &paneSubagents{byID: make(map[string]*subagent)}
		if s.agentSubagents == nil {
			s.agentSubagents = make(map[string]*paneSubagents)
		}
		s.agentSubagents[id] = ps
	}
	return ps
}

// takeSpawn removes and returns the spawn hint a subagent of agentType
// starting at now takes: the oldest of its type, else the oldest. Hints older
// than subagentSpawnTTL are dropped first.
func (ps *paneSubagents) takeSpawn(agentType string, now int64) (spawnHint, bool) {
	ps.spawns = slices.DeleteFunc(ps.spawns, func(h spawnHint) bool { return now-h.at > int64(subagentSpawnTTL) })
	if len(ps.spawns) == 0 {
		return spawnHint{}, false
	}
	pick := 0
	for i, h := range ps.spawns {
		if h.agentType == agentType {
			pick = i
			break
		}
	}
	h := ps.spawns[pick]
	ps.spawns = slices.Delete(ps.spawns, pick, pick+1)
	return h, true
}

// find is the subagent an update names: by id, else by the call that
// launched it.
func (ps *paneSubagents) find(id, callID string) *subagent {
	if ps == nil {
		return nil
	}
	if id != "" {
		return ps.byID[id]
	}
	if callID == "" {
		return nil
	}
	for _, sa := range ps.byID {
		if sa.callID == callID {
			return sa
		}
	}
	return nil
}

// finish ends sa as state at now and drops the oldest finished subagents past
// subagentsFinishedMax.
func (ps *paneSubagents) finish(sa *subagent, state, result string, now int64) {
	sa.state, sa.endedAt, sa.seen = state, now, now
	if result != "" {
		sa.result = result
	}
	if sa.tool != "" {
		sa.last = toolLine(sa.tool, sa.target)
	}
	sa.tool, sa.target = "", ""
	var done []*subagent
	for _, o := range ps.byID {
		if o.endedAt != 0 {
			done = append(done, o)
		}
	}
	if len(done) <= subagentsFinishedMax {
		return
	}
	slices.SortFunc(done, func(a, b *subagent) int { return cmp.Compare(a.endedAt, b.endedAt) })
	for _, o := range done[:len(done)-subagentsFinishedMax] {
		delete(ps.byID, o.id)
	}
}

// toolLine is a tool call as now and last say it: "Bash: go test ./...".
func toolLine(tool, target string) string {
	if tool == "" {
		return ""
	}
	line := tool
	if target != "" {
		line += ": " + target
	}
	return attentionText(line, subagentToolMax)
}

// moveSubagentsLocked applies c, reported at now, to window w's subagents.
// A start of one already running renews when it was last seen and moves
// nothing. A start past subagentsMax, a stop or an update of one it does not
// know, and a start on a pane with no agent state change nothing. The caller
// holds stateMu.
func (s *Session) moveSubagentsLocked(w *WindowState, c subagentChange, now int64) subagentOutcome {
	var out subagentOutcome
	if c.spawn && w.AgentState != AgentStateNone {
		ps := s.paneSubagentsLocked(w.ID, true)
		if len(ps.spawns) >= subagentSpawnsMax {
			ps.spawns = slices.Delete(ps.spawns, 0, 1)
		}
		ps.spawns = append(ps.spawns, spawnHint{agentType: c.agentType, description: c.spawnText, callID: c.callID, at: now})
	}
	switch c.op {
	case ActivitySubagentStart:
		ps := s.agentSubagents[w.ID]
		if sa := ps.find(c.id, ""); sa != nil {
			if sa.endedAt == 0 {
				sa.seen = now
				out.sa = *sa
				return out
			}
			// A teammate that went idle wakes to work again under its id.
			delete(ps.byID, c.id)
		}
		if ps.running() >= subagentsMax || w.AgentState == AgentStateNone {
			return out
		}
		ps = s.paneSubagentsLocked(w.ID, true)
		ps.nextSeq++
		sa := &subagent{id: c.id, agentType: c.agentType, seq: ps.nextSeq, startedAt: now, seen: now, state: SubagentRunning}
		if h, ok := ps.takeSpawn(c.agentType, now); ok {
			sa.description, sa.callID = h.description, h.callID
			if sa.agentType == "" {
				sa.agentType = h.agentType
			}
		}
		ps.byID[c.id] = sa
		out.move, out.sa = subagentListMove, *sa
	case ActivitySubagentStop:
		ps := s.agentSubagents[w.ID]
		sa := ps.find(c.id, "")
		if sa == nil || sa.endedAt != 0 {
			return out
		}
		state := SubagentDone
		if sa.stopping || c.text == "" {
			state = SubagentStopped
		}
		ps.finish(sa, state, c.text, now)
		out.move, out.ended, out.sa = subagentListMove, true, *sa
	case ActivitySubagentUpdate:
		out = s.updateSubagentLocked(w, c, now)
	case ActivitySessionStart:
		// The window's count is checked as well as the set, so a count the
		// set lost some other way is not left on the row.
		ps := s.agentSubagents[w.ID]
		delete(s.agentSubagents, w.ID)
		if ps.empty() && w.AgentSubagents == 0 && len(w.AgentSubagentList) == 0 {
			return out
		}
		if len(ps.byID) > 0 || w.AgentSubagents != 0 || len(w.AgentSubagentList) != 0 {
			out.move = subagentListMove
		}
	}
	return out
}

// updateSubagentLocked applies a subagent_update. The caller holds stateMu.
func (s *Session) updateSubagentLocked(w *WindowState, c subagentChange, now int64) subagentOutcome {
	var out subagentOutcome
	ps := s.agentSubagents[w.ID]
	sa := ps.find(c.id, c.callID)
	if sa == nil {
		return out
	}
	sa.seen = now
	if sa.callID == "" && c.callID != "" && !c.spawn {
		sa.callID = c.callID
	}
	switch c.outcome {
	case subagentOutcomeStopping:
		sa.stopping = true
	case subagentOutcomeFailed:
		if sa.state != SubagentFailed {
			ended := sa.endedAt == 0
			if ended {
				ps.finish(sa, SubagentFailed, c.text, now)
			} else {
				sa.state = SubagentFailed
				if c.text != "" {
					sa.result = c.text
				}
			}
			out.move, out.ended = subagentListMove, ended
		}
		out.sa = *sa
		return out
	}
	if c.text != "" && c.text != sa.description {
		sa.description = c.text
		out.move = subagentListMove
	}
	if c.agentType != "" && sa.agentType == "" && !c.spawn {
		sa.agentType = c.agentType
		out.move = subagentListMove
	}
	if c.tools > 0 && c.tools != sa.tools {
		sa.tools = c.tools
		out.move = subagentListMove
	}
	if c.tool != "" && sa.endedAt == 0 {
		moved := false
		if c.ok == nil {
			sa.tool, sa.target = c.tool, c.target
			sa.tools++
			moved = true
		} else {
			sa.last = toolLine(c.tool, c.target)
			if sa.tool == c.tool && sa.target == c.target {
				sa.tool, sa.target = "", ""
			}
			moved = true
		}
		if moved && out.move == subagentNoMove {
			out.move = subagentToolMove
		}
	}
	out.sa = *sa
	return out
}

// info is sa as the window carries it.
func (sa *subagent) info() SubagentInfo {
	return SubagentInfo{
		ID:          sa.id,
		Type:        sa.agentType,
		Description: sa.description,
		State:       sa.state,
		StartedAt:   sa.startedAt,
		EndedAt:     sa.endedAt,
		Now:         toolLine(sa.tool, sa.target),
		Last:        sa.last,
		Tools:       sa.tools,
		Result:      sa.result,
	}
}

// subagentListLocked is window id's subagents as the window carries them, in
// start order, at most subagentListMax: finished ones go first, the longest
// finished first, then the newest running. Nil when there are none. The
// caller holds stateMu.
func (s *Session) subagentListLocked(id string) []SubagentInfo {
	ps := s.agentSubagents[id]
	if ps == nil || len(ps.byID) == 0 {
		return nil
	}
	all := slices.SortedFunc(maps.Values(ps.byID), func(a, b *subagent) int { return cmp.Compare(a.seq, b.seq) })
	for len(all) > subagentListMax {
		drop := -1
		for i, sa := range all {
			if sa.endedAt != 0 && (drop < 0 || sa.endedAt < all[drop].endedAt) {
				drop = i
			}
		}
		if drop < 0 {
			all = all[:subagentListMax]
			break
		}
		all = slices.Delete(all, drop, drop+1)
	}
	out := make([]SubagentInfo, len(all))
	for i, sa := range all {
		out[i] = sa.info()
	}
	return out
}

// showSubagentsLocked puts window w's subagents on the window: the running
// count and the list, and the subagents key in tokens, which it returns. It
// reports whether any of them changed. The caller holds stateMu and writes
// the tokens to w.AgentMeta.
func (s *Session) showSubagentsLocked(w *WindowState, tokens []AgentMetaToken, now int64) ([]AgentMetaToken, bool) {
	ps := s.agentSubagents[w.ID]
	if ps != nil {
		ps.held = false
	}
	n := ps.running()
	list := s.subagentListLocked(w.ID)
	if n == w.AgentSubagents && slices.Equal(list, w.AgentSubagentList) {
		return tokens, false
	}
	if n != w.AgentSubagents || agentMetaValue(tokens, AgentMetaSubagents) != SubagentsText(n) {
		tokens = withSubagentsKey(tokens, n, now)
	}
	w.AgentSubagents, w.AgentSubagentList = n, list
	return tokens, true
}

// SubagentsText is how n subagents read on the rail and in the subagents
// key: "1 subagent" or "n subagents", and "" at zero.
func SubagentsText(n int) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return "1 subagent"
	}
	return strconv.Itoa(n) + " subagents"
}

// subagentsMetaValue is the subagents key for n subagents, and nil to remove
// the key at zero.
func subagentsMetaValue(n int) *string {
	if n <= 0 {
		return nil
	}
	v := SubagentsText(n)
	return &v
}

// withSubagentsKey is tokens with the subagents key saying n, or without it at
// zero. A pane that already holds as many keys as it may keeps its tokens as
// they are: the count the window carries still moves, and the rail draws that.
func withSubagentsKey(tokens []AgentMetaToken, n int, now int64) []AgentMetaToken {
	next, _, err := applyAgentMeta(tokens, AgentMetaUpdate{
		Keys: []string{AgentMetaSubagents}, Values: []*string{subagentsMetaValue(n)}, Source: agentMetaActivitySource,
	}, now)
	if err != nil {
		return tokens
	}
	return next
}

// forgetSubagentsLocked drops the subagents of every window that is gone or
// whose agent state is none, with the count, the list and the key that say
// them. It runs inside every daemon-side mutation and after every client
// push, so the set goes however the agent left: a SessionEnd reported as
// none, the detector seeing the agent leave, set-agent-state none, or the
// window closing. The caller holds stateMu.
func (s *Session) forgetSubagentsLocked(st *SessionState) {
	if len(s.agentSubagents) == 0 {
		return
	}
	maps.DeleteFunc(s.agentSubagents, func(id string, _ *paneSubagents) bool {
		// Few windows have subagents, so a scan for each costs less than a
		// map of them all.
		var w *WindowState
		for i := range st.Windows {
			if st.Windows[i].ID == id {
				w = &st.Windows[i]
				break
			}
		}
		if w != nil && w.AgentState != AgentStateNone {
			return false
		}
		if w != nil {
			w.AgentSubagents = 0
			w.AgentSubagentList = nil
			if agentMetaValue(w.AgentMeta, AgentMetaSubagents) != "" {
				// A new slice, since a published snapshot may share the old one.
				w.AgentMeta = slices.DeleteFunc(slices.Clone(w.AgentMeta), func(t AgentMetaToken) bool { return t.Key == AgentMetaSubagents })
				if len(w.AgentMeta) == 0 {
					w.AgentMeta = nil
				}
			}
		}
		return true
	})
}

// subagentDue is when sa next needs the prune: when it goes quiet while it
// runs, when it fades once it ended.
func subagentDue(sa *subagent) int64 {
	if sa.endedAt == 0 {
		return sa.seen + int64(subagentQuiet)
	}
	fade := subagentFade()
	if sa.state == SubagentFailed {
		fade *= 5
	}
	return sa.endedAt + int64(fade)
}

// subagentExpiryLocked is when the soonest of the session's subagents goes
// quiet or fades, in unix nanoseconds, or 0 when it has none. The caller
// holds stateMu.
func (s *Session) subagentExpiryLocked() int64 {
	var at int64
	for _, ps := range s.agentSubagents {
		for _, sa := range ps.byID {
			if due := subagentDue(sa); at == 0 || due < at {
				at = due
			}
		}
	}
	return at
}

// expireSubagents drops every running subagent the session has heard nothing
// of for subagentQuiet as of now, and every finished one whose fade is over,
// moves the windows that lost one, and arms the next prune. It reports how
// many it dropped. now is passed in so a test can stand at any time without
// waiting.
func (s *Session) expireSubagents(now time.Time) int {
	dropped := 0
	var next int64
	_ = s.mutateState(func(st *SessionState) error {
		at := now.UnixNano()
		for i := range st.Windows {
			w := &st.Windows[i]
			ps := s.agentSubagents[w.ID]
			if ps == nil {
				continue
			}
			gone := 0
			for id, sa := range ps.byID {
				if subagentDue(sa) <= at {
					delete(ps.byID, id)
					gone++
				}
			}
			if gone == 0 {
				continue
			}
			dropped += gone
			if ps.empty() {
				delete(s.agentSubagents, w.ID)
			}
			w.AgentMeta, _ = s.showSubagentsLocked(w, w.AgentMeta, at)
		}
		next = s.subagentExpiryLocked()
		if dropped == 0 {
			// Nothing a client draws moved, so the version stays and nothing
			// is pushed.
			return errNoAgentMetaChange
		}
		s.subagentShownAt = at
		return nil
	})
	s.armSubagentPrune(next)
	return dropped
}

// armSubagentPrune makes sure a prune runs by at, the way armAgentMetaPrune
// does for metadata with a TTL. A timer already due sooner stands. It is armed
// only while a pane has subagents, so a session without them costs nothing.
func (s *Session) armSubagentPrune(at int64) {
	s.subagentPrune.arm(at, func() { s.expireSubagents(time.Now()) })
}

// flushHeldSubagents puts on their windows the tool changes held back by
// subagentPublishGap, in one push.
func (s *Session) flushHeldSubagents() {
	_ = s.mutateState(func(st *SessionState) error {
		now := time.Now().UnixNano()
		changed := false
		for i := range st.Windows {
			w := &st.Windows[i]
			if ps := s.agentSubagents[w.ID]; ps != nil && ps.held {
				var shown bool
				if w.AgentMeta, shown = s.showSubagentsLocked(w, w.AgentMeta, now); shown {
					changed = true
				}
			}
		}
		if !changed {
			return errNoAgentMetaChange
		}
		s.subagentShownAt = now
		return nil
	})
}

// subagentCount is how many subagents the window's agent is running, as its
// hooks reported them.
func (s *Session) subagentCount(windowID string) int {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.agentSubagents[windowID].running()
}

// subagentLabel is how the ring and agent-log name a subagent: its
// description and type, "Research tmux (general-purpose)", or the one of the
// two it has.
func subagentLabel(sa subagent) string {
	switch {
	case sa.description != "" && sa.agentType != "":
		return sa.description + " (" + sa.agentType + ")"
	case sa.description != "":
		return sa.description
	}
	return sa.agentType
}

// subagentListOut is a window's list as list-agents and get-agent-state
// return it: an empty list rather than null when there are none.
func subagentListOut(list []SubagentInfo) []SubagentInfo {
	if list == nil {
		return []SubagentInfo{}
	}
	return list
}

// subagentListReturn documents subagent_list in list-verbs.
var subagentListReturn = verbParam{Name: "subagent_list", Type: "[]object", Description: "The pane's subagents in start order, at most 16, each kept for a minute after it ends (five for a failed one): id, type, description (what it was asked to do), state (running, done, failed or stopped), started_at and ended_at (Unix nanoseconds, ended_at 0 while it runs), now (the tool it runs, such as \"Bash: go test ./...\"), last (the tool it ran last), tools (how many tool calls it made) and result (the first line it ended with, or the error). The strings are the agent's, cut to one line with likely secrets masked. Empty while there are none."}
