package main

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/session"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The agent cockpit is the TUI's agents view (internal/app/cockpit*.go) in
// guios: every agent of this machine's daemon grouped by what it needs from
// the person, Needs you first, and the selected agent's work beside them as
// a thread, its changes, checkpoints, mail and the session's task board.
//
// It reads the daemon through the client's cockpit connection (list-agents,
// list-attention, agent-transcript, ...) and answers as the person with the
// presence that connection holds. Nothing polls: the lists are read when the
// cockpit opens and again when the daemon tells of a change (Event "daemon"),
// and only while it is open. The one timer is the row hold, armed with
// c.After only while a row waits to move.

// The fleet's groups, in the order they are drawn, as the TUI's.
const (
	ckNeeds = iota
	ckReview
	ckWorking
	ckIdle
	ckSettled
	ckSnoozed
	ckGroups
)

// ckHold is how long an agent's new group must last before its row moves
// there, so a hook that flips working, needs you, working in a moment does
// not move the row twice (the TUI's ckHold).
const ckHold = 400 * time.Millisecond

// ckSettleAfter is how long an agent rests before it goes to Settled by
// itself.
const ckSettleAfter = time.Hour

// ckFoldAt is how many rows a group keeps before the rest fold into a "more"
// row. Needs you never folds.
const ckFoldAt = 8

// ckNewFlash is how long a new prompt's card edge shows the accent. No time
// guards an answer: a letter never answers (cockpit_guard.go), a risky call
// asks first, and an answer goes only to the prompt the card drew.
const ckNewFlash = 400 * time.Millisecond

// ckRiskWindow is how long the first click of an allow on a risky approval
// waits for the second (the TUI's inboxRiskPressWindow).
const ckRiskWindow = 3 * time.Second

type ckRowKind uint8

const (
	ckRowHead ckRowKind = iota
	ckRowAgent
	ckRowItem
	ckRowMore
)

// ckRow is one row of the fleet.
type ckRow struct {
	kind  ckRowKind
	group int
	count int
	agent int // index in cockpit.agents, -1 for none
	item  *session.AttentionItem
	key   string
	// folded marks the heading of a folded group.
	folded bool
}

// ckPlaced is the group an agent's row is in, and the group its state asks
// for since when.
type ckPlaced struct {
	shown, want int
	since       time.Time
	// st and unread are the state the row showed in its shown group: while
	// a move waits (ckHold), every view draws that state, so the row, its
	// group, the counts, the badge and the rail say one thing.
	st     proto.Status
	unread bool
}

// ckShown is the held approval the card last drew, and since when.
type ckShown struct {
	key   string
	since time.Time
}

// ckArmed is the first press of an allow on a risky approval.
type ckArmed struct {
	decision, item string
	at             time.Time
}

