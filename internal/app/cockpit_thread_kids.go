package app

import (
	"hash/fnv"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// Child pills on the cards that started agents.
//
// A Task or Agent card in the thread started one of the harness's own
// subagents, and a tuios start-agent or delegate call started agent panes
// that are linked to this one (the parent link, agent_parent.go). Under such
// a card the thread draws a pill per child: its mark, its name and its state.
// The pill cursor (inbox_child, "." by default) steps through the pills of
// linked panes, newest first, and enter on one opens that agent's thread in
// the cockpit. A subagent's pill is drawn and takes no cursor: the client has
// no thread of a subagent to show, since the daemon reads the pane's own
// transcript only.
//
// Everything comes from what the client holds: the thread's entries, the
// pane's subagent list, and the agents the cockpit lists. Nothing here calls
// the daemon or runs a timer. The pills sit on tool cards, which the thread
// builds on every rebuild rather than from the card cache, and the body key
// carries ckKidsSig, so a child that changes state redraws the thread and a
// thread whose children did not change keeps its lines.

// ckPill is one child on a card.
type ckPill struct {
	// id is the pill's identity in the thread: "a:" and the child's agent
	// key for a linked pane, "s:" and the subagent's id or the card's for a
	// subagent.
	id    string
	name  string
	state string
	// open is the agent key of a linked pane, which enter opens. Empty for
	// a subagent.
	open string
}

// ckStartsSubagent reports whether a tool call starts one of the harness's
// own subagents.
func ckStartsSubagent(tool string) bool {
	switch tool {
	case "Task", "Agent":
		return true
	}
	return false
}

// ckStartsAgents reports whether a tool call starts tuios agent panes: a
// shell command that runs start-agent, delegate or fan, or the MCP tools of
// the same names.
func ckStartsAgents(tool, target string) bool {
	low := strings.ToLower(tool)
	if strings.Contains(low, "start_agent") || strings.Contains(low, "delegate") {
		return true
	}
	if low != "bash" && low != "shell" && low != "run_shell_command" && low != "exec_command" {
		return false
	}
	return strings.Contains(target, "tuios") &&
		(strings.Contains(target, "start-agent") || strings.Contains(target, " delegate") || strings.Contains(target, " fan "))
}

// ckLinkedKids are the agents the cockpit lists that the pane e started,
// in the cockpit's order.
func (m *OS) ckLinkedKids(e sidebarAgentEntry) []sidebarAgentEntry {
	if e.Host != "" {
		return nil
	}
	var out []sidebarAgentEntry
	for _, k := range m.Inbox.ck.agents {
		if k.Host == "" && k.Parent != nil && k.Parent.Session == e.SessionID && k.Parent.Window == e.WindowID {
			out = append(out, k)
		}
	}
	return out
}

// ckChildPills is the pills of each card of the thread that started
// children, by the card's entry id.
//
// A subagent card takes the first subagent of its description not yet
// taken, and without one the card's own status says the state. A start card
// takes the linked panes whose name its command names; the panes no command
// names go to the newest start card, which is the call most likely to have
// started them.
func (m *OS) ckChildPills(e sidebarAgentEntry, entries []threadEntry) map[string][]ckPill {
	var out map[string][]ckPill
	put := func(id string, p ckPill) {
		if out == nil {
			out = map[string][]ckPill{}
		}
		out[id] = append(out[id], p)
	}
	used := make([]bool, len(e.Children))
	var starts []threadEntry
	for _, en := range entries {
		if en.Kind != "tool_call" {
			continue
		}
		switch {
		case ckStartsSubagent(en.Tool):
			put(en.ID, ckSubagentPill(en, e.Children, used))
		case ckStartsAgents(en.Tool, en.Target):
			starts = append(starts, en)
		}
	}
	if len(starts) == 0 {
		return out
	}
	kids := m.ckLinkedKids(e)
	placed := make([]bool, len(kids))
	for _, en := range starts {
		for i, k := range kids {
			name := printableTitle(k.Title)
			if !placed[i] && len([]rune(name)) > 1 && strings.Contains(en.Target, name) {
				placed[i] = true
				put(en.ID, ckLinkedPill(k))
			}
		}
	}
	last := starts[len(starts)-1].ID
	for i, k := range kids {
		if !placed[i] {
			put(last, ckLinkedPill(k))
		}
	}
	return out
}

// ckSubagentPill is the pill of a subagent card.
func ckSubagentPill(en threadEntry, list []sessiontree.Subagent, used []bool) ckPill {
	desc := printableTitle(en.Target)
	for i, s := range list {
		if !used[i] && desc != "" && printableTitle(s.Description) == desc {
			used[i] = true
			return ckPill{id: "s:" + s.ID, name: subagentName(s), state: subagentMarkState(s)}
		}
	}
	state := "done"
	switch en.Status {
	case "running":
		state = "working"
	case "error":
		state = "errored"
	}
	name := desc
	if name == "" {
		name = "subagent"
	}
	return ckPill{id: "s:" + en.ID, name: name, state: state}
}

// ckLinkedPill is the pill of a linked agent pane.
func ckLinkedPill(k sidebarAgentEntry) ckPill {
	key := agentKey(k)
	return ckPill{id: "a:" + key, name: sidebarAgentName(k), state: sidebarKinState(k), open: key}
}

// ckPillLines lays a card's pills out in rows under its tool line. sel is
// the pill under the pill cursor. A line's hit names the pills on it.
func (m *OS) ckPillLines(l ckLook, pills []ckPill, sel string, width int) []ckLine {
	pal := l.pal
	const indent = 4
	room := max(width-indent-2, 12)
	var out []ckLine
	var cur ckLine
	var ids []string
	used := 0
	flush := func() {
		if len(cur.segs) > 0 {
			cur.indent = indent
			cur.hit = "kid:" + strings.Join(ids, "\x1f")
			out = append(out, cur)
		}
		cur, ids, used = ckLine{}, nil, 0
	}
	for _, p := range pills {
		mark, ink := l.ckMark(p.state, false)
		words := sidebarStateWords(p.state)
		name := ckTrunc(p.name, max(room-ckWidth(ckSeg{t: mark + "  " + words + "  "}), 6))
		bg, fg, wordFg := l.panel, pal.FgDim, pal.FgMute
		var attrs uint8
		if p.id == sel && p.open != "" {
			bg, fg, wordFg, ink, attrs = pal.Accent, pal.PillFg, pal.PillFg, pal.PillFg, ckBold
		}
		segs := []ckSeg{
			{t: " " + mark + " ", fg: ink, bg: bg},
			{t: name, fg: fg, bg: bg, attrs: attrs},
			{t: " " + words + " ", fg: wordFg, bg: bg},
		}
		w := ckWidth(segs...)
		if used > 0 && used+1+w > room {
			flush()
		}
		if used > 0 {
			cur.segs = append(cur.segs, ckSeg{t: " "})
			used++
		}
		cur.segs = append(cur.segs, segs...)
		ids = append(ids, p.id)
		used += w
	}
	flush()
	return out
}

// ckKidsSig is what the pills of e's thread depend on beyond the entries:
// the pill cursor, the pane's subagents and its linked panes' names and
// states. The thread's body key carries it.
func (m *OS) ckKidsSig(e sidebarAgentEntry, t *agentThread) uint64 {
	if t == nil {
		return 0
	}
	h := fnv.New64a()
	h.Write([]byte(t.pill))
	for _, s := range e.Children {
		h.Write([]byte("\x00" + s.ID + "\x00" + s.State + "\x00" + s.Description))
	}
	for _, k := range m.ckLinkedKids(e) {
		h.Write([]byte("\x00" + agentKey(k) + "\x00" + k.Title + "\x00" + sidebarKinState(k)))
	}
	return h.Sum64()
}

// ckOpenablePills is every pill of the thread enter can open, newest first.
func (m *OS) ckOpenablePills(e sidebarAgentEntry, t *agentThread) []string {
	by := m.ckChildPills(e, t.entries)
	var out []string
	for _, en := range slices.Backward(t.entries) {
		for _, p := range by[en.ID] {
			if p.open != "" {
				out = append(out, p.id)
			}
		}
	}
	return out
}

// CockpitNextChild moves the pill cursor of the thread on screen to the next
// pill, newest first, and off the pills after the last. It scrolls the thread
// to the pill. It reports false when the thread has no pill to step to.
func (m *OS) CockpitNextChild() bool {
	e, ok := m.cockpitAgent()
	t := m.cockpitThread()
	if !ok || t == nil {
		return false
	}
	ids := m.ckOpenablePills(e, t)
	if len(ids) == 0 {
		if t.pill == "" {
			return false
		}
		t.pill = ""
		m.cockpitTouch()
		return true
	}
	next := ids[0]
	for i, id := range ids {
		if id == t.pill {
			next = ""
			if i+1 < len(ids) {
				next = ids[i+1]
			}
			break
		}
	}
	t.pill = next
	if next != "" {
		for j, hit := range slices.Backward(t.hits) {
			if h, ok := strings.CutPrefix(hit, "kid:"); ok && ckHasID(h, next) {
				// A few lines of the thread stay under the pill.
				t.follow.Scroll(max(len(t.hits)-1-j-3, 0) - t.follow.Up)
				break
			}
		}
	}
	m.cockpitTouch()
	return true
}

// ckHasID reports whether a pill line's hit names id.
func ckHasID(hit, id string) bool {
	for p := range strings.SplitSeq(hit, "\x1f") {
		if p == id {
			return true
		}
	}
	return false
}

// cockpitChildGo opens the thread of the pane under the pill cursor: the
// cockpit's cursor goes to that agent's row. ok is false when no pill is
// under the cursor, so enter does what it did before.
func (m *OS) cockpitChildGo() (tea.Cmd, bool) {
	e, ok := m.cockpitAgent()
	t := m.cockpitThread()
	if !ok || t == nil || t.pill == "" {
		return nil, false
	}
	key, isPane := strings.CutPrefix(t.pill, "a:")
	if !isPane {
		return nil, false
	}
	kidOf := false
	for _, k := range m.ckLinkedKids(e) {
		if agentKey(k) == key {
			kidOf = true
			break
		}
	}
	if !kidOf {
		t.pill = ""
		return nil, false
	}
	return m.cockpitOpenAgent(key), true
}

// cockpitChildClick handles a click on a pill line: the pill under the
// cursor on that line opens, and on a line without it the first pill that
// opens takes the cursor.
func (m *OS) cockpitChildClick(hit string) tea.Cmd {
	t := m.cockpitThread()
	if t == nil {
		return nil
	}
	if t.pill != "" && ckHasID(hit, t.pill) {
		cmd, _ := m.cockpitChildGo()
		return cmd
	}
	for p := range strings.SplitSeq(hit, "\x1f") {
		if strings.HasPrefix(p, "a:") {
			t.pill = p
			m.cockpitTouch()
			return nil
		}
	}
	return nil
}

// cockpitOpenAgent puts the cockpit's cursor on the agent with key, which
// shows its thread, and asks for the thread. It does nothing when the fleet
// does not list that agent, as under a filter that hides it.
func (m *OS) cockpitOpenAgent(key string) tea.Cmd {
	ck := &m.Inbox.ck
	for i, r := range m.cockpitRows() {
		if r.agent >= 0 && r.agent < len(ck.agents) && r.cursorable() && agentKey(ck.agents[r.agent]) == key {
			m.cockpitSetCursor(i)
			m.cockpitSync()
			m.cockpitTouch()
			m.cockpitReplyFollow()
			return m.InboxThreadFetch()
		}
	}
	return nil
}

// ckChildHints are the footer's hints for the pills: the step key while the
// thread has pills that open. Enter's own hint says "open agent" while a pill
// is under the cursor.
func (m *OS) ckChildHints() []overlay.Hint {
	e, ok := m.cockpitAgent()
	t := m.cockpitThread()
	if !ok || t == nil {
		return nil
	}
	if t.pill != "" {
		return m.keyHints(config.ActionInboxChild, "next agent")
	}
	if len(m.ckOpenablePills(e, t)) == 0 {
		return nil
	}
	return m.keyHints(config.ActionInboxChild, "child agents")
}
