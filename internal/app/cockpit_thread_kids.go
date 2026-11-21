package app

import (
	"hash/fnv"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

type ckPill struct {
	id    string
	name  string
	state string
	open  string
}

func ckStartsSubagent(tool string) bool {
	switch tool {
	case "Task", "Agent":
		return true
	}
	return false
}

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

func ckLinkedPill(k sidebarAgentEntry) ckPill {
	key := agentKey(k)
	return ckPill{id: "a:" + key, name: sidebarAgentName(k), state: sidebarKinState(k), open: key}
}

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
	if ckThreadTalks(t) {
		h.Write([]byte("\x00" + strconv.FormatUint(m.ckPeersSig(), 10)))
	}
	return h.Sum64()
}

func (m *OS) ckOpenablePills(e sidebarAgentEntry, t *agentThread) []string {
	by := m.ckChildPills(e, t.entries)
	var peers map[string][]ckPeer
	if ckThreadTalks(t) {
		peers = m.ckThreadPeers(e, t)
	}
	var out []string
	for _, en := range slices.Backward(t.entries) {
		for _, p := range peers[en.ID] {
			out = append(out, p.id)
		}
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
			if h, ok := strings.CutPrefix(hit, "kid:"); ok && ckHasID(h, next) || hit == "peer:"+next {
				t.follow.Scroll(max(len(t.hits)-1-j-3, 0) - t.follow.Up)
				break
			}
		}
	}
	m.cockpitTouch()
	return true
}

func ckHasID(hit, id string) bool {
	for p := range strings.SplitSeq(hit, "\x1f") {
		if p == id {
			return true
		}
	}
	return false
}

func (m *OS) cockpitChildGo() (tea.Cmd, bool) {
	e, ok := m.cockpitAgent()
	t := m.cockpitThread()
	if !ok || t == nil || t.pill == "" {
		return nil, false
	}
	if strings.HasPrefix(t.pill, "p:") {
		return m.ckPeerGo(e, t, t.pill), true
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

func (m *OS) ckChildHints() []overlay.Hint {
	e, ok := m.cockpitAgent()
	t := m.cockpitThread()
	if !ok || t == nil {
		return nil
	}
	if t.pill != "" {
		return m.keyHints(config.ActionInboxChild, "next agent")
	}
	ids := m.ckOpenablePills(e, t)
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		if strings.HasPrefix(id, "p:") {
			return m.keyHints(config.ActionInboxChild, "agents in thread")
		}
	}
	return m.keyHints(config.ActionInboxChild, "child agents")
}