// cockpit is the agent cockpit's state.
type cockpit struct {
	// cardShown is set while the focus card shows an approval card, for
	// the views drawn above it this frame.
	cardShown bool
	open      bool
	// edit is the body of the held edit the card shows (cockpit_edit.go).
	edit *ckEdit
	// editScroll is where the edit's body is scrolled to, and cardView
	// the approval card's viewport in the last frame (ckEditBlock).
	editScroll ui.ScrollState
	cardView   ui.Rect
	// tabH is the tab's height above the card in the last frame: with the
	// card's, the room the card may grow into (ckEditBlock).
	tabH float32
	// tabMin is the least height the tab keeps in this frame, and
	// cardH the approval card's whole height in the last frame, which
	// with the body's gives the room around the body (ckEditBlock).
	tabMin, cardH float32
	// gapH is the room under the card in the last frame, which an empty
	// thread leaves there rather than above the card (cardUnder).
	gapH float32
	// editBoxW is the held edit's code block's width in the last frame,
	// which its rows wrap to.
	editBoxW float32
	// manyHarness is set while the list's agents use more than one
	// harness, so a row names its harness in words.
	manyHarness bool
	// keptEdits is the body of each held edit the card drew, by its prompt
	// key, for the card once its hold ends (ckEndedEdit).
	keptEdits map[string]*integration.EditBody
	// editLast is the body's and the card's bounds when the body last
	// counted its rows.
	editLast [2]ui.Rect
	// view is what the card shows: the agents, the board or the mail for
	// the person.
	view int
	// jumpTo is a row to select once the rows are built.
	jumpTo *ckJump
	// focusView asks the view just shown to take the keys.
	focusView bool
	// zoomed is the session and pane goToAgent zoomed, to show the layout
	// again when the agents view opens.
	zoomed string
	// advance is the agent the person just answered from a Needs you
	// row, and when: once its row has moved, the next one is selected.
	advance   string
	advanceAt time.Time
	// fleetRect is the agents list's box in the last frame, and cardRect
	// the whole card's, for toasts.
	fleetRect, cardRect ui.Rect
	// lightbox is an image of a thread shown whole.
	lightbox *ckThreadImage

	agents []client.Agent
	// letters are the fans' attempts' letters by session (ckFanLetters).
	letters map[string]string
	items   []session.AttentionItem
	loaded  bool
	err     string
	// fetching says a read of the lists runs, and again that an event came
	// during it, so another follows.
	fetching, again bool

	rows   []ckRow
	sel    string // the selected row's key
	selAgt string // its agent's key, so the cursor follows an agent whose row key changed
	placed map[string]ckPlaced
	// agentsAt is when the agents list last came from the daemon: a row
	// held on its way to a new group counts from it (ckHoldGroup).
	agentsAt time.Time
	// pushed is what the last state push said of each local agent pane,
	// pushedAt when it came: laid on the agents' rows (followPanes).
	pushed   map[string]ckPushed
	pushedAt time.Time
	folded   map[int]bool
	opened   map[int]bool
	// needsOnly is the f filter: the Needs you group alone.
	needsOnly bool
	// tab is the focus area's tab; picked the one the person chose last,
	// and chosen the one they chose for each agent, by its key.
	tab    string
	picked string
	chosen map[string]string
	// stacked is a card too narrow for both columns, which then shows one
	// at a time: the agents, or with stackFocus the chosen agent.
	stacked, stackFocus bool
	// narrow is a card too narrow for what the cockpit shows twice.
	narrow bool
	// fleetW is the agents column's width this frame, for its rows' plans;
	// focusW the focus column's in the last frame, for the thread's.
	fleetW, focusW float32

	shown ckShown
	// cardKey is the key of the approval card this frame drew, "" for
	// none; promptToasts are the toasts about a card, by the card's key
	// (ckPromptToast).
	cardKey      string
	promptToasts map[string]string
	// wasHeld are the prompts the card drew held (ckHoldEnded).
	wasHeld map[string]bool
	// peek is the prompt on the selected pane's screen, as peek-prompt read
	// it, for a prompt no hook holds.
	peek  *ckPeek
	armed ckArmed
	// question is the open confirm step of a list letter (cockpit_ask.go),
	// and confirmed holds that its Enter is answering now.
	question  *ckQuestion
	confirmed bool
	// answered holds the items the person answered that the daemon may
	// still list, by item id, with their agent's key and when: they stay
	// out of the list (ckAnswered). placedFree lets those agents leave
	// Needs you without the row hold.
	answered   map[string]ckAnsweredItem
	placedFree map[string]bool
	// nothingAt is when a chord found nothing to answer: the key line says
	// so for 1.5 s.
	nothingAt       time.Time
	nothingQuestion bool
	// nothingOff says the chord came to a card that approvals being off
	// leaves with no Allow.
	nothingOff bool
	// reason is the open "reject and say why" line, for the item id.
	reason, reasonFor string
	// undo is the last settle, which u takes back for 10 s.
	undo *ckUndo

	// fleetFocus is the fleet's focus binding: the keys act while it holds
	// the focus.
	fleetFocus ui.Handle

	threads map[string]*ckThread
	reply   ckReply
	tabs    ckTabs

	// spawn is the open New agent sheet, spawnDraft a closed one kept for
	// the next, spawnGen counts the sheets opened, spawnLast is the agent
	// the last start used, and spawnUI holds its fields' focus.
	spawn, spawnDraft *spawnSheet
	spawnGen          uint64
	spawnLast         string
	spawnUI           spawnHandles
	// selectWhenSeen is the key of an agent just started: the list selects
	// its row once it shows.
	selectWhenSeen string
	// selectName is the name of an agent being started, which the list
	// selects as soon as its pane shows; spawnTasks the task of each, by
	// name, which its row says until the agent reports.
	selectName string
	spawnTasks map[string]string
	// cmp is the open comparison of a fan's attempts.
	cmp *ckCompare
	// newHover is the sidebar's New agent button under the pointer, so a
	// click on it does not also toggle the agents view.
	newHover bool
}

