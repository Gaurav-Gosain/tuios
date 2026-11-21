package session

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
)

const (
	subagentsMax         = 64
	subagentsFinishedMax = 8
	subagentListMax      = 16
	subagentTextMax      = 80
	subagentToolMax      = 80
	subagentSpawnsMax    = 8
	subagentSpawnTTL     = 30 * time.Second
	subagentPublishGap   = 250 * time.Millisecond
)

const subagentQuiet = time.Hour

const defaultSubagentFade = 60 * time.Second

// Subagent states, the values of SubagentInfo.State.
const (
	SubagentRunning = "running"
	SubagentDone    = "done"
	SubagentFailed  = "failed"
	SubagentStopped = "stopped"
)

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

type subagent struct {
	id                       string
	agentType                string
	description              string
	callID                   string
	seq                      uint64
	startedAt, seen, endedAt int64
	tool, target             string
	last                     string
	tools                    int
	stopping                 bool
	state                    string
	result                   string
}

type spawnHint struct {
	agentType   string
	description string
	callID      string
	at          int64
}

type paneSubagents struct {
	byID    map[string]*subagent
	spawns  []spawnHint
	nextSeq uint64
	held    bool
}

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

func (ps *paneSubagents) empty() bool {
	return ps == nil || (len(ps.byID) == 0 && len(ps.spawns) == 0)
}

func subagentEvent(event string) bool {
	return event == ActivitySubagentStart || event == ActivitySubagentStop
}

type subagentChange struct {
	op           string
	id           string
	agentType    string
	text         string
	callID       string
	tool, target string
	ok           *bool
	outcome      string
	tools        int
	spawn        bool
	spawnText    string
}

func subagentChangeOf(r *AgentActivityReport) subagentChange {
	clean := func(s string) string { return attentionText(integration.FirstLine(s), subagentTextMax) }
	c := subagentChange{}
	switch r.Event {
	case ActivitySubagentStart, ActivitySubagentStop, ActivitySubagentUpdate:
		c = subagentChange{
			op:        r.Event,
			id:        r.AgentID,
			agentType: attentionText(integration.FirstLine(r.AgentType), activityToolMax),
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
		c.agentType = attentionText(integration.FirstLine(r.AgentType), activityToolMax)
		c.spawnText = clean(r.Target)
		c.callID = r.CallID
	}
	return c
}

type subagentMove int

const (
	subagentNoMove subagentMove = iota
	subagentToolMove
	subagentListMove
)

type subagentOutcome struct {
	move  subagentMove
	ended bool
	sa    subagent
}

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
		if sa.description == "" && c.text != "" {
			sa.description = c.text
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
	if c.tool == "" && c.target == "" && c.text == "" && c.outcome == "" && c.ok == nil &&
		c.callID == "" && c.tools == 0 && !c.spawn && sa.endedAt == 0 {
		if sa.tool != "" {
			sa.last = toolLine(sa.tool, sa.target)
			sa.tool, sa.target = "", ""
			out.move = subagentToolMove
		}
		out.sa = *sa
		return out
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

func subagentsMetaValue(n int) *string {
	if n <= 0 {
		return nil
	}
	v := SubagentsText(n)
	return &v
}

func withSubagentsKey(tokens []AgentMetaToken, n int, now int64) []AgentMetaToken {
	next, _, err := applyAgentMeta(tokens, AgentMetaUpdate{
		Keys: []string{AgentMetaSubagents}, Values: []*string{subagentsMetaValue(n)}, Source: agentMetaActivitySource,
	}, now)
	if err != nil {
		return tokens
	}
	return next
}

func (s *Session) forgetSubagentsLocked(st *SessionState) {
	if len(s.agentSubagents) == 0 {
		return
	}
	maps.DeleteFunc(s.agentSubagents, func(id string, _ *paneSubagents) bool {
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
				w.AgentMeta = slices.DeleteFunc(slices.Clone(w.AgentMeta), func(t AgentMetaToken) bool { return t.Key == AgentMetaSubagents })
				if len(w.AgentMeta) == 0 {
					w.AgentMeta = nil
				}
			}
		}
		return true
	})
}

func (s *Session) subagentDue(sa *subagent) int64 {
	if sa.endedAt == 0 {
		return sa.seen + int64(subagentQuiet)
	}
	fade := defaultSubagentFade
	if s.config != nil && s.config.SubagentFade > 0 {
		fade = s.config.SubagentFade
	}
	if sa.state == SubagentFailed {
		fade *= 5
	}
	return sa.endedAt + int64(fade)
}

func (s *Session) subagentExpiryLocked() int64 {
	var at int64
	for _, ps := range s.agentSubagents {
		for _, sa := range ps.byID {
			if due := s.subagentDue(sa); at == 0 || due < at {
				at = due
			}
		}
	}
	return at
}

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
				if s.subagentDue(sa) <= at {
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
			return errNoAgentMetaChange
		}
		s.subagentShownAt = at
		return nil
	})
	s.armSubagentPrune(next)
	return dropped
}

func (s *Session) armSubagentPrune(at int64) {
	s.subagentPrune.arm(at, func() { s.expireSubagents(time.Now()) })
}

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

func (s *Session) subagentCount(windowID string) int {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.agentSubagents[windowID].running()
}

func subagentLabel(sa subagent) string {
	switch {
	case sa.description != "" && sa.agentType != "":
		return sa.description + " (" + sa.agentType + ")"
	case sa.description != "":
		return sa.description
	}
	return sa.agentType
}

func subagentListOut(list []SubagentInfo) []SubagentInfo {
	if list == nil {
		return []SubagentInfo{}
	}
	return list
}

var subagentListReturn = verbParam{Name: "subagent_list", Type: "[]object", Description: "The pane's subagents in start order, at most 16, each kept for a minute after it ends (five for a failed one): id, type, description (what it was asked to do), state (running, done, failed or stopped), started_at and ended_at (Unix nanoseconds, ended_at 0 while it runs), now (the tool it runs, such as \"Bash: go test ./...\"), last (the tool it ran last), tools (how many tool calls it made) and result (the first line it ended with, or the error). The strings are the agent's, cut to one line with likely secrets masked. Empty while there are none."}
