package app

import (
	"slices"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// The cockpit is the Inbox drawn as one full-screen surface on ctrl+b i: a
// fleet of every agent grouped by what it needs from the person, and a focus
// area that draws the selected agent's work. The attention items are the
// fleet's Needs you group, so the queue and the agents are one list.
//
// It keeps the Inbox's state machine. Every answer, dismiss, snooze, peek and
// reply runs through the same functions the old panel called, on the item or
// agent under the cockpit's cursor. Before a key runs, cockpitSync points the
// Inbox's own cursors at that row, so a key never acts on a row the person
// cannot see.
//
// Cost: the fleet is rebuilt only when the rail's signature, the Inbox or the
// cockpit's own state changes, and the frame is kept until any of them does.
// Nothing polls. The one timer is the minute tick that moves the ages on,
// armed only while the cockpit is open. Closed, it costs nothing.

// The fleet's groups, in the order they are drawn.
const (
	ckNeeds = iota
	ckReview
	ckWorking
	ckIdle
	ckEarlier
	ckSnoozed
	ckGroups
)

// ckGroupTitles names each group, as its heading says it.
var ckGroupTitles = [ckGroups]string{"Needs you", "Ready for review", "Working", "Idle", "Earlier", "Snoozed"}

// ckEarlierAfter is how long an agent rests before it sinks to Earlier.
const ckEarlierAfter = time.Hour

// ckFoldAt is how many rows a group keeps when the list is taller than its
// column. Needs you never folds.
const ckFoldAt = 8

// ckChildMax is how many subagent rows an agent shows under it.
const ckChildMax = 3

// ckDoubleClick is how long after a click on a row a second click on it still
// reads as a double-click.
const ckDoubleClick = 400 * time.Millisecond

type ckRowKind uint8

const (
	ckRowHead ckRowKind = iota
	ckRowAgent
	ckRowItem
	ckRowChild
	ckRowMore
	ckRowGap
	ckRowNote
)

// ckRow is one row of the fleet.
type ckRow struct {
	kind  ckRowKind
	group int
	// count is a heading's count, and how many rows a "more" row stands
	// for.
	count int
	// agent indexes the cockpit's agents, -1 for an item with no agent.
	agent   int
	item    session.AttentionItem
	hasItem bool
	child   sessiontree.Subagent
	last    bool
	note    string
	key     string
	// lines is how many lines the row takes.
	lines int
}

// selectable reports whether the cursor can rest on the row.
func (r ckRow) selectable() bool { return r.kind == ckRowAgent || r.kind == ckRowItem }

// cockpitState is the cockpit's own state, beside the Inbox's.
type cockpitState struct {
	// key is the selected row, and akey its agent, so the cursor stays on
	// an agent whose approval was answered and whose row key changed.
	key, akey string
	idx       int
	// hover is the row under the pointer. It lights the row and selects
	// nothing.
	hover string
	// scroll is the first fleet line drawn. follow says to bring the
	// cursor's row into view; the wheel turns it off until the cursor moves.
	scroll int
	follow bool
	// folded groups show their heading only; opened ones show every row
	// when the list folds long groups.
	folded, opened [ckGroups]bool
	// needsOnly is the f filter: the Needs you group alone, which is the
	// old flat Inbox.
	needsOnly bool
	// The layout the rows were last built for.
	colH    int
	oneLine bool
	agents  []sidebarAgentEntry
	// branches are the worktree sessions' branches, by host and session,
	// read for the rail signature branchesAt.
	branches   map[string]string
	branchesAt uint64
	// tall is how many lines the fleet takes with two-line rows, whatever
	// it is drawn with, so the choice of one-line rows does not feed back
	// into itself.
	tall   int
	rows   []ckRow
	rowsAt ckRowsKey
	// hits are the click targets of the last frame.
	hits []ckHit
	// lastClick and lastClickKey find a double-click.
	lastClick    time.Time
	lastClickKey string
	// frame is the last frame drawn, and frameAt what it was drawn from.
	frame   string
	frameAt ckFrameKey
	// cardRoom is how many detail lines the item card showed whole in the
	// last frame. A plan is read to its end only when its last line fit.
	cardRoom int
	// gen counts changes of the state above, for the frame key.
	gen uint64
}

// ckRowsKey is what the fleet's rows were built from.
type ckRowsKey struct {
	list                agentsListKey
	inbox, gen          uint64
	colH                int
	oneLine, narrowRows bool
	minute              int64
}

// ckHit is a click target: a fleet row, a group heading, a button.
type ckHit struct {
	x0, y0, x1, y1 int
	kind           ckHitKind
	key            string
	group          int
	action         string
}

type ckHitKind uint8

const (
	ckHitRow ckHitKind = iota
	ckHitHead
	ckHitMore
	ckHitAction
	ckHitTab
)

// cockpitTouch marks the cockpit's own state changed.
func (m *OS) cockpitTouch() { m.Inbox.ck.gen++ }

// CockpitTouch is cockpitTouch for the input path.
func (m *OS) CockpitTouch() { m.cockpitTouch() }

// CockpitSync is cockpitSync for the input path.
func (m *OS) CockpitSync() { m.cockpitSync() }

// cockpitList is the agents the fleet is built from: the rail's model, with
// the selector and the search applied, sorted needs-you first and oldest
// wait first in each state.
func (m *OS) cockpitList() []sidebarAgentEntry {
	return m.inboxAgentsList()
}

// cockpitRows is the fleet as rows, rebuilt only when what it shows changed.
func (m *OS) cockpitRows() []ckRow {
	ck := &m.Inbox.ck
	list := m.cockpitList()
	key := ckRowsKey{
		list: m.Inbox.agents.listAt, inbox: m.Inbox.Gen, gen: ck.gen, colH: ck.colH,
		oneLine: ck.oneLine, minute: time.Now().Unix() / 60,
	}
	if ck.rows != nil && ck.rowsAt == key {
		return ck.rows
	}
	ck.agents = list
	if sig := m.Inbox.agents.listAt.sig; ck.branches == nil || ck.branchesAt != sig {
		ck.branches, ck.branchesAt = m.ckBranches(), sig
	}
	ck.rows, ck.rowsAt = m.buildCockpitRows(list, time.Now()), key
	m.cockpitClamp()
	return ck.rows
}

// buildCockpitRows groups the agents and the Inbox's items.
func (m *OS) buildCockpitRows(list []sidebarAgentEntry, now time.Time) []ckRow {
	ck := &m.Inbox.ck
	st := &m.Inbox
	var groups [ckGroups][]ckRow
	byKey := make(map[string]int, len(list))
	for i, e := range list {
		byKey[e.Host+"\x00"+e.SessionID+"\x00"+e.WindowID] = i
	}
	used := make(map[int]bool)
	// An agent whose item is snoozed rests out of sight with the item.
	for _, it := range st.life.Snoozed {
		if i, ok := byKey[it.Host+"\x00"+it.Session+"\x00"+it.Window]; ok && it.Window != "" {
			used[i] = true
		}
	}
	for _, r := range m.inboxRows() {
		if r.item == nil {
			if r.note != "" && len(groups[ckNeeds]) > 0 {
				groups[ckNeeds] = append(groups[ckNeeds], ckRow{kind: ckRowNote, group: ckNeeds, note: r.note, lines: 1, key: "n:" + r.note})
			}
			continue
		}
		it := *r.item
		g := ckNeeds
		switch {
		case it.SnoozedUntil != 0:
			g = ckSnoozed
		case it.Kind == session.AttentionFinished:
			g = ckReview
		}
		idx := -1
		if it.Window != "" {
			if i, ok := byKey[it.Host+"\x00"+it.Session+"\x00"+it.Window]; ok {
				idx = i
				used[i] = true
			}
		}
		kind := ckRowItem
		if idx >= 0 {
			kind = ckRowAgent
		}
		groups[g] = append(groups[g], ckRow{kind: kind, group: g, agent: idx, item: it, hasItem: true, key: "i:" + it.ID})
	}
	// The Inbox filter narrows to items. Agents join only the unfiltered
	// list, and the f filter keeps the agents that need you.
	kindFilter := st.Filter != ""
	for i, e := range list {
		if used[i] || kindFilter {
			continue
		}
		g := ckIdle
		switch sidebarAgentGroup(e.State, e.DoneSeen) {
		case sidebarGroupNeedsYou:
			// A blocked agent needs you whether or not the Inbox holds an
			// item for it. An error the person dismissed rests with the
			// idle agents, its mark still saying it failed.
			if e.State == "needs_input" {
				g = ckNeeds
			}
		case sidebarGroupFinished:
			g = ckReview
		case sidebarGroupWorking:
			g = ckWorking
		default:
			if e.StateAt > 0 && now.Sub(time.Unix(0, e.StateAt)) > ckEarlierAfter {
				g = ckEarlier
			}
		}
		groups[g] = append(groups[g], ckRow{kind: ckRowAgent, group: g, agent: i, key: "a:" + agentKey(e)})
	}

	var rows []ckRow
	ck.tall = 0
	for g := range ckGroups {
		items := groups[g]
		if len(items) == 0 || (ck.needsOnly || kindFilter) && g != ckNeeds && g != ckSnoozed {
			continue
		}
		n := 0
		for _, r := range items {
			if r.selectable() {
				n++
			}
		}
		if len(rows) > 0 {
			rows = append(rows, ckRow{kind: ckRowGap, group: g, lines: 1, key: "g:" + strconv.Itoa(g)})
		}
		rows = append(rows, ckRow{kind: ckRowHead, group: g, count: n, lines: 1, key: "h:" + strconv.Itoa(g)})
		if ck.folded[g] {
			continue
		}
		shown := 0
		for _, r := range items {
			if r.selectable() && g != ckNeeds && ck.oneLine && !ck.opened[g] && shown >= ckFoldAt {
				continue
			}
			if r.selectable() {
				shown++
			}
			r.lines = 2
			var e sidebarAgentEntry
			if r.agent >= 0 {
				e = list[r.agent]
			}
			if g == ckIdle || g == ckEarlier || g == ckSnoozed || !m.ckHasLine2(r, e) {
				r.lines = 1
			}
			ck.tall += r.lines
			if ck.oneLine {
				r.lines = 1
			}
			if r.kind == ckRowNote {
				r.lines = 1
			}
			rows = append(rows, r)
			if r.kind == ckRowAgent && !r.hasItem && !ck.oneLine {
				rows = append(rows, ckChildRows(list[r.agent], g)...)
			}
		}
		if hidden := n - shown; hidden > 0 {
			rows = append(rows, ckRow{kind: ckRowMore, group: g, count: hidden, lines: 1, key: "m:" + strconv.Itoa(g)})
		}
	}
	return rows
}

// ckChildRows are an agent's subagents, one line each under its row.
func ckChildRows(e sidebarAgentEntry, g int) []ckRow {
	var rows []ckRow
	for i, c := range e.Children {
		if i == ckChildMax {
			rows = append(rows, ckRow{kind: ckRowMore, group: -1, count: len(e.Children) - i, lines: 1, key: "c:" + agentKey(e) + ":more"})
			break
		}
		rows = append(rows, ckRow{kind: ckRowChild, group: g, child: c, lines: 1, key: "c:" + agentKey(e) + ":" + c.ID})
	}
	if n := len(rows); n > 0 {
		rows[n-1].last = true
	}
	return rows
}

// cockpitClamp keeps the cursor on the selected row, on its agent's new row
// when the row changed kind, or on the nearest row.
func (m *OS) cockpitClamp() {
	ck := &m.Inbox.ck
	rows := ck.rows
	find := func(match func(ckRow) bool) int {
		for i, r := range rows {
			if r.selectable() && match(r) {
				return i
			}
		}
		return -1
	}
	i := -1
	if id := m.Inbox.pendingItem; id != "" {
		if i = find(func(r ckRow) bool { return r.key == "i:"+id }); i >= 0 {
			m.Inbox.pendingItem = ""
		}
	}
	if i < 0 && ck.key != "" {
		i = find(func(r ckRow) bool { return r.key == ck.key })
	}
	if i < 0 && ck.akey != "" {
		i = find(func(r ckRow) bool { return r.agent >= 0 && agentKey(ck.agents[r.agent]) == ck.akey })
	}
	if i < 0 {
		i = ckNearest(rows, ck.idx)
	}
	m.cockpitSetCursor(i)
}

// ckNearest is the selectable row nearest idx, -1 when there is none.
func ckNearest(rows []ckRow, idx int) int {
	idx = min(max(idx, 0), len(rows)-1)
	for d := 0; d < len(rows); d++ {
		for _, j := range [2]int{idx + d, idx - d} {
			if j >= 0 && j < len(rows) && rows[j].selectable() {
				return j
			}
		}
	}
	return -1
}

// cockpitSetCursor puts the cursor on row i.
func (m *OS) cockpitSetCursor(i int) {
	ck := &m.Inbox.ck
	if i < 0 || i >= len(ck.rows) {
		ck.key, ck.akey = "", ""
		return
	}
	r := ck.rows[i]
	ck.follow = ck.follow || ck.key != r.key
	ck.idx, ck.key, ck.akey = i, r.key, ""
	if r.agent >= 0 {
		ck.akey = agentKey(ck.agents[r.agent])
	}
}

// cockpitSelected is the row under the cursor.
func (m *OS) cockpitSelected() (ckRow, bool) {
	rows := m.cockpitRows()
	ck := &m.Inbox.ck
	if ck.idx < 0 || ck.idx >= len(rows) || rows[ck.idx].key != ck.key || !rows[ck.idx].selectable() {
		return ckRow{}, false
	}
	return rows[ck.idx], true
}

// cockpitAgent is the agent of the row under the cursor.
func (m *OS) cockpitAgent() (sidebarAgentEntry, bool) {
	r, ok := m.cockpitSelected()
	if !ok || r.agent < 0 {
		return sidebarAgentEntry{}, false
	}
	return m.Inbox.ck.agents[r.agent], true
}

// cockpitSync points the Inbox's item cursor and the agents cursor at the
// row under the cockpit's cursor, and at nothing when the row has no item or
// no agent. Every key and click calls it first, so the functions the old
// panel used act on the row the person sees.
func (m *OS) cockpitSync() (ckRow, bool) {
	st := &m.Inbox
	r, ok := m.cockpitSelected()
	st.Selected, st.SelectedID = -1, ""
	a := &st.agents
	a.sel, a.key = -1, ""
	if !ok {
		return r, false
	}
	if r.agent >= 0 {
		list := m.inboxAgentsList()
		e := st.ck.agents[r.agent]
		for i := range list {
			if agentKey(list[i]) == agentKey(e) {
				a.sel, a.key = i, agentKey(e)
				break
			}
		}
	}
	if r.hasItem {
		for i, row := range m.inboxRows() {
			if row.item != nil && row.item.ID == r.item.ID {
				st.Selected, st.SelectedID = i, r.item.ID
				break
			}
		}
	}
	return r, true
}

// CockpitMove moves the cursor by delta selectable rows.
func (m *OS) CockpitMove(delta int) {
	rows := m.cockpitRows()
	ck := &m.Inbox.ck
	if len(rows) == 0 || delta == 0 {
		return
	}
	var sel []int
	at := -1
	for i, r := range rows {
		if r.selectable() {
			if i == ck.idx {
				at = len(sel)
			}
			sel = append(sel, i)
		}
	}
	if len(sel) == 0 {
		return
	}
	if at < 0 {
		at = 0
	} else {
		at = min(max(at+delta, 0), len(sel)-1)
	}
	m.cockpitSetCursor(sel[at])
	m.Inbox.pendingItem = ""
	m.cockpitTouch()
	// The detail reads that follow a key ask the Inbox's cursors.
	m.cockpitSync()
}

// cockpitSelectKey puts the cursor on the row with key.
func (m *OS) cockpitSelectKey(key string) bool {
	for i, r := range m.cockpitRows() {
		if r.key == key && r.selectable() {
			m.cockpitSetCursor(i)
			m.cockpitTouch()
			m.cockpitSync()
			return true
		}
	}
	return false
}

// CockpitToggleGroup folds or unfolds a group, or shows every row of a long
// group the list folded.
func (m *OS) CockpitToggleGroup(g int, more bool) {
	ck := &m.Inbox.ck
	if g < 0 || g >= ckGroups {
		return
	}
	if more {
		ck.opened[g] = !ck.opened[g]
	} else {
		ck.folded[g] = !ck.folded[g]
	}
	m.cockpitTouch()
}

// CockpitCycleFilter is f: the whole fleet, then the Needs you group alone.
// A kind filter, such as the mail one ctrl+b M opens on, is cleared first.
func (m *OS) CockpitCycleFilter() {
	st := &m.Inbox
	if st.Filter != "" {
		st.Filter = ""
		st.ck.needsOnly = false
	} else {
		st.ck.needsOnly = !st.ck.needsOnly
	}
	st.ck.scroll = 0
	m.cockpitTouch()
}

// CockpitNeedsOnly reports whether the f filter shows only Needs you.
func (m *OS) CockpitNeedsOnly() bool { return m.Inbox.ck.needsOnly }

// cockpitHeldItem is the held approval, plan or question of the row under the
// cursor, which a, A, r and R answer.
func (m *OS) cockpitHeldItem() (session.AttentionItem, bool) {
	r, ok := m.cockpitSelected()
	if !ok || !r.hasItem || r.item.RequestID == "" {
		return session.AttentionItem{}, false
	}
	switch r.item.Kind {
	case session.AttentionApproval, session.AttentionPlan:
		return r.item, true
	}
	return session.AttentionItem{}, false
}

// CockpitAnswerLetter answers the held approval under the cursor with a
// letter: a allows once, A always, r rejects. handled is false when no
// approval waits, and the key then does what it does on any row.
func (m *OS) CockpitAnswerLetter(key string) (tea.Cmd, bool) {
	it, ok := m.cockpitHeldItem()
	if !ok {
		return nil, false
	}
	m.cockpitSync()
	decision := session.ApprovalDeny
	switch key {
	case "a":
		decision = session.ApprovalOnce
	case "A":
		decision = session.ApprovalAlways
	}
	if !slices.Contains(it.Options, decision) {
		return nil, true
	}
	return m.InboxReplyApproval(decision), true
}

// CockpitNewAgent is n and the New agent button. Until the spawn sheet
// lands, it opens the launcher, where any agent's program starts in a new
// pane and the cockpit finds it.
func (m *OS) CockpitNewAgent() tea.Cmd {
	m.CloseInbox()
	cmd := m.OpenLauncher()
	m.ShowNotification("Start an agent such as claude or codex. It shows in the agents view.", "info", m.Settings.NotificationDuration)
	return cmd
}

// CockpitGo is enter: go to the row's pane and close the cockpit. Mail opens
// its thread, as before.
func (m *OS) CockpitGo() tea.Cmd {
	r, ok := m.cockpitSync()
	if !ok {
		return nil
	}
	if r.hasItem {
		return m.InboxActivate()
	}
	return m.InboxAgentsGo()
}

// CockpitOpenFocus shows the focus area in place of the fleet on a narrow
// screen.
func (m *OS) CockpitOpenFocus(open bool) tea.Cmd {
	if m.cockpitWideFocus() {
		return nil
	}
	m.Inbox.threads.open = open
	m.cockpitTouch()
	if open {
		return m.InboxThreadFetch()
	}
	return nil
}

// cockpitWideFocus reports whether the focus area is drawn beside the fleet.
func (m *OS) cockpitWideFocus() bool { return m.GetRenderWidth() >= ckOneColumn }

// CockpitClick handles a click at screen x, y while the cockpit is up.
// handled is false for a click outside it.
func (m *OS) CockpitClick(x, y int) (tea.Cmd, bool) {
	ck := &m.Inbox.ck
	for _, h := range ck.hits {
		if x < h.x0 || x >= h.x1 || y < h.y0 || y >= h.y1 {
			continue
		}
		switch h.kind {
		case ckHitHead:
			m.CockpitToggleGroup(h.group, false)
		case ckHitMore:
			m.CockpitToggleGroup(h.group, true)
		case ckHitRow:
			now := time.Now()
			double := ck.lastClickKey == h.key && now.Sub(ck.lastClick) <= ckDoubleClick
			ck.lastClick, ck.lastClickKey = now, h.key
			m.cockpitSelectKey(h.key)
			if double {
				ck.lastClickKey = ""
				return m.CockpitGo(), true
			}
			if !m.cockpitWideFocus() {
				return m.CockpitOpenFocus(true), true
			}
			return m.InboxThreadFetch(), true
		case ckHitAction:
			return m.cockpitAction(h.action), true
		}
		return nil, true
	}
	return nil, true
}

// cockpitAction runs a button's action.
func (m *OS) cockpitAction(action string) tea.Cmd {
	switch action {
	case "new":
		return m.CockpitNewAgent()
	case "search":
		m.InboxStartSelect()
	case "needs":
		m.CockpitCycleFilter()
	case "a", "A", "r":
		cmd, _ := m.CockpitAnswerLetter(action)
		return cmd
	case "R":
		m.cockpitSync()
		cmd, _ := m.InboxDenyReason()
		return cmd
	case "reply":
		m.cockpitSync()
		return m.cockpitReply()
	case "agents":
		m.CloseInbox()
		m.OpenSettingsAt("Agents")
	case "back":
		return m.CockpitOpenFocus(false)
	default:
		if n, err := strconv.Atoi(action); err == nil {
			m.cockpitSync()
			return m.InboxNumber(n)
		}
	}
	return nil
}

// cockpitReply opens the reply editor for the row under the cursor: mail
// replies to the mail, anything else to the agent.
func (m *OS) cockpitReply() tea.Cmd {
	r, ok := m.cockpitSync()
	if !ok {
		return nil
	}
	if r.hasItem && (r.item.Kind == session.AttentionMail || r.agent < 0) {
		return m.InboxReply()
	}
	return m.InboxAgentsReply()
}

// CockpitHover lights the row under the pointer. It selects nothing.
func (m *OS) CockpitHover(x, y int) {
	ck := &m.Inbox.ck
	key := ""
	for _, h := range ck.hits {
		if h.kind == ckHitRow && x >= h.x0 && x < h.x1 && y >= h.y0 && y < h.y1 {
			key = h.key
			break
		}
	}
	if key != ck.hover {
		ck.hover = key
		m.cockpitTouch()
	}
}

// CockpitWheel scrolls the fleet, or moves the cursor when the pointer is
// over the focus area.
func (m *OS) CockpitWheel(x int, up bool) {
	ck := &m.Inbox.ck
	if x < ck.fleetRight() {
		if up {
			ck.scroll = max(ck.scroll-3, 0)
		} else {
			ck.scroll += 3
		}
		ck.follow = false
		m.cockpitTouch()
		return
	}
	m.CockpitMove(wheelDelta(up))
}

// cockpitGroupHides reports whether a group shows only its first rows.
func (m *OS) cockpitGroupHides(g int) bool {
	for _, r := range m.Inbox.ck.rows {
		if r.kind == ckRowMore && r.group == g {
			return true
		}
	}
	return false
}

// fleetRight is the column past the fleet in the last frame.
func (ck *cockpitState) fleetRight() int {
	for _, h := range ck.hits {
		if h.kind == ckHitRow || h.kind == ckHitHead {
			return h.x1
		}
	}
	return 0
}

// CockpitKey handles the cockpit's own keys ahead of the Inbox bindings: the
// approval letters, n, and tab. handled is false for any other key.
func (m *OS) CockpitKey(key, action string) (tea.Cmd, bool) {
	switch key {
	case "a", "A", "r":
		if cmd, ok := m.CockpitAnswerLetter(key); ok {
			return cmd, true
		}
	case "right", "l":
		if !m.cockpitWideFocus() && !m.Inbox.threads.open {
			return m.CockpitOpenFocus(true), true
		}
		if r, ok := m.cockpitSelected(); ok && m.cockpitGroupHides(r.group) {
			m.CockpitToggleGroup(r.group, true)
		}
		return nil, true
	case "left", "h":
		if m.Inbox.threads.open {
			return m.CockpitOpenFocus(false), true
		}
		return nil, true
	case "?":
		m.CloseInbox()
		m.OpenHelpAtCategory("Inbox")
		return nil, true
	}
	switch action {
	case config.ActionInboxNewAgent:
		return m.CockpitNewAgent(), true
	case config.ActionInboxAgents:
		// tab: the reply editor, where the next words go.
		return m.cockpitReply(), true
	case config.ActionInboxFilter:
		m.CockpitCycleFilter()
		return nil, true
	case config.ActionInboxReply:
		return m.cockpitReply(), true
	case config.ActionInboxGo:
		if !m.cockpitWideFocus() && !m.Inbox.threads.open {
			return m.CockpitOpenFocus(true), true
		}
		return m.CockpitGo(), true
	case config.ActionInboxClose:
		if m.Inbox.threads.open {
			return m.CockpitOpenFocus(false), true
		}
	case config.ActionInboxUp, config.ActionInboxDown, config.ActionInboxPageUp, config.ActionInboxPageDown,
		config.ActionInboxFirst, config.ActionInboxLast:
		d := 1
		switch action {
		case config.ActionInboxUp:
			d = -1
		case config.ActionInboxPageUp:
			d = -10
		case config.ActionInboxPageDown:
			d = 10
		case config.ActionInboxFirst:
			d = -1 << 20
		case config.ActionInboxLast:
			d = 1 << 20
		}
		m.CockpitMove(d)
		return nil, true
	}
	return nil, false
}