// ckUndo is a settle the person can take back.
type ckUndo struct {
	agent client.Agent
	id    string // the item settle returned, for unsettle
	until time.Time
}

// cockpitClient is this machine's client, nil while it is not connected.
func (a *App) cockpitClient() *client.Client {
	if h := a.local(); h != nil && h.conn == connReady {
		return h.cl
	}
	return nil
}

// toggleCockpit opens or closes the agent cockpit.
func (a *App) toggleCockpit() {
	if a.ck.open {
		a.closeCockpit()
		return
	}
	a.ck.open = true
	a.moveKeys("view")
	a.reportView()
	a.refreshCockpit()
	// A pane the cockpit zoomed to open it goes back into the layout.
	if sid, pane, ok := strings.Cut(a.ck.zoomed, "\x00"); ok {
		a.ck.zoomed = ""
		if s := a.session(sid); s != nil && s.Zoomed == pane {
			a.zoomPane(s, pane)
		}
	}
}

func (a *App) closeCockpit() {
	a.ck.open = false
	a.ck.reason = ""
	a.moveKeys("view")
	a.reportView()
}

// cockpitEvent takes a daemon event: the lists are read again, open or not,
// since the badge and the keys to what needs you read them; while the
// cockpit is open, the selected agent's thread too when its transcript grew.
func (a *App) cockpitEvent(ev *proto.DaemonEvent) {
	if ev == nil {
		return
	}
	if !a.ck.open {
		switch ev.Type {
		case "agent-state", "attention", "agent-message", "window-closed", "window-created", "agent-seen":
			a.refreshCockpit()
		}
		return
	}
	switch ev.Type {
	case "fan-verdict":
		a.compareEvent(ev.Session)
		return
	case "agent-state":
		// A daemon from before fan-verdict: the judge's turn ending, or an
		// attempt's, reads the comparison again.
		a.compareEvent(ev.Session)
	}
	switch ev.Type {
	case "transcript":
		if ag, ok := a.ckSelectedAgent(); ok && ag.Window == ev.Window {
			a.fetchThread(ag, false)
		}
		return
	case "agent-message":
		a.ckMailChanged(ev)
	case "task-claimed", "task-finished", "task-changed":
		a.ckTasksChanged()
		return
	case "agent-activity":
		return
	case "agent-state":
		// A queued message is typed at a rest: the queue shown changes.
		if ag, ok := a.ckSelectedAgent(); ok && ag.Window == ev.Window {
			a.fetchQueued(ag)
			// A report can join the pane to its transcript: a thread
			// that had none, or did not load, reads again.
			if th := a.ck.threads[threadKey(ag)]; th != nil && th.err != "" {
				a.fetchThread(ag, true)
			}
		}
	}
	a.refreshCockpit()
}

// refreshCockpit reads the agents and the Inbox again, once at a time.
func (a *App) refreshCockpit() {
	cl := a.cockpitClient()
	if cl == nil {
		return
	}
	if a.ck.fetching {
		a.ck.again = true
		return
	}
	a.ck.fetching = true
	began := time.Now()
	go func() {
		agents, items, err := cl.Agents()
		a.update(func() {
			a.ck.fetching = false
			if err != nil {
				a.ck.err = err.Error()
			} else {
				a.ck.err = ""
				a.ck.agents, a.ck.items, a.ck.loaded = agents, items, true
				a.ck.agentsAt = time.Now()
				a.ck.ckRefetched(began)
				a.shareAgentStates()
				a.ckKeepAnswered()
				a.ck.letters = ckFanLetters(agents)
				if a.ck.undo != nil && time.Now().After(a.ck.undo.until) {
					a.ck.undo = nil
				}
			}
			if a.ck.again {
				a.ck.again = false
				a.refreshCockpit()
			}
		})
	}()
}

// ckShownGroup is the group an agent's row is in, from the state it shows
// (ckStatus) with the item that stands for it: the section and the
// header's counts say what the row's mark and the pill say.
func ckShownGroup(ag client.Agent, it *session.AttentionItem, now time.Time) int {
	st, unread := ckStatus(ag, it)
	g := ckIdle
	switch {
	case st == proto.Waiting || st == proto.Failed:
		g = ckNeeds
	case st == proto.Working:
		g = ckWorking
	case unread:
		g = ckReview
	case ag.StateAt > 0 && now.Sub(time.Unix(0, ag.StateAt)) > ckSettleAfter:
		g = ckSettled
	}
	if ag.Settled && g != ckNeeds {
		g = ckSettled
	}
	return g
}

