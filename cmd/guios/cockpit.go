package main

import (
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
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

// ckAnswerSettle is how long a held approval must have been on screen, as it
// is, before a key or a click answers it (the TUI's inboxAnswerSettle).
const ckAnswerSettle = 400 * time.Millisecond

// ckTypingQuiet is how long after the person typed in the reply box an
// answer key is still taken as typing, not as an answer.
const ckTypingQuiet = 600 * time.Millisecond

// ckRiskWindow is how long the first press of an allow on a risky approval
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
	open bool
	// view is what the card shows: the agents, the board or the mail for
	// the person.
	view int
	// jumpTo is a row to select once the rows are built.
	jumpTo *ckJump

	agents []client.Agent
	items  []session.AttentionItem
	loaded bool
	err    string
	// fetching says a read of the lists runs, and again that an event came
	// during it, so another follows.
	fetching, again bool

	rows   []ckRow
	sel    string // the selected row's key
	selAgt string // its agent's key, so the cursor follows an agent whose row key changed
	hover  string
	placed map[string]ckPlaced
	folded map[int]bool
	opened map[int]bool
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
	// peek is the prompt on the selected pane's screen, as peek-prompt read
	// it, for a prompt no hook holds.
	peek  *ckPeek
	armed ckArmed
	// typedAt is when the person last typed in the reply box.
	typedAt time.Time
	// reason is the open "reject and say why" line, for the item id.
	reason, reasonFor string
	// undo is the last settle, which u takes back for 10 s.
	undo *ckUndo

	// fleetFocus is the fleet's focus binding: the keys act while it holds
	// the focus.
	fleetFocus ui.Handle
	lastClick  time.Time
	lastKey    string

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
	a.ck.fleetFocus.Focus()
	a.refreshCockpit()
}

func (a *App) closeCockpit() {
	a.ck.open = false
	a.ck.reason = ""
	a.focusTerminal()
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
	// A verdict comes with no event of its own: the judge's state and the
	// sessions' changes read the comparison again, never the activity
	// ticks, which come many times a second.
	if ev.Type != "agent-activity" && ev.Type != "transcript" {
		a.compareEvent()
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
	go func() {
		agents, items, err := cl.Agents()
		a.update(func() {
			a.ck.fetching = false
			if err != nil {
				a.ck.err = err.Error()
			} else {
				a.ck.err = ""
				a.ck.agents, a.ck.items, a.ck.loaded = agents, items, true
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

// ckGroupOf is the group an agent's state asks for, as the TUI's fleet.
func ckGroupOf(ag client.Agent, now time.Time) int {
	g := ckIdle
	switch ag.State {
	case "needs_input":
		g = ckNeeds
	case "errored":
		g = ckNeeds
	case "working":
		g = ckWorking
	case "done":
		if ag.FinishedUnread {
			g = ckReview
		} else if ag.StateAt > 0 && now.Sub(time.Unix(0, ag.StateAt)) > ckSettleAfter {
			g = ckSettled
		}
	default:
		if ag.StateAt > 0 && now.Sub(time.Unix(0, ag.StateAt)) > ckSettleAfter {
			g = ckSettled
		}
	}
	if ag.Settled && g != ckNeeds {
		g = ckSettled
	}
	return g
}

// ckHoldGroup is the group an agent's row is drawn in: the group its state
// asks for, once it has asked for it for ckHold. It arms a frame for when a
// held move is due.
func (a *App) ckHoldGroup(c *ui.Context, key string, want int, now time.Time) int {
	ck := &a.ck
	if ck.placed == nil {
		ck.placed = map[string]ckPlaced{}
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
			c.After(due.Sub(now))
		} else {
			p.shown = p.want
		}
	}
	ck.placed[key] = p
	return p.shown
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
			switch ck.agents[idx].State {
			case "working", "needs_input":
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
			g = a.ckHoldGroup(c, ck.agents[idx].Key(), g, now)
		}
		groups[g] = append(groups[g], ckRow{kind: kind, group: g, agent: idx, item: it, key: key})
	}
	for i, ag := range ck.agents {
		if used[i] {
			continue
		}
		g := a.ckHoldGroup(c, ag.Key(), ckGroupOf(ag, now), now)
		groups[g] = append(groups[g], ckRow{kind: ckRowAgent, group: g, agent: i, key: "a:" + ag.Key()})
	}
	var rows []ckRow
	for g := range ckGroups {
		items := groups[g]
		if len(items) == 0 || ck.needsOnly && g != ckNeeds {
			continue
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

// ckStatus is an agent's state as the sidebar's status mark draws it.
func ckStatus(ag client.Agent, it *session.AttentionItem) (proto.Status, bool) {
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
		return proto.Idle, ag.FinishedUnread
	}
	return proto.Idle, false
}
