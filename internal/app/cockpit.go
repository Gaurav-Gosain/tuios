package app

import (
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
	"tuios.dev/loom"
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

// ckHold is how long an agent's new group must last before its row moves
// there, so a hook that flips working, needs you, working in a moment does
// not move the row twice.
const ckHold = 400 * time.Millisecond

type ckRowKind uint8

const (
	ckRowHead ckRowKind = iota
	ckRowAgent
	ckRowItem
	ckRowChild
	ckRowMore
	ckRowGap
	ckRowNote
	ckRowFan
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
	// attempt marks an agent row drawn as one attempt under its fan's row.
	attempt bool
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
	// branches are the worktree sessions' records, by host and session,
	// read for the rail signature branchesAt.
	branches   map[string]ckWorktree
	branchesAt uint64
	// machines counts the agents on each machine, by host, "" for this
	// one, from the last list no selector or search narrowed.
	machines map[string]int
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
	// liveID is the live pane, and live its last rows as last read, for
	// liveCols by liveRows; liveDirty says the pane had output since.
	liveID             string
	live               string
	liveCols, liveRows int
	liveDirty          bool
	// liveGen counts the live box's new reads, for the frame key; liveTick
	// paces them. liveShown says the last frame drew the box. liveOwned
	// says the view subscribed the live pane, and livePrime counts the
	// primes asked for, so one the cursor left is dropped.
	liveGen, liveTick, livePrime uint64
	// primeID is the pane the prime timer is for, and primeTimer the one
	// timer, moved on every step of the cursor.
	primeID              string
	primeTimer           *time.Timer
	liveShown, liveOwned bool
	// cpWant says a frame drew checkpoints read for an older state.
	cpWant bool
	// cps are the agents' checkpoints, by agent.
	cps map[string]*ckCheckpoints
	// img is the image view, nil when it is not up.
	img *ckImageView
	// tab is the focus tab by name, so it stays put when the tabs change
	// with the screen's width or the agent: Thread, Changes, Checkpoints,
	// Pane. A name the row has not is its first tab.
	tab string
	// dv is the diff theme the thread's cards draw with, for dvKey.
	dv    *diffview.Theme
	dvKey string
	// cardRoom is how many detail lines the item card showed whole in the
	// last frame. A plan is read to its end only when its last line fit.
	cardRoom int
	// gen counts changes of the state above, for the frame key. rowsGen
	// counts the changes that move rows: folds, filters, a held group let
	// go. A cursor move or a hover changes gen alone.
	gen, rowsGen uint64
	// pending is the item to put the cursor on once the rows hold it: an
	// item a notification link or a question's pop opened the view on.
	pending string
	// fleetW is the fleet column's width in the last frame, 0 when the
	// fleet was not drawn.
	fleetW int
	// placed is the group each agent's row is drawn in, and the group it
	// is moving to. holdUntil is when the next held move is due.
	placed    map[string]ckPlaced
	holdUntil time.Time
	holdTimer *time.Timer
	// back is where the cursor was when ctrl+] left for a pane, for the
	// view to open on again. Nil once used.
	back *ckBack
	// look is the palette the frames are drawn with, and lookAt what it
	// was built from. canvas is the grid kept from frame to frame.
	look   ckLook
	lookAt ckLookKey
	// lookGen counts the looks built, for the keys of what is drawn with
	// one.
	lookGen uint64
	canvas  *ckCanvas
	// replyCheck says the rows moved the cursor, so the reply editor is
	// checked against the row after the message.
	replyCheck bool
	// edW is the width the reply editor was last drawn at.
	edW int
	// cutKey is the held approval or question the card could not show
	// whole, by inboxShownKey. Its keys are refused with a word.
	cutKey string
	// spawn is the open spawn sheet, and spawnDraft a closed one kept for
	// the next n. spawnGen counts openings, and spawnLast is the agent the
	// last Start used. See cockpit_spawn.go.
	spawn, spawnDraft *spawnSheet
	spawnGen          uint64
	spawnLast         string
	spawnPreset       *spawnPreset
	// spawnBg is the dimmed cockpit under the sheet, and spawnBgAt the
	// frame it was drawn from, so a key typed on the sheet does not draw
	// the cockpit again. spawnFade holds the faded colours.
	spawnBg   []uv.Line
	spawnBgAt ckFrameKey
	spawnFade map[color.Color]color.Color
	// cmp is the open comparison of a fan's attempts, nil when it is not up.
	// See cockpit_compare.go.
	cmp *ckCompare
	// art is the Artifacts tab. See cockpit_artifacts.go.
	art ckArtState
}

// ckBack is the cursor and the scroll to open the view on again.
type ckBack struct {
	key, akey string
	scroll    int
}

// ckPlaced is the group an agent's row is in, and the group its state asks
// for since when.
type ckPlaced struct {
	shown, want int
	since       time.Time
}

// ckRowsKey is what the fleet's rows were built from.
type ckRowsKey struct {
	list       agentsListKey
	inbox, gen uint64
	// arts moves when a surface opens or closes, or one is seen: a new
	// surface puts a chip on its agent's row.
	arts    uint64
	colH    int
	oneLine bool
	minute  int64
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
	ckHitMark
)

// cockpitTouch marks the cockpit's own state changed.
func (m *OS) cockpitTouch() { m.Inbox.ck.gen++ }

// cockpitTouchRows marks a change that moves the fleet's rows.
func (m *OS) cockpitTouchRows() {
	m.Inbox.ck.gen++
	m.Inbox.ck.rowsGen++
}

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
	now := time.Now()
	m.cockpitSettleHold(now)
	key := ckRowsKey{
		list: m.Inbox.agents.listAt, inbox: m.Inbox.Gen, gen: ck.rowsGen, colH: ck.colH, arts: m.tml.setGen<<20 + ck.art.gen,
		oneLine: ck.oneLine, minute: now.Unix() / 60,
	}
	if ck.rows != nil && ck.rowsAt == key {
		return ck.rows
	}
	ck.agents = list
	if sig := m.Inbox.agents.listAt.sig; ck.branches == nil || ck.branchesAt != sig {
		ck.branches, ck.branchesAt = m.ckBranches(), sig
	}
	if m.Inbox.Select == "" && m.Inbox.agents.query == "" {
		ck.machines = map[string]int{}
		for _, e := range list {
			ck.machines[e.Host]++
		}
	}
	ck.rows, ck.rowsAt = m.buildCockpitRows(list, now), key
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
	searching := st.agents.query != "" || st.Select != ""
	judges := m.ckJudgeWindows()
	for _, r := range m.inboxRows() {
		if r.item == nil {
			if r.note != "" && len(groups[ckNeeds]) > 0 {
				groups[ckNeeds] = append(groups[ckNeeds], ckRow{kind: ckRowNote, group: ckNeeds, note: r.note, lines: 1, key: "n:" + r.note})
			}
			continue
		}
		it := *r.item
		if it.Kind == session.AttentionFinished && judges[it.Host+"\x00"+it.Window] {
			// A judge's finished turn is told of on its fan's row.
			continue
		}
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
		// A search narrows the items with the agents: an item stays when
		// its agent matched, or when its own words do.
		if searching && idx < 0 && !m.ckItemMatches(it) {
			continue
		}
		if g == ckReview && idx >= 0 {
			// A finished turn the agent has moved on from: the row goes
			// where the agent's state says, without the stale item.
			switch list[idx].State {
			case "working", "needs_input":
				used[idx] = false
				continue
			}
		}
		kind := ckRowItem
		if idx >= 0 {
			kind = ckRowAgent
			g = m.ckHoldGroup(agentKey(list[idx]), g, now)
		}
		groups[g] = append(groups[g], ckRow{kind: kind, group: g, agent: idx, item: it, hasItem: true, key: "i:" + it.ID})
	}
	// The Inbox filter narrows to items. Agents join only the unfiltered
	// list, and the f filter keeps the agents that need you.
	kindFilter := st.Filter != ""
	for i, e := range list {
		if used[i] || kindFilter || judges[e.Host+"\x00"+e.WindowID] {
			// A fan's judge is told of on the fan's row, not as a row.
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
		if g != ckNeeds && m.ckArtifactNew(e, true) {
			// A popup the person has not seen in this view waits for them,
			// so its agent rests with the agents that need them.
			g = ckNeeds
		}
		g = m.ckHoldGroup(agentKey(e), g, now)
		groups[g] = append(groups[g], ckRow{kind: ckRowAgent, group: g, agent: i, key: "a:" + agentKey(e)})
	}

	var rows []ckRow
	ck.tall = 0
	for g := range ckGroups {
		items := m.ckGatherFans(groups[g], list)
		itemsOnly := ck.needsOnly || kindFilter
		if len(items) == 0 || itemsOnly && g != ckNeeds && g != ckSnoozed && g != ckReview {
			continue
		}
		n := 0
		for _, r := range items {
			if r.selectable() && (r.hasItem || !itemsOnly) {
				n++
			}
		}
		if n == 0 {
			continue
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
			if itemsOnly && !r.hasItem && r.kind != ckRowNote {
				continue
			}
			if r.selectable() && g != ckNeeds && ck.oneLine && !ck.opened[g] && shown >= ckFoldAt {
				continue
			}
			if r.selectable() {
				shown++
			}
			if r.kind == ckRowFan {
				r.lines = 1
				if m.ckWorktreeOf(list[r.agent]).prompt != "" {
					r.lines = 2
				}
				ck.tall += r.lines
				if ck.oneLine {
					r.lines = 1
				}
				rows = append(rows, r)
				continue
			}
			r.lines = 2
			var e sidebarAgentEntry
			if r.agent >= 0 {
				e = list[r.agent]
			}
			if g == ckIdle || g == ckEarlier || g == ckSnoozed || r.kind == ckRowNote || r.attempt || !m.ckHasLine2(r, e) {
				r.lines = 1
			}
			ck.tall += r.lines
			if ck.oneLine {
				r.lines = 1
			}

			rows = append(rows, r)
			if r.kind == ckRowAgent && !r.hasItem && !r.attempt && !ck.oneLine {
				rows = append(rows, ckChildRows(list[r.agent], g)...)
			}
		}
		if hidden := n - shown; hidden > 0 {
			rows = append(rows, ckRow{kind: ckRowMore, group: g, count: hidden, lines: 1, key: "m:" + strconv.Itoa(g)})
		}
	}
	return rows
}

// ckHoldGroup is the group an agent's row is drawn in: the group its state
// asks for, once it has asked for it for ckHold. A row new to the fleet goes
// straight to its group.
func (m *OS) ckHoldGroup(key string, want int, now time.Time) int {
	ck := &m.Inbox.ck
	if ck.placed == nil {
		ck.placed = make(map[string]ckPlaced)
	}
	p, ok := ck.placed[key]
	switch {
	case !ok:
		p = ckPlaced{shown: want, want: want, since: now}
	case p.want != want:
		p.want, p.since = want, now
	}
	if p.shown != p.want {
		if due := p.since.Add(ckHold); now.Before(due) {
			if ck.holdUntil.IsZero() || due.Before(ck.holdUntil) {
				ck.holdUntil = due
				m.armCockpitHold(due.Sub(now))
			}
		} else {
			p.shown = p.want
		}
	}
	ck.placed[key] = p
	return p.shown
}

// ckHoldMsg wakes the client when a held row's move is due.
type ckHoldMsg struct{}

// armCockpitHold wakes the client after wait, once, for the held move. The
// hold is found while a frame is drawn, after Update has asked what is due,
// so a message brings the client back: one timer, moved, never a poll.
func (m *OS) armCockpitHold(wait time.Duration) {
	p := m.frameRate.program
	if p == nil {
		return
	}
	ck := &m.Inbox.ck
	if ck.holdTimer == nil {
		ck.holdTimer = time.AfterFunc(wait, func() { p.Send(ckHoldMsg{}) })
		return
	}
	ck.holdTimer.Reset(wait)
}

// cockpitHoldDue reports whether a held row's move is due: the tick that
// wakes for it has work, and settles it.
func (m *OS) cockpitHoldDue(now time.Time) bool {
	h := m.Inbox.ck.holdUntil
	return m.ShowInbox && !h.IsZero() && !now.Before(h)
}

// cockpitSettleHold lets a held row move once its hold is due: the rows are
// built again, and the frame drawn again.
func (m *OS) cockpitSettleHold(now time.Time) {
	if m.cockpitHoldDue(now) {
		m.Inbox.ck.holdUntil = time.Time{}
		m.cockpitTouchRows()
	}
}

// ckItemMatches reports whether the search's words match an item's own: who
// it is from, where, and what it says. The selector has narrowed the items
// already (inboxRows).
func (m *OS) ckItemMatches(it session.AttentionItem) bool {
	q := m.Inbox.agents.query
	return q == "" || fuzzy.Match(q, inboxWho(it)+" "+m.inboxWhere(it)+" "+it.Summary)
}

// ckGatherFans puts the attempts of a fan together under one row for the
// fan, at the place of its first attempt: the agents of one group whose
// worktree sessions share a fan's name on one machine. A fan with one attempt
// in the group stays a plain row. An attempt with an item to answer is never
// moved under a fan row: its item stays where the person sees it.
func (m *OS) ckGatherFans(items []ckRow, list []sidebarAgentEntry) []ckRow {
	fanOf := func(r ckRow) string {
		// A finished turn's item is the attempt's own state, so it joins the
		// fan; any other item stays where the person answers it.
		if r.kind != ckRowAgent || r.agent < 0 || r.hasItem && r.item.Kind != session.AttentionFinished {
			return ""
		}
		e := list[r.agent]
		return ckFanKey(e.Host, m.ckWorktreeOf(e))
	}
	count := map[string]int{}
	for _, r := range items {
		if f := fanOf(r); f != "" {
			count[f]++
		}
	}
	gathered := false
	for _, n := range count {
		if n > 1 {
			gathered = true
		}
	}
	if !gathered {
		return items
	}
	out := make([]ckRow, 0, len(items)+len(count))
	done := map[string]bool{}
	for _, r := range items {
		f := fanOf(r)
		if f == "" || count[f] < 2 {
			out = append(out, r)
			continue
		}
		if done[f] {
			continue
		}
		done[f] = true
		var tries []ckRow
		for _, a := range items {
			if fanOf(a) == f {
				a.attempt = true
				tries = append(tries, a)
			}
		}
		slices.SortStableFunc(tries, func(a, b ckRow) int {
			return ckFanOrder(list[a.agent].SessionID, list[b.agent].SessionID)
		})
		tries[len(tries)-1].last = true
		out = append(out, ckRow{kind: ckRowFan, group: r.group, agent: tries[0].agent, count: m.ckFanSize(f), key: "f:" + f})
		out = append(out, tries...)
	}
	return out
}

// ckFanOrder sorts a fan's sessions the way fan numbers them: the stem,
// then -2, -3 and on.
func ckFanOrder(a, b string) int {
	num := func(s string) (string, int) {
		if i := strings.LastIndexByte(s, '-'); i > 0 {
			if n, err := strconv.Atoi(s[i+1:]); err == nil && n > 1 {
				return s[:i], n
			}
		}
		return s, 1
	}
	sa, na := num(a)
	sb, nb := num(b)
	if sa != sb {
		return strings.Compare(a, b)
	}
	return na - nb
}

// ckFanSize is how many sessions belong to the fan f, by ckFanKey.
func (m *OS) ckFanSize(f string) int {
	n := 0
	for k, w := range m.Inbox.ck.branches {
		host, _, _ := strings.Cut(k, "\x00")
		if ckFanKey(host, w) == f {
			n++
		}
	}
	return n
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
	if id := ck.pending; id != "" {
		if i = find(func(r ckRow) bool { return r.key == "i:"+id }); i >= 0 {
			ck.pending = ""
			ck.follow = true
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
	was := ck.key
	m.cockpitSetCursor(i)
	if ck.key != was {
		// The rows moved the cursor to another agent: the reply editor is
		// checked against it after this message (CockpitReplyCheck).
		ck.replyCheck = true
	}
}

// ckNearest is the selectable row nearest idx, -1 when there is none.
func ckNearest(rows []ckRow, idx int) int {
	idx = min(max(idx, 0), len(rows)-1)
	for d := range rows {
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

// cockpitSync points the Inbox's item cursor at the row under the cockpit's
// cursor, and at nothing when the row has no item. The agent functions read
// the row's agent from cockpitAgent. Every key and click calls it first, so the functions the old
// panel used act on the row the person sees.
func (m *OS) cockpitSync() (ckRow, bool) {
	st := &m.Inbox
	r, ok := m.cockpitSelected()
	st.Selected, st.SelectedID = -1, ""
	if !ok {
		return r, false
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
	m.Inbox.ck.pending = ""
	m.cockpitTouch()
	m.cockpitReplyFollow()
	// The detail reads that follow a key ask the Inbox's cursors.
	m.cockpitSync()
}

// cockpitReplyFollow closes the reply editor, keeping its draft, when the
// cursor left the agent it writes to: the area under another agent's thread
// must not send to the one before.
func (m *OS) cockpitReplyFollow() {
	if m.cockpitReplyMismatch() {
		m.InboxReplyCancel()
	}
}

// cockpitReplyMismatch reports whether the reply editor writes to another
// agent than the one under the cursor. Until it closes, the area draws the
// row's own closed area, never the other agent's draft.
func (m *OS) cockpitReplyMismatch() bool {
	ed := m.Inbox.reply.editor
	if ed == nil || len(ed.targets) > 0 {
		return false
	}
	r, ok := m.cockpitSelected()
	if ok && r.hasItem && r.item.Session == ed.session && r.item.Window == ed.window {
		return false
	}
	if e, ok := m.cockpitAgent(); ok && e.SessionID == ed.session && e.WindowID == ed.window {
		return false
	}
	return true
}

// CockpitReplyCheck closes the reply editor, keeping its draft, when the
// rows moved the cursor off its agent. Update asks it after every message:
// one flag when nothing moved.
func (m *OS) CockpitReplyCheck() {
	if m.Inbox.ck.replyCheck {
		m.Inbox.ck.replyCheck = false
		m.cockpitReplyFollow()
	}
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
	m.cockpitTouchRows()
}

// CockpitCycleFilter is f: the whole fleet, then every item alone (the old
// flat Inbox), then each kind of item there is, then the whole fleet again.
func (m *OS) CockpitCycleFilter() {
	st := &m.Inbox
	var kinds []string
	for _, it := range st.Items {
		if k := inboxGroupKey(it.Kind); !slices.Contains(kinds, k) {
			kinds = append(kinds, k)
		}
	}
	switch {
	case !st.ck.needsOnly && st.Filter == "":
		st.ck.needsOnly = true
	case st.Filter == "":
		st.ck.needsOnly = false
		if len(kinds) > 1 {
			st.Filter = kinds[0]
		}
	default:
		i := slices.Index(kinds, st.Filter)
		st.Filter = ""
		if i >= 0 && i+1 < len(kinds) {
			st.Filter = kinds[i+1]
		}
	}
	st.ck.scroll, st.ck.follow = 0, true
	m.cockpitTouchRows()
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
	if _, ok := m.cockpitHeldItem(); !ok {
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
	// A decision the prompt does not offer is refused with a word.
	return m.InboxReplyApproval(decision), true
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
	if ck.img != nil {
		m.cockpitImageClick(x, y)
		return nil, true
	}
	if ck.spawn != nil {
		return m.spawnClick(x, y), true
	}
	if cmd, ok := m.cockpitArtifactMouse(x, y, loom.MouseLeft); ok {
		return cmd, true
	}
	if ck.art.focus {
		// A click anywhere else gives the keys back to the view.
		ck.art.focus = false
		m.cockpitTouch()
	}
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
			if ck.cmp != nil {
				// A row of the fleet leaves the comparison for its agent.
				m.CockpitCompareClose()
			}
			now := time.Now()
			double := ck.lastClickKey == h.key && now.Sub(ck.lastClick) <= ckDoubleClick
			ck.lastClick, ck.lastClickKey = now, h.key
			m.cockpitSelectKey(h.key)
			m.cockpitReplyFollow()
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
		case ckHitMark:
			m.cockpitSelectKey(h.key)
			m.InboxAgentsToggleMark()
			return nil, true
		case ckHitTab:
			m.CockpitTab(h.group - m.cockpitTabIndex())
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
	case "scope":
		m.cockpitNextScope()
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
	case "send":
		return m.InboxReplySend()
	case "review":
		m.cockpitSync()
		cmd, _ := m.InboxReview()
		return cmd
	default:
		if strings.HasPrefix(action, "cmp:") {
			return m.ckCompareAction(action)
		}
		if h, ok := strings.CutPrefix(action, "scope:"); ok {
			if h == m.ckScope() {
				h = ""
			}
			m.CockpitScope(h)
			return nil
		}
		if id, ok := strings.CutPrefix(action, "art:"); ok {
			m.ckArtShow(id, true)
			return nil
		}
		if key, ok := strings.CutPrefix(action, "artrow:"); ok {
			m.cockpitSelectKey(key)
			m.cockpitReplyFollow()
			return m.CockpitArtifactOpen()
		}
		if i, ok := strings.CutPrefix(action, "image:"); ok {
			n, _ := strconv.Atoi(i)
			return m.cockpitOpenImage(n)
		}
		if id, ok := strings.CutPrefix(action, "fold:"); ok {
			if t := m.cockpitThread(); t != nil {
				if t.folded == nil {
					t.folded = map[string]bool{}
				}
				t.folded[id] = !t.folded[id]
				t.gen++
				m.cockpitTouch()
			}
			return nil
		}
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
	if ck.spawn != nil {
		return
	}
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

// CockpitWheel scrolls the fleet when the pointer is over it, and the card's
// detail when it is over the focus area.
func (m *OS) CockpitWheel(x, y int, up bool) {
	ck := &m.Inbox.ck
	if ck.img != nil || ck.spawn != nil {
		// The image view and the spawn sheet cover everything behind
		// them.
		return
	}
	button := loom.WheelDown
	if up {
		button = loom.WheelUp
	}
	if cmd, ok := m.cockpitArtifactMouse(x, y, button); ok {
		m.queueTMLCmd(cmd)
		return
	}
	if x < ck.fleetW {
		if up {
			ck.scroll = max(ck.scroll-3, 0)
		} else {
			ck.scroll += 3
		}
		ck.follow = false
		m.cockpitTouch()
		return
	}
	if t := m.cockpitThread(); t != nil {
		if up {
			t.scroll += 3
		} else {
			t.scroll = max(t.scroll-3, 0)
		}
		m.cockpitTouch()
		return
	}
	m.cockpitSync()
	if _, ok := m.InboxDetailScroll(3 * wheelDelta(up)); ok {
		m.cockpitTouch()
	}
}

// cockpitThread is the kept thread of the agent under the cursor, nil when
// it has none.
func (m *OS) cockpitThread() *agentThread {
	e, ok := m.cockpitAgent()
	if !ok {
		return nil
	}
	return m.Inbox.threads.byKey[agentKey(e)]
}

// cockpitTab is the name of the focus tab in force: Thread, Changes,
// Checkpoints or Pane.
func (m *OS) cockpitTab() string {
	e, _ := m.cockpitAgent()
	r, _ := m.cockpitSelected()
	tabs := m.cockpitTabs(e, r)
	if len(tabs) == 0 {
		return "Thread"
	}
	name, _, _ := strings.Cut(tabs[m.cockpitTabIndex()], " ")
	return name
}

// cockpitTabIndex is the place of the tab in force among the row's tabs.
func (m *OS) cockpitTabIndex() int {
	e, _ := m.cockpitAgent()
	r, _ := m.cockpitSelected()
	for i, t := range m.cockpitTabs(e, r) {
		if name, _, _ := strings.Cut(t, " "); name == m.Inbox.ck.tab {
			return i
		}
	}
	return 0
}

// CockpitTab steps the focus tab by delta.
func (m *OS) CockpitTab(delta int) {
	e, _ := m.cockpitAgent()
	r, _ := m.cockpitSelected()
	tabs := m.cockpitTabs(e, r)
	n := len(tabs)
	if n == 0 {
		return
	}
	i := ((m.cockpitTabIndex()+delta)%n + n) % n
	m.Inbox.ck.tab, _, _ = strings.Cut(tabs[i], " ")
	if t := m.cockpitThread(); t != nil {
		t.scroll = 0
	}
	m.cockpitTouch()
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

// CockpitKey handles the cockpit's own actions ahead of the Inbox's: the
// approval answers, the focus on a narrow screen, help, n, and tab. handled
// is false for any other action.
func (m *OS) CockpitKey(action string) (tea.Cmd, bool) {
	switch action {
	case config.ActionInboxAllow, config.ActionInboxAllowAlways, config.ActionInboxReply:
		letter := "r"
		switch action {
		case config.ActionInboxAllow:
			letter = "a"
		case config.ActionInboxAllowAlways:
			letter = "A"
		}
		if cmd, ok := m.CockpitAnswerLetter(letter); ok {
			return cmd, true
		}
		if action == config.ActionInboxReply {
			return m.cockpitReply(), true
		}
		return nil, true
	case config.ActionInboxOpen:
		if !m.cockpitWideFocus() && !m.Inbox.threads.open {
			return m.CockpitOpenFocus(true), true
		}
		if r, ok := m.cockpitSelected(); ok && m.cockpitGroupHides(r.group) {
			m.CockpitToggleGroup(r.group, true)
		}
		return nil, true
	case config.ActionInboxBack:
		if m.Inbox.threads.open {
			return m.CockpitOpenFocus(false), true
		}
		return nil, true
	case config.ActionInboxHelp:
		m.CloseInbox()
		m.OpenHelpAtCategory(HelpCategoryAgents)
		return nil, true
	case config.ActionInboxTabPrev, config.ActionInboxTabNext:
		d := 1
		if action == config.ActionInboxTabPrev {
			d = -1
		}
		m.CockpitTab(d)
		return nil, true
	case config.ActionInboxExpand:
		if t := m.cockpitThread(); t != nil {
			t.expandAll = !t.expandAll
			t.gen++
			m.cockpitTouch()
		}
		return nil, true
	case config.ActionInboxArtifact:
		return m.CockpitArtifactOpen(), true
	case config.ActionInboxDetailDown, config.ActionInboxDetailUp:
		// J and K scroll a plan's detail first, then the thread. On the
		// Artifacts tab they move through the artifacts.
		d := 1
		if action == config.ActionInboxDetailUp {
			d = -1
		}
		if m.ckArtMove(d) {
			return nil, true
		}
		m.cockpitSync()
		if cmd, ok := m.InboxDetailScroll(d); ok {
			return cmd, true
		}
		t := m.cockpitThread()
		if t == nil || len(t.entries) == 0 {
			return nil, false
		}
		if action == config.ActionInboxDetailUp {
			t.scroll += 3
		} else {
			t.scroll = max(t.scroll-3, 0)
		}
		m.cockpitTouch()
		return nil, true
	case config.ActionInboxNewAgent:
		return m.CockpitNewAgent(), true
	case config.ActionInboxCompare:
		return m.CockpitCompare(), true
	case config.ActionInboxWrite:
		return m.cockpitReply(), true
	case config.ActionInboxFilter:
		m.CockpitCycleFilter()
		return nil, true
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