// ckItemOf is the Inbox item that stands for an agent, as its row in the
// agents view has it: its first one, nil for none.
func (a *App) ckItemOf(ag client.Agent) *session.AttentionItem {
	if ag.Host != "" {
		return nil
	}
	for i := range a.ck.items {
		it := &a.ck.items[i]
		if it.Kind != session.AttentionOutbox && it.Host == "" && it.Session == ag.Session && it.Window == ag.Window {
			return it
		}
	}
	return nil
}

// ckHoldGroup is the group an agent's row is drawn in: the group its state
// asks for, once it has asked for it for ckHold. It arms a frame for when a
// held move is due.
func (a *App) ckHoldGroup(c *ui.Context, key string, want int, st proto.Status, unread bool, now time.Time) int {
	ck := &a.ck
	if ck.placed == nil {
		ck.placed = map[string]ckPlaced{}
	}
	p, ok := ck.placed[key]
	switch {
	case !ok:
		p = ckPlaced{shown: want, want: want, since: now}
	case p.want != want:
		p.want, p.since = want, ck.moveSince(p, want, now)
	}
	if p.shown == ckNeeds && p.want != ckNeeds && ck.placedFree[key] {
		// The person just answered it: out of Needs you in this frame, so
		// the next fast answer finds the next agent (council round 5, S1).
		p.shown = p.want
	}
	if p.shown != p.want && p.want == ckNeeds {
		// Into Needs you at once: the badge counts the agent from the
		// moment it needs the person, and the row must say so too.
		p.shown = ckNeeds
	}
	if p.shown != p.want {
		if due := p.since.Add(ckHold); now.Before(due) {
			c.After(due.Sub(now))
		} else {
			p.shown = p.want
		}
	}
	if p.shown != ckNeeds {
		delete(ck.placedFree, key)
	}
	if p.shown == p.want {
		p.st, p.unread = st, unread
	}
	ck.placed[key] = p
	return p.shown
}

// moveSince is when the hold of a row's move to group want starts. The hold
// counts from when the data that moves the row came, not from the frame
// that saw it: a window that builds frames only on input showed the old
// state after it was long gone (Mac round 5, workflow S1).
func (ck *cockpit) moveSince(p ckPlaced, want int, now time.Time) time.Time {
	if p.want == want {
		return p.since
	}
	if at := ck.agentsAt; at.After(p.since) && at.Before(now) {
		return at
	}
	return now
}

// ckSilent reports whether ag stopped reporting while it worked: the daemon
// shows it as unknown with the reason silent, and its last report was
// working.
func ckSilent(ag client.Agent) bool { return ag.State == "unknown" && ag.Reason == "silent" }

// ckSilentFor is how long a silent agent has sent no report, as line 2
// says it: since its last report, which said working.
func ckSilentFor(ag client.Agent, now time.Time) string {
	return workedFor(now.Sub(time.Unix(0, ag.StateAt)).Milliseconds())
}

// ckShownStatus is the state every view draws for ag, with it its item:
// its state, or while its row's move to a new group waits, the state of
// the group it is still shown in (ckHoldGroup).
func (a *App) ckShownStatus(ag client.Agent, it *session.AttentionItem) (proto.Status, bool) {
	p, ok := a.ck.placed[ag.Key()]
	if !ok || p.st == "" || !a.ck.open {
		// The view is shut: no row waits to move.
		return ckStatus(ag, it)
	}
	// The rail draws before the view places its rows: a move the view will
	// start in this frame holds already, until the view would place it, so
	// the rail and the list show one state in a frame.
	now := time.Now()
	want := ckShownGroup(ag, it, now)
	if p.shown == ckNeeds && want != ckNeeds && a.ck.placedFree[ag.Key()] {
		// Just answered: out of Needs you in this frame, as its row
		// (ckHoldGroup).
		return ckStatus(ag, it)
	}
	if (p.shown != p.want || want != p.want && want != ckNeeds) && now.Before(a.ck.moveSince(p, want, now).Add(ckHold)) {
		return p.st, p.unread
	}
	return ckStatus(ag, it)
}

// buildCockpitRows groups the agents and the Inbox's items into rows.
func (a *App) buildCockpitRows(c *ui.Context) []ckRow {
	ck := &a.ck
	now := c.Now()
	var groups [ckGroups][]ckRow
	byKey := make(map[string]int, len(ck.agents))
	for i, ag := range ck.agents {
		byKey[ag.Session+"\x00"+ag.Window] = i
	}
	used := map[int]bool{}
	// An agent that needs the person is in Needs you, whatever its first
	// item is: the badge counts it from the same list (cockpit_needs.go).
	needing := ck.needAgents()
	for i := range ck.items {
		it := &ck.items[i]
		g := ckNeeds
		switch {
		case it.SnoozedUntil != 0:
			g = ckSnoozed
		case it.Kind == session.AttentionFinished:
			g = ckReview
		case it.Kind == session.AttentionOutbox:
			continue
		}
		idx := -1
		if it.Window != "" && it.Host == "" {
			if j, ok := byKey[it.Session+"\x00"+it.Window]; ok {
				idx = j
			}
		}
		if g == ckReview && idx >= 0 && needing[idx] {
			continue
		}
		if g == ckReview && idx >= 0 {
			if st, unread := ckStatus(ck.agents[idx], it); !unread || st != proto.Idle {
				// It went on: its row is the agent's, by its state.
				continue
			}
		}
		if idx >= 0 && used[idx] {
			// One row per agent: its first item stands for it.
			continue
		}
		kind := ckRowItem
		key := "i:" + it.ID
		if idx >= 0 {
			used[idx] = true
			kind = ckRowAgent
			st, unread := ckStatus(ck.agents[idx], it)
			g = a.ckHoldGroup(c, ck.agents[idx].Key(), g, st, unread, now)
		}
		groups[g] = append(groups[g], ckRow{kind: kind, group: g, agent: idx, item: it, key: key})
	}
	for i, ag := range ck.agents {
		if used[i] {
			continue
		}
		st, unread := ckStatus(ag, nil)
		g := a.ckHoldGroup(c, ag.Key(), ckShownGroup(ag, nil, now), st, unread, now)
		groups[g] = append(groups[g], ckRow{kind: ckRowAgent, group: g, agent: i, key: "a:" + ag.Key()})
	}
	var rows []ckRow
	for g := range ckGroups {
		items := groups[g]
		if len(items) == 0 || ck.needsOnly && g != ckNeeds {
			continue
		}
		if g == ckNeeds {
			// The order of ck.needs, which the rail and the next-agent
			// keys follow too: one "next" in every place (Mac round 1,
			// workflow S3).
			rank := map[string]int{}
			for n, nd := range ck.needs() {
				switch {
				case nd.agent >= 0:
					rank["a"+strconv.Itoa(nd.agent)] = n
				case nd.item != nil:
					rank["i"+nd.item.ID] = n
				}
			}
			at := func(r ckRow) int {
				k := "a" + strconv.Itoa(r.agent)
				if r.agent < 0 && r.item != nil {
					k = "i" + r.item.ID
				}
				if n, ok := rank[k]; ok {
					return n
				}
				return len(rank)
			}
			sort.SliceStable(items, func(i, j int) bool { return at(items[i]) < at(items[j]) })
		}
		if g != ckNeeds && g != ckSnoozed {
			// The oldest wait first, as the TUI's: by the time the state
			// came, and then by name for a stable order.
			sort.SliceStable(items, func(i, j int) bool {
				ai, aj := ck.rowAgent(items[i]), ck.rowAgent(items[j])
				if ai.StateAt != aj.StateAt {
					return ai.StateAt < aj.StateAt
				}
				return ai.Name < aj.Name
			})
		}
		head := ckRow{kind: ckRowHead, group: g, agent: -1, count: len(items), key: "h:" + strconv.Itoa(g)}
		if ck.folded[g] {
			head.folded = true
			rows = append(rows, head)
			continue
		}
		rows = append(rows, head)
		for n, r := range items {
			if g != ckNeeds && n >= ckFoldAt && !ck.opened[g] {
				rows = append(rows, ckRow{kind: ckRowMore, group: g, agent: -1, count: len(items) - n, key: "m:" + strconv.Itoa(g)})
				break
			}
			rows = append(rows, r)
		}
	}
	return rows
}

// rowAgent is a row's agent, zero for none.
func (ck *cockpit) rowAgent(r ckRow) client.Agent {
	if r.agent >= 0 && r.agent < len(ck.agents) {
		return ck.agents[r.agent]
	}
	return client.Agent{}
}

// selectable reports whether the keys act on the row.
func (r ckRow) selectable() bool {
	return r.kind == ckRowAgent || r.kind == ckRowItem || r.kind == ckRowHead && r.folded
}

// ckClamp keeps the selection on a row that exists: the same row, else the
// row of the same agent, else the first row that needs the person.
func (a *App) ckClamp() {
	ck := &a.ck
	if n := ck.selectName; n != "" {
		for _, r := range ck.rows {
			if r.agent >= 0 && ck.agents[r.agent].Name == n && ck.agents[r.agent].Host == "" {
				ck.selectName = ""
				a.ckSelect(r)
				return
			}
		}
	}
	if k := ck.selectWhenSeen; k != "" {
		for _, r := range ck.rows {
			if r.agent >= 0 && ck.agents[r.agent].Key() == k {
				ck.selectWhenSeen = ""
				a.ckSelect(r)
				return
			}
		}
	}
	if i := ck.index(ck.sel); i >= 0 {
		return
	}
	for _, r := range ck.rows {
		if r.agent >= 0 && ck.agents[r.agent].Key() == ck.selAgt && ck.selAgt != "" {
			ck.sel = r.key
			return
		}
	}
	for _, r := range ck.rows {
		if r.kind == ckRowAgent || r.kind == ckRowItem {
			a.ckSelect(r)
			return
		}
	}
	ck.sel, ck.selAgt = "", ""
}

func (ck *cockpit) index(key string) int {
	if key == "" {
		return -1
	}
	return slices.IndexFunc(ck.rows, func(r ckRow) bool { return r.key == key })
}

// ckSelect puts the cursor on a row.
func (a *App) ckSelect(r ckRow) {
	ck := &a.ck
	if ck.sel == r.key {
		return
	}
	ck.sel = r.key
	ck.selAgt = ""
	ck.reason = ""
	ck.armed = ckArmed{}
	ck.question = nil
	if r.agent >= 0 {
		ag := ck.agents[r.agent]
		ck.selAgt = ag.Key()
		a.fetchThread(ag, true)
	}
	if t := a.ckTabFor(r); t != ck.tab {
		ck.tab = t
		a.ckTabOpened()
	}
}

// ckMove moves the cursor by d selectable rows.
func (a *App) ckMove(d int) {
	ck := &a.ck
	i := ck.index(ck.sel)
	for j := i + d; j >= 0 && j < len(ck.rows); j += d {
		if ck.rows[j].selectable() {
			a.ckSelect(ck.rows[j])
			return
		}
	}
}

// ckSelected is the row under the cursor.
func (a *App) ckSelected() (ckRow, bool) {
	ck := &a.ck
	if i := ck.index(ck.sel); i >= 0 {
		return ck.rows[i], true
	}
	return ckRow{}, false
}

// ckOnAgent reports whether the cursor is on an agent's row or an Inbox
// item's, which the focus area shows.
func (a *App) ckOnAgent() bool {
	r, ok := a.ckSelected()
	return ok && r.kind != ckRowHead && r.kind != ckRowMore
}

// ckSelectedAgent is the agent under the cursor.
func (a *App) ckSelectedAgent() (client.Agent, bool) {
	r, ok := a.ckSelected()
	if !ok || r.agent < 0 {
		return client.Agent{}, false
	}
	return a.ck.agents[r.agent], true
}

// goToAgent shows the agent's pane and closes the cockpit.
func (a *App) goToAgent(ag client.Agent) {
	var s *proto.Session
	for i := range a.st.Sessions {
		if t := &a.st.Sessions[i]; (t.Name == ag.Session || t.ID == ag.Session) && !a.hostOf(t.ID).remote() {
			s = t
		}
	}
	if s == nil {
		a.toast(locales.CockpitNoPane())
		return
	}
	sid := s.ID
	if ag.Workspace > 0 && ag.Workspace != s.Workspace {
		a.call(sid, "workspace", proto.WorkspaceParams{Session: sid, Workspace: ag.Workspace}, nil)
	}
	a.focus[sid] = ag.Window
	a.selectSession(sid)
	a.ck.open = false
	a.reportView()
	// A tile too small to read opens zoomed; the agents key shows the
	// layout again with the agents (toggleCockpit).
	// The size is the terminal's grid here: the daemon's is its own.
	cols, rows := 0, 0
	if t := a.termFor(ag.Window); t != nil {
		cols, rows = t.Size()
	}
	if s.PaneByID(ag.Window) != nil && len(s.Panes) > 1 && s.Zoomed != ag.Window && cols > 0 && (cols < 80 || rows < 24) {
		a.zoomPane(s, ag.Window)
		a.ck.zoomed = sid + "\x00" + ag.Window
	}
	a.focusTerminal()
}

// ckSettle puts the selected agent on the Settled shelf, or takes it off.
func (a *App) ckSettle() {
	ag, ok := a.ckSelectedAgent()
	cl := a.cockpitClient()
	if !ok || cl == nil {
		a.toast(locales.CockpitSettleOnlyAgent())
		return
	}
	action := "settle"
	if ag.Settled {
		action = "unsettle"
	}
	go func() {
		var out struct {
			ID string `json:"id"`
		}
		err := cl.VerbInto("mark-attention", map[string]any{"session": ag.Session, "window": ag.Window, "action": action}, true, &out)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
				return
			}
			if action == "settle" {
				a.ck.undo = &ckUndo{agent: ag, id: out.ID, until: time.Now().Add(10 * time.Second)}
				a.toast(locales.CockpitSettled(ag.Name))
			}
			a.refreshCockpit()
		})
	}()
}

// ckUndoSettle takes back the last settle, within 10 s.
func (a *App) ckUndoSettle() {
	u := a.ck.undo
	cl := a.cockpitClient()
	if u == nil || time.Now().After(u.until) || cl == nil {
		a.toast(locales.CockpitNothingToUndo())
		return
	}
	a.ck.undo = nil
	go func() {
		p := map[string]any{"session": u.agent.Session, "window": u.agent.Window, "action": "unsettle"}
		if u.id != "" {
			p["id"] = u.id
		}
		_, err := cl.Verb("mark-attention", p, true)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
			}
			a.refreshCockpit()
		})
	}()
}

// ckSnooze hides the selected item until its agent's state changes.
func (a *App) ckSnooze() {
	r, ok := a.ckSelected()
	cl := a.cockpitClient()
	if !ok || r.item == nil || cl == nil {
		a.toast(locales.CockpitSnoozeOnlyItem())
		return
	}
	it := *r.item
	action, p := "snooze", map[string]any{"id": it.ID}
	if it.SnoozedUntil != 0 {
		action = "wake"
	} else if it.Window != "" {
		p["until_change"] = true
	} else {
		p["for_ms"] = int64(time.Hour / time.Millisecond)
	}
	p["action"] = action
	go func() {
		_, err := cl.Verb("mark-attention", p, true)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
			}
			a.refreshCockpit()
		})
	}()
}

// ckDismiss closes the selected Inbox item.
func (a *App) ckDismiss() {
	r, ok := a.ckSelected()
	cl := a.cockpitClient()
	if !ok || r.item == nil || cl == nil {
		return
	}
	id := r.item.ID
	go func() {
		_, err := cl.Verb("dismiss-attention", map[string]any{"id": id}, true)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
			}
			a.refreshCockpit()
		})
	}()
}

// ckToggleFold folds or opens a group.
func (a *App) ckToggleFold(g int) {
	ck := &a.ck
	if ck.folded == nil {
		ck.folded = map[int]bool{}
	}
	ck.folded[g] = !ck.folded[g]
}

// ckOpenMore shows every row of a group that folded its long tail.
func (a *App) ckOpenMore(g int) {
	if a.ck.opened == nil {
		a.ck.opened = map[int]bool{}
	}
	a.ck.opened[g] = true
}

// ckStatus is the one state an agent shows, with it the item that stands
// for it (ckItemOf): every view reads it, the row's mark, the state pill,
// the rail's mark, the pane's mark and, through ckShownGroup, the section
// and the header's counts (Mac round 4, workflow M2). A state the daemon
// no longer knows (unknown, after a report aged out) is idle everywhere.
func ckStatus(ag client.Agent, it *session.AttentionItem) (proto.Status, bool) {
	if it != nil && it.Kind == session.AttentionFinished && (ag.State == "working" || ag.State == "needs_input") {
		// The agent went on after it finished: the item no longer says
		// what it does, as its row's group says too.
		it = nil
	}
	switch {
	case it != nil && it.Kind == session.AttentionFinished:
		return proto.Idle, true
	case it != nil && it.Kind == session.AttentionErrored:
		return proto.Failed, true
	case it != nil && it.Kind != session.AttentionMail && it.Kind != session.AttentionResume:
		return proto.Waiting, true
	}
	switch ag.State {
	case "working":
		return proto.Working, false
	case "needs_input":
		return proto.Waiting, true
	case "errored":
		return proto.Failed, true
	case "done":
		if ag.FinishedUnread {
			return proto.Idle, true
		}
		return statusDone, false
	}
	return proto.Idle, false
}

// ckAdvanceAfter notes an answer to an agent on a Needs you row: once its
// row leaves Needs you, the cursor moves to the next row there.
func (a *App) ckAdvanceAfter(ag client.Agent) {
	r, ok := a.ckSelected()
	if !ok || r.group != ckNeeds || ag.Key() != a.ck.selAgt {
		return
	}
	a.ck.advance, a.ck.advanceAt = ag.Key(), time.Now()
}

// ckTakeAdvance moves the cursor to the next Needs you row once the
// answered agent's row has left the group, after the row hold. With none
// left, the cursor stays.
func (a *App) ckTakeAdvance(c *ui.Context) {
	ck := &a.ck
	if ck.advance == "" {
		return
	}
	now := c.Now()
	if due := ck.advanceAt.Add(ckHold + 50*time.Millisecond); now.Before(due) {
		c.After(due.Sub(now))
		return
	}
	if now.Sub(ck.advanceAt) > 5*time.Second || ck.selAgt != ck.advance {
		// The person moved on, or the row never left.
		ck.advance = ""
		return
	}
	r, ok := a.ckSelected()
	if ok && r.group == ckNeeds {
		// Still waiting: the refresh has not come yet.
		c.After(100 * time.Millisecond)
		return
	}
	ck.advance = ""
	for _, n := range ck.rows {
		if n.group == ckNeeds && (n.kind == ckRowAgent || n.kind == ckRowItem) {
			a.ckSelect(n)
			return
		}
	}
}

// ckAnswered takes the answered item out of the list at once, moves its
// agent out of Needs you, and puts the cursor on the next agent that needs
// the person, all in this frame: a fast run of answers finds the next card
// each time (council round 5, S1). The daemon's refresh confirms it.
func (a *App) ckAnswered(it session.AttentionItem, ag client.Agent) {
	ck := &a.ck
	key := ag.Key()
	// The next row that needs the person after this one, else before it.
	var after, before *ckRow
	past := false
	for i := range ck.rows {
		r := &ck.rows[i]
		if r.key == ck.sel {
			past = true
			continue
		}
		if r.group != ckNeeds || !r.selectable() || r.agent >= 0 && ck.agents[r.agent].Key() == key {
			continue
		}
		switch {
		case past && after == nil:
			after = r
		case !past && before == nil:
			before = r
		}
	}
	next := after
	if next == nil {
		next = before
	}
	var nextRow ckRow
	if next != nil {
		nextRow = *next
	}
	if ck.answered == nil {
		ck.answered = map[string]ckAnsweredItem{}
	}
	ck.answered[it.ID] = ckAnsweredItem{agent: key, at: time.Now()}
	a.ckKeepAnswered()
	ck.advance = ""
	if next != nil {
		a.ckSelect(nextRow)
	}
}

// ckAnsweredItem is an item the person answered.
type ckAnsweredItem struct {
	agent string
	at    time.Time
}

// ckKeepAnswered keeps the answered items out of the list, and their
// agents out of Needs you, until the daemon stops listing them, for at
// most 10 s. A new prompt of the same agent has its own id: it shows.
func (a *App) ckKeepAnswered() {
	ck := &a.ck
	if len(ck.answered) == 0 {
		return
	}
	listed := map[string]bool{}
	for _, it := range ck.items {
		listed[it.ID] = true
	}
	for id, x := range ck.answered {
		if !listed[id] || time.Since(x.at) > 10*time.Second {
			delete(ck.answered, id)
		}
	}
	ck.items = slices.DeleteFunc(ck.items, func(x session.AttentionItem) bool {
		_, gone := ck.answered[x.ID]
		return gone
	})
	still := map[string]bool{}
	for _, it := range ck.items {
		switch it.Kind {
		case session.AttentionApproval, session.AttentionPlan, session.AttentionQuestion, session.AttentionAsk:
			still[client.Agent{Host: it.Host, Session: it.Session, Window: it.Window}.Key()] = true
		}
	}
	if ck.placedFree == nil {
		ck.placedFree = map[string]bool{}
	}
	for _, x := range ck.answered {
		ck.placedFree[x.agent] = true
		if still[x.agent] {
			continue
		}
		for i := range ck.agents {
			if ck.agents[i].Key() == x.agent && ck.agents[i].State == "needs_input" {
				ck.agents[i].State = "working"
			}
		}
	}
}
