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
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
	"tuios.dev/loom"
)

const (
	ckNeeds = iota
	ckReview
	ckWorking
	ckIdle
	ckSettled
	ckSnoozed
	ckGroups
)

var ckGroupTitles = [ckGroups]string{"Needs you", "Ready for review", "Working", "Idle", "Settled", "Snoozed"}

const ckSettleAfter = time.Hour

const ckFoldAt = 8

const ckChildMax = 3

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
	ckRowCard
	ckRowBand
)

type ckRow struct {
	kind    ckRowKind
	group   int
	count   int
	agent   int
	card    int
	item    session.AttentionItem
	hasItem bool
	child   sessiontree.Subagent
	last    bool
	attempt bool
	note    string
	key     string
	lines   int
	folded  bool
	hidden  []ckRow
	title   string
	rest    bool
	inline  bool
}

func (r ckRow) selectable() bool {
	return r.kind == ckRowAgent || r.kind == ckRowItem || r.kind == ckRowCard
}

func (r ckRow) cursorable() bool {
	return r.selectable() || r.kind == ckRowHead && (r.folded || r.rest)
}

func (ck *cockpitState) cursorOn(r ckRow, list []sidebarAgentEntry) bool {
	if ck.key != "" && r.key == ck.key {
		return true
	}
	return ck.akey != "" && r.agent >= 0 && r.agent < len(list) && agentKey(list[r.agent]) == ck.akey
}

type cockpitState struct {
	hostPal                      ckHostPal
	region                       ckRegion
	regionBox                    [ckRegions]ckRegionBox
	listWidth                    int
	sideHidden                   bool
	groupBy                      string
	menu                         *ckMenu
	anchors                      map[string]ckAnchor
	ship                         *ckShip
	zoom                         bool
	zoomTook                     bool
	div                          ckDivider
	key, akey                    string
	idx                          int
	hover                        string
	scroll                       int
	follow                       bool
	folded, opened, foldSet      [ckGroups]bool
	pins                         map[string]bool
	needsOnly                    bool
	chip                         int
	groupN                       [ckGroups]int
	colH                         int
	oneLine                      bool
	agents                       []sidebarAgentEntry
	branches                     map[string]ckWorktree
	branchesAt                   uint64
	machines                     map[string]int
	tall                         int
	rows                         []ckRow
	rowsAt                       ckRowsKey
	hits                         []ckHit
	code                         ckCodeView
	lastClick                    time.Time
	lastClickKey                 string
	frame                        string
	frameAt                      ckFrameKey
	liveID                       string
	live                         string
	liveCols, liveRows           int
	liveDirty                    bool
	liveGen, liveTick, livePrime uint64
	primeID                      string
	primeTimer                   *time.Timer
	liveShown, liveOwned         bool
	cpWant                       bool
	cps                          map[string]*ckCheckpoints
	img                          *ckImageView
	tab                          string
	dv                           *diffview.Theme
	dvKey                        string
	cardRoom                     int
	gen, rowsGen                 uint64
	pending                      string
	fleetW                       int
	placed                       map[string]ckPlaced
	holdUntil                    time.Time
	holdTimer                    *time.Timer
	back                         *ckBack
	jumps                        []ckJump
	seen                         map[string]ckSeenAgent
	talk                         ckTalkCache
	talkShown                    int
	look                         ckLook
	lookAt                       ckLookKey
	lookGen                      uint64
	canvas                       *ckCanvas
	canvasTop                    int
	linkHover                    ckLinkRun
	linkPointX, linkPointY       int
	codeWrap                     bool
	linkPress                    string
	mouse                        CopyMouseGesture
	lastTop                      CockpitLayer
	replyCheck                   bool
	edW                          int
	cutKey                       string
	folders                      map[string]int
	spawn, spawnDraft            *spawnSheet
	spawnGen                     uint64
	spawnLast                    string
	spawnPreset                  *spawnPreset
	spawnBg                      []uv.Line
	spawnBgAt                    ckFrameKey
	spawnFade                    map[color.Color]color.Color
	cmp                          *ckCompare
	art                          ckArtState
	board                        bool
	cards                        []session.TaskRow
	farCards                     []session.TaskRow
	cardsGen                     uint64
	noBoard                      bool
	boardErr                     string
	boardFolded, boardOpened     map[string]bool
	card                         *ckTaskDetail
	cardWant                     bool
	held                         map[string][]session.TaskRow
	heldGen                      uint64
	cardKeys                     []string
	cardKeysGen                  uint64
	stages                       string
	stagesCfg                    *config.UserConfig
	boardList                    bool
	kb                           ckKanban
	dispatch                     *session.DispatchStatus
	dispatchBoard                string
	dispatchBusy                 bool
	listMode                     string
	stripShown                   bool
	sideRoom, focusShown         bool
	layoutRefused                string
}

type ckBack struct {
	key, akey string
	scroll    int
}

type ckPlaced struct {
	shown, want int
	since       time.Time
}

type ckRowsKey struct {
	list        agentsListKey
	inbox, gen  uint64
	arts        uint64
	colH        int
	oneLine     bool
	groupBy     string
	minute      int64
	foldWorking bool
	cards       uint64
	board       bool
	stages      string
	kanban      bool
}

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
	ckHitColumn
)

func (m *OS) cockpitTouch() { m.Inbox.ck.gen++ }

func (m *OS) cockpitTouchRows() {
	m.Inbox.ck.gen++
	m.Inbox.ck.rowsGen++
}

// CockpitTouch is cockpitTouch for the input path.
func (m *OS) CockpitTouch() { m.cockpitTouch() }

// CockpitSync is cockpitSync for the input path.
func (m *OS) CockpitSync() { m.cockpitSync() }

func (m *OS) cockpitList() []sidebarAgentEntry {
	return m.inboxAgentsList()
}

func (m *OS) cockpitRows() []ckRow {
	ck := &m.Inbox.ck
	list := m.cockpitList()
	now := time.Now()
	m.cockpitSettleHold(now)
	key := ckRowsKey{
		list: m.Inbox.agents.listAt, inbox: m.Inbox.Gen, gen: ck.rowsGen, colH: ck.colH, arts: m.tml.setGen<<20 + ck.art.gen,
		oneLine: ck.oneLine, minute: now.Unix() / 60, foldWorking: m.ckFoldWorking(),
		cards: ck.cardsGen, board: ck.board, stages: m.ckStagesShown(), kanban: m.kanbanOn(),
		groupBy: m.ckGroupBy(),
	}
	if ck.rows != nil && ck.rowsAt == key {
		return ck.rows
	}
	ck.agents = list
	m.ckSeeAgents(list)
	ck.folders = ckActiveFolders(list)
	if sig := m.Inbox.agents.listAt.sig; ck.branches == nil || ck.branchesAt != sig {
		ck.branches, ck.branchesAt = m.ckBranches(), sig
	}
	if m.Inbox.Select == "" && m.Inbox.agents.query == "" {
		ck.machines = map[string]int{}
		for _, e := range list {
			ck.machines[e.Host]++
		}
	}
	switch {
	case key.kanban:
		ck.rows = m.buildKanbanRows(list)
	case ck.board:
		ck.rows = m.buildBoardRows(list)
	default:
		ck.rows = m.buildCockpitRows(list, now)
	}
	ck.rowsAt = key
	m.cockpitClamp()
	return ck.rows
}

func ckFolderKey(host, cwd string) string { return host + "\x00" + cwd }

func ckActive(state string) bool { return state == "working" || state == "needs_input" }

func ckActiveFolders(list []sidebarAgentEntry) map[string]int {
	out := map[string]int{}
	for _, e := range list {
		if e.Cwd != "" && ckActive(e.State) {
			out[ckFolderKey(e.Host, e.Cwd)]++
		}
	}
	return out
}

func (m *OS) ckSharesFolder(e sidebarAgentEntry) bool {
	return e.Cwd != "" && ckActive(e.State) && m.Inbox.ck.folders[ckFolderKey(e.Host, e.Cwd)] > 1
}

func (m *OS) ckFolderAgents(folder string) int {
	if folder == "" {
		return 0
	}
	n := 0
	for _, e := range m.cockpitList() {
		if e.Host == "" && ckActive(e.State) && e.Cwd == folder {
			n++
		}
	}
	return n
}

func (m *OS) buildCockpitRows(list []sidebarAgentEntry, now time.Time) []ckRow {
	ck := &m.Inbox.ck
	st := &m.Inbox
	var groups [ckGroups][]ckRow
	byKey := make(map[string]int, len(list))
	for i, e := range list {
		byKey[e.Host+"\x00"+e.SessionID+"\x00"+e.WindowID] = i
	}
	used := make(map[int]bool)
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
				groups[ckNeeds] = append(groups[ckNeeds], ckRow{kind: ckRowNote, group: ckNeeds, note: r.note, lines: 1, agent: -1, key: "n:" + r.note})
			}
			continue
		}
		it := *r.item
		if it.Kind == session.AttentionFinished && judges[it.Host+"\x00"+it.Window] {
			continue
		}
		g := ckNeeds
		switch {
		case it.SnoozedUntil != 0:
			g = ckSnoozed
		case it.Kind == session.AttentionFinished:
			g = ckReview
		case inboxWithParent(it):
			g = ckWorking
		}
		idx := -1
		if it.Window != "" {
			if i, ok := byKey[it.Host+"\x00"+it.Session+"\x00"+it.Window]; ok {
				idx = i
				used[i] = true
			}
		}
		if searching && idx < 0 && !m.ckItemMatches(it) {
			continue
		}
		if g == ckReview && idx >= 0 {
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
	kindFilter := st.Filter != ""
	for i, e := range list {
		if used[i] || kindFilter || judges[e.Host+"\x00"+e.WindowID] {
			continue
		}
		g := ckIdle
		switch sidebarAgentGroup(e.State, e.DoneSeen) {
		case sidebarGroupNeedsYou:
			if e.State == "needs_input" {
				g = ckNeeds
			}
		case sidebarGroupFinished:
			g = ckReview
		case sidebarGroupWorking:
			g = ckWorking
		default:
			if e.State != "limit_reached" && e.StateAt > 0 && now.Sub(time.Unix(0, e.StateAt)) > ckSettleAfter {
				g = ckSettled
			}
		}
		if e.Settled && g != ckNeeds {
			g = ckSettled
		}
		if g != ckNeeds && m.ckArtifactNew(e, true) {
			g = ckNeeds
		}
		g = m.ckHoldGroup(agentKey(e), g, now)
		groups[g] = append(groups[g], ckRow{kind: ckRowAgent, group: g, agent: i, key: "a:" + agentKey(e)})
	}

	var rows []ckRow
	ck.tall = 0
	itemsOnly := ck.needsOnly || kindFilter
	add := func(g int, r ckRow) {
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
			return
		}
		r.lines = 2
		var e sidebarAgentEntry
		if r.agent >= 0 {
			e = list[r.agent]
		}
		r.inline = false
		chainWaits := r.agent >= 0 && (m.childrenWaiting(e) > 0 || e.State == "limit_reached")
		if (g == ckIdle || g == ckSettled) && !chainWaits || g == ckSnoozed || r.kind == ckRowNote || r.attempt || !m.ckHasLine2(r, e) {
			r.lines = 1
		}
		ck.tall += r.lines
		if ck.oneLine && r.lines == 2 && g != ckNeeds {
			r.lines, r.inline = 1, true
		}
		rows = append(rows, r)
		if r.kind == ckRowAgent && !r.hasItem && !r.attempt && !ck.oneLine {
			rows = append(rows, ckChildRows(list[r.agent], g)...)
		}
	}
	by := m.ckGroupBy()
	var bands []ckBand
	ck.groupN = [ckGroups]int{}
	for g := range ckGroups {
		items := ck.ckPinFirst(m.ckGatherFans(groups[g], list), list)
		if len(items) == 0 || itemsOnly && g != ckNeeds && g != ckSnoozed && g != ckReview {
			continue
		}
		n := 0
		for _, r := range items {
			if r.selectable() && (r.hasItem || !itemsOnly) {
				n++
			}
		}
		ck.groupN[g] = n
		if n == 0 || !ck.chipKeeps(g) {
			continue
		}
		if by != "" && g != ckNeeds {
			bands = m.ckBandAdd(bands, by, g, items, itemsOnly, list)
			continue
		}
		if len(rows) > 0 {
			rows = append(rows, ckRow{kind: ckRowGap, group: g, lines: 1, agent: -1, key: "g:" + strconv.Itoa(g)})
		}
		head := ckRow{kind: ckRowHead, group: g, agent: -1, count: n, lines: 1, key: "h:" + strconv.Itoa(g)}
		if m.ckGroupFolded(g) {
			head.folded = true
			for _, r := range items {
				if r.selectable() && (r.hasItem || !itemsOnly) {
					head.hidden = append(head.hidden, r)
				}
			}
			rows = append(rows, head)
			continue
		}
		rows = append(rows, head)
		shown := 0
		for _, r := range items {
			if itemsOnly && !r.hasItem && r.kind != ckRowNote {
				continue
			}
			if r.selectable() && g != ckNeeds && ck.oneLine && !ck.opened[g] && shown >= ckFoldAt && !ck.cursorOn(r, list) {
				continue
			}
			if r.selectable() {
				shown++
			}
			add(g, r)
		}
		if hidden := n - shown; hidden > 0 {
			rows = append(rows, ckRow{kind: ckRowMore, group: g, count: hidden, lines: 1, agent: -1, key: "m:" + strconv.Itoa(g)})
		}
	}
	for _, b := range bands {
		if len(rows) > 0 {
			rows = append(rows, ckRow{kind: ckRowGap, group: ckIdle, lines: 1, agent: -1, key: "bg:" + b.key})
		}
		rows = append(rows, ckRow{kind: ckRowBand, group: ckIdle, agent: -1, count: b.count, title: b.title, lines: 1, key: "b:" + b.key})
		for _, br := range b.rows {
			add(br.group, br.row)
		}
	}
	return rows
}

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

type ckHoldMsg struct{}

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

func (m *OS) cockpitHoldDue(now time.Time) bool {
	h := m.Inbox.ck.holdUntil
	return m.ShowInbox && !h.IsZero() && !now.Before(h)
}

func (m *OS) cockpitSettleHold(now time.Time) {
	if m.cockpitHoldDue(now) {
		m.Inbox.ck.holdUntil = time.Time{}
		m.cockpitTouchRows()
	}
}

func (m *OS) ckItemMatches(it session.AttentionItem) bool {
	q := m.Inbox.agents.query
	return q == "" || fuzzy.Match(q, inboxWho(it)+" "+m.inboxWhere(it)+" "+it.Summary)
}

func (m *OS) ckGatherFans(items []ckRow, list []sidebarAgentEntry) []ckRow {
	fanOf := func(r ckRow) string {
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
		sortx.ShortStableFunc(tries, func(a, b ckRow) int {
			return ckFanOrder(m.ckAgentSession(list[a.agent]), m.ckAgentSession(list[b.agent]))
		})
		tries[len(tries)-1].last = true
		out = append(out, ckRow{kind: ckRowFan, group: r.group, agent: tries[0].agent, count: m.ckFanSize(f), key: "f:" + f})
		out = append(out, tries...)
	}
	return out
}

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

func ckChildRows(e sidebarAgentEntry, g int) []ckRow {
	var rows []ckRow
	for i, c := range e.Children {
		if i == ckChildMax {
			rows = append(rows, ckRow{kind: ckRowMore, group: -1, count: len(e.Children) - i, lines: 1, agent: -1, key: "c:" + agentKey(e) + ":more"})
			break
		}
		rows = append(rows, ckRow{kind: ckRowChild, group: g, child: c, lines: 1, agent: -1, key: "c:" + agentKey(e) + ":" + c.ID})
	}
	if n := len(rows); n > 0 {
		rows[n-1].last = true
	}
	return rows
}

func (m *OS) cockpitClamp() {
	ck := &m.Inbox.ck
	rows := ck.rows
	find := func(match func(ckRow) bool) int {
		for i, r := range rows {
			if r.cursorable() && match(r) {
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
		ck.replyCheck = true
	}
}

func ckNearest(rows []ckRow, idx int) int {
	idx = min(max(idx, 0), len(rows)-1)
	for d := range rows {
		for _, j := range [2]int{idx + d, idx - d} {
			if j >= 0 && j < len(rows) && rows[j].cursorable() {
				return j
			}
		}
	}
	return -1
}

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

func (m *OS) cockpitSelected() (ckRow, bool) {
	rows := m.cockpitRows()
	ck := &m.Inbox.ck
	if ck.idx < 0 || ck.idx >= len(rows) || rows[ck.idx].key != ck.key || !rows[ck.idx].cursorable() {
		return ckRow{}, false
	}
	return rows[ck.idx], true
}

func (m *OS) cockpitAgent() (sidebarAgentEntry, bool) {
	r, ok := m.cockpitSelected()
	if !ok || r.agent < 0 {
		return sidebarAgentEntry{}, false
	}
	return m.Inbox.ck.agents[r.agent], true
}

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
		if r.cursorable() {
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
		if delta == 1 && ck.idx+1 < len(rows) && rows[ck.idx+1].kind == ckRowMore && rows[ck.idx+1].group >= 0 && !ck.board {
			g := rows[ck.idx+1].group
			key := ck.key
			m.CockpitToggleGroup(g, true)
			rows = m.cockpitRows()
			sel, at = sel[:0], -1
			for i, r := range rows {
				if r.cursorable() {
					if r.key == key {
						at = len(sel)
					}
					sel = append(sel, i)
				}
			}
			if at < 0 || len(sel) == 0 {
				return
			}
		}
		at = min(max(at+delta, 0), len(sel)-1)
	}
	m.cockpitSetCursor(sel[at])
	m.Inbox.ck.pending = ""
	m.cockpitTouch()
	m.cockpitReplyFollow()
	m.cockpitSync()
}

func (m *OS) cockpitReplyFollow() {
	if m.cockpitReplyMismatch() {
		m.InboxReplyCancel()
	}
}

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

func (m *OS) cockpitSelectKey(key string) bool {
	for i, r := range m.cockpitRows() {
		if r.key == key && r.cursorable() {
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
	if ck.board {
		if g < 0 || g >= len(session.TaskStages) {
			return
		}
		flip := &ck.boardFolded
		if more {
			flip = &ck.boardOpened
		}
		if *flip == nil {
			*flip = map[string]bool{}
		}
		s := session.TaskStages[g]
		(*flip)[s] = !(*flip)[s]
		m.cockpitTouchRows()
		return
	}
	if g < 0 || g >= ckGroups {
		return
	}
	if more {
		ck.opened[g] = !ck.opened[g]
	} else {
		ck.folded[g] = !m.ckGroupFolded(g)
		ck.foldSet[g] = true
		m.saveSidebarState()
	}
	m.cockpitTouchRows()
}

func (m *OS) ckFoldWorking() bool {
	return m.UserConfig != nil && m.UserConfig.Agents.FoldWorkingOn()
}

func (m *OS) ckGroupFolded(g int) bool {
	ck := &m.Inbox.ck
	if ck.foldSet[g] {
		return ck.folded[g]
	}
	return g == ckWorking && m.ckFoldWorking()
}

var ckGroupIDs = [ckGroups]string{"needs", "review", "working", "idle", "earlier", "snoozed"}

func (ck *cockpitState) ckFoldsToFile() map[string]bool {
	var out map[string]bool
	for g := range ckGroups {
		if ck.foldSet[g] {
			if out == nil {
				out = map[string]bool{}
			}
			out[ckGroupIDs[g]] = ck.folded[g]
		}
	}
	return out
}

func (ck *cockpitState) ckFoldsFromFile(folds map[string]bool) {
	for g, id := range ckGroupIDs {
		if f, ok := folds[id]; ok {
			ck.folded[g], ck.foldSet[g] = f, true
		}
	}
}

// CockpitFold is F: it folds or opens the group of the row under the cursor.
// A fold leaves the cursor on the group's heading, so F again opens it. On
// the board it folds the stage, as a click on its heading does.
func (m *OS) CockpitFold() {
	r, ok := m.cockpitSelected()
	if !ok {
		return
	}
	m.CockpitToggleGroup(r.group, false)
	head := "h:" + strconv.Itoa(r.group)
	if m.Inbox.ck.board {
		head = "bh:" + session.TaskStages[r.group]
	}
	if r.kind != ckRowHead {
		m.cockpitSelectKey(head)
	} else {
		m.cockpitRows()
		m.cockpitSync()
	}
	m.cockpitTouch()
	m.cockpitReplyFollow()
}

func ckPinKey(e sidebarAgentEntry) string {
	if e.Host != "" {
		return e.Host + "/" + e.WindowID
	}
	return e.WindowID
}

func (ck *cockpitState) ckPinned(r ckRow, list []sidebarAgentEntry) bool {
	return len(ck.pins) > 0 && r.kind == ckRowAgent && r.agent >= 0 && r.agent < len(list) &&
		list[r.agent].WindowID != "" && ck.pins[ckPinKey(list[r.agent])]
}

func (ck *cockpitState) ckPinFirst(items []ckRow, list []sidebarAgentEntry) []ckRow {
	if len(ck.pins) == 0 {
		return items
	}
	out := make([]ckRow, 0, len(items))
	for _, r := range items {
		if ck.ckPinned(r, list) {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return items
	}
	for _, r := range items {
		if !ck.ckPinned(r, list) {
			out = append(out, r)
		}
	}
	return out
}

// CockpitPin is P: it pins the agent under the cursor to the top of its
// group, or unpins it. The cursor stays on the agent's row.
func (m *OS) CockpitPin() {
	ck := &m.Inbox.ck
	r, ok := m.cockpitSelected()
	if !ok || ck.board || r.kind != ckRowAgent || r.agent < 0 || r.agent >= len(ck.agents) || ck.agents[r.agent].WindowID == "" {
		m.ShowNotification("Move the cursor to an agent to pin it.", "info", m.Settings.NotificationDuration)
		return
	}
	e := ck.agents[r.agent]
	k := ckPinKey(e)
	if ck.pins == nil {
		ck.pins = map[string]bool{}
	}
	if ck.pins[k] {
		delete(ck.pins, k)
	} else {
		ck.pins[k] = true
	}
	m.ckPrunePins()
	m.saveSidebarState()
	m.cockpitTouchRows()
	m.cockpitRows()
	m.cockpitSelectKey(r.key)
	m.cockpitTouch()
}

func (m *OS) ckPrunePins() {
	ck := &m.Inbox.ck
	if m.Inbox.Select != "" || m.Inbox.agents.query != "" || m.Inbox.Filter != "" {
		return
	}
	live := map[string]bool{}
	for _, e := range ck.agents {
		live[ckPinKey(e)] = true
	}
	for k := range ck.pins {
		if !strings.Contains(k, "/") && !live[k] {
			delete(ck.pins, k)
		}
	}
}

func (ck *cockpitState) ckPinsToFile() []string {
	if len(ck.pins) == 0 {
		return nil
	}
	out := make([]string, 0, len(ck.pins))
	for k := range ck.pins {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func (ck *cockpitState) ckPinsFromFile(pins []string) {
	ck.pins = nil
	for _, k := range pins {
		if ck.pins == nil {
			ck.pins = map[string]bool{}
		}
		ck.pins[k] = true
	}
}

func (ck *cockpitState) chipKeeps(g int) bool {
	return ck.chip == 0 || ck.chip == g+1 || ck.chip == ckIdle+1 && g == ckSettled
}

func (m *OS) cockpitChip(g int) {
	ck := &m.Inbox.ck
	if ck.chip == g+1 {
		ck.chip = 0
	} else {
		ck.chip = g + 1
	}
	ck.scroll, ck.follow = 0, true
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

func (m *OS) ckListMode() string {
	if mode := m.Inbox.ck.listMode; mode != "" {
		return mode
	}
	if m.UserConfig == nil {
		return config.AgentsViewListColumn
	}
	return m.UserConfig.Agents.ViewListMode()
}

// CockpitToggleList is the list key: the strip when the view shows the
// column, and the column when it shows the strip. The choice is kept.
func (m *OS) CockpitToggleList() {
	ck := &m.Inbox.ck
	ck.listMode = config.AgentsViewListStrip
	if ck.stripShown {
		ck.listMode = config.AgentsViewListColumn
	}
	m.cockpitTouchRows()
	m.saveSidebarState()
}

// CockpitNeedsOnly reports whether the f filter shows only Needs you.
func (m *OS) CockpitNeedsOnly() bool { return m.Inbox.ck.needsOnly }

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

func (m *OS) cockpitUnheldApproval() bool {
	r, ok := m.cockpitSelected()
	if !ok || !r.hasItem || r.item.RequestID != "" {
		return false
	}
	return r.item.Kind == session.AttentionApproval || r.item.Kind == session.AttentionPlan
}

// CockpitAnswerLetter answers the held approval under the cursor with a
// letter: a allows once, A always, X rejects. handled is false when no
// approval waits, and the key then does what it does on any row.
func (m *OS) CockpitAnswerLetter(key string) (tea.Cmd, bool) {
	if _, ok := m.cockpitHeldItem(); !ok {
		if m.cockpitUnheldApproval() {
			m.ShowNotification(inboxNotHeldWords, "warning", m.Settings.NotificationDuration)
			return nil, true
		}
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
	return m.InboxReplyApproval(decision), true
}

// CockpitGo is enter: go to the row's pane and close the cockpit. Mail opens
// its thread, as before.
func (m *OS) CockpitGo() tea.Cmd {
	r, ok := m.cockpitSync()
	if !ok {
		return nil
	}
	if r.kind == ckRowCard {
		return m.cockpitCardGo(r, m.Inbox.ck.cards[r.card])
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

func (m *OS) cockpitWideFocus() bool { return m.GetRenderWidth() >= ckOneColumn }

// CockpitClick handles a click at screen x, y while the cockpit is up.
// handled is false for a click outside it.
func (m *OS) CockpitClick(x, y int) (tea.Cmd, bool) {
	ck := &m.Inbox.ck
	if cmd, ok := m.cockpitMenuClick(x, y); ok {
		return cmd, true
	}
	if m.cockpitClickDismisses(x, y) {
		return nil, true
	}
	if ck.img != nil {
		m.cockpitImageClick(x, y)
		return nil, true
	}
	if m.draftClick("inbox", x, y) {
		return nil, true
	}
	if ck.spawn != nil {
		return m.spawnClick(x, y), true
	}
	if cmd, ok := m.cockpitArtifactMouse(x, y, loom.MouseLeft); ok {
		return cmd, true
	}
	if ck.art.focus {
		ck.art.focus = false
		m.cockpitTouch()
	}
	if m.cockpitDividerPress(x, y) || m.kanbanPreviewClick(x, y) {
		return nil, true
	}
	if m.cockpitLinkPress(x, y) {
		return nil, true
	}
	m.cockpitFocusRegionAt(x, y)
	for _, h := range ck.hits {
		if x < h.x0 || x >= h.x1 || y < h.y0 || y >= h.y1 {
			continue
		}
		switch h.kind {
		case ckHitHead:
			m.CockpitToggleGroup(h.group, false)
		case ckHitMore:
			m.CockpitToggleGroup(h.group, true)
		case ckHitColumn:
			m.kanbanFocusColumn(h.group)
		case ckHitRow:
			m.kanbanPress(h, x, y)
			if ck.cmp != nil {
				m.CockpitCompareClose()
			}
			now := time.Now()
			double := ck.lastClickKey == h.key && now.Sub(ck.lastClick) <= m.DoubleClickWindow()
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

func (m *OS) cockpitAction(action string) tea.Cmd {
	if cmd, ok := m.kanbanPreviewAction(action); ok {
		return cmd
	}
	switch action {
	case "new":
		return m.CockpitNewAgent()
	case "group", "board", "scope", "talked":
		m.cockpitOpenMenu(action)
		return nil
	case "dispatch-mode":
		return m.CockpitDispatchMode()
	case "dispatch-pause":
		return m.CockpitDispatchPause()
	case "search":
		m.InboxStartSelect()
	case "needs":
		m.CockpitCycleFilter()
	case "show-all":
		m.cockpitShowAll()
	case "chip:0", "chip:1", "chip:2", "chip:3":
		g, _ := strconv.Atoi(strings.TrimPrefix(action, "chip:"))
		m.cockpitChip(g)
	case "dismiss":
		return m.InboxDismiss()
	case "follow":
		m.CockpitThreadBottom()
	case "a", "A", "X":
		decision := map[string]string{"a": session.ApprovalOnce, "A": session.ApprovalAlways, "X": session.ApprovalDeny}[action]
		if m.InboxConfirmOpen() && m.Inbox.approvals.answer.confirm.decision == decision {
			return m.InboxConfirmKey("enter")
		}
		cmd, _ := m.CockpitAnswerLetter(action)
		if it, ok := m.inboxSelected(); ok && m.InboxConfirmOpen() && !inboxRisky(it) {
			return m.InboxConfirmKey("enter")
		}
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
	case "settle":
		cmd, _ := m.InboxSettle()
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
		if i, ok := strings.CutPrefix(action, "mention:"); ok {
			n, _ := strconv.Atoi(i)
			if ed := m.Inbox.reply.editor; ed != nil && ed.comp.PickMention(n) {
				return m.replyMentionCmd()
			}
			return nil
		}
		if id, ok := strings.CutPrefix(action, "talk:"); ok {
			return m.cockpitTalkClick(id)
		}
		if id, ok := strings.CutPrefix(action, "peer:"); ok {
			return m.cockpitPeerClick(id)
		}
		if ids, ok := strings.CutPrefix(action, "kid:"); ok {
			return m.cockpitChildClick(ids)
		}
		if key, ok := strings.CutPrefix(action, ckBlockHit); ok {
			m.ToggleBlock(key)
			return nil
		}
		if key, ok := strings.CutPrefix(action, ckBlockPicHit); ok {
			m.ToggleBlockPicture(key)
			return nil
		}
		if img, ok := strings.CutPrefix(action, "timg:"); ok {
			return m.cockpitOpenThreadImage(img)
		}
		if cmd, ok := m.echoAction(action); ok {
			return cmd
		}
		if key, ok := strings.CutPrefix(action, ckCodeCopyHit); ok {
			if t := m.cockpitThread(); t != nil {
				return m.cockpitCopyCode(t, key)
			}
			return nil
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

func (m *OS) cockpitHeldReplyWords(it session.AttentionItem) string {
	allow := m.inboxKeyOr(config.ActionInboxAllow, "a")
	reject := m.inboxKeyOr(config.ActionInboxReject, "X")
	if it.Kind == session.AttentionPlan {
		return "Answer the plan first. Press " + allow + " to approve or " + reject + " to keep planning."
	}
	return "Answer first. Press " + allow + " to allow or " + reject + " to reject."
}

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
	if s := ck.spawn; s != nil {
		m.cockpitClearLinkHover()
		if s.list && s.dd != nil && s.dd.Hover(x, y) {
			m.cockpitTouch()
		}
		return
	}
	if m.cockpitMenuHover(x, y) {
		m.cockpitClearLinkHover()
		return
	}
	if m.cockpitDividerMotion(x, y) || m.kanbanPreviewMotion(x, y) {
		return
	}
	if m.kanbanDragTo(x, y) {
		return
	}
	m.cockpitLinkHover(x, y)
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
	if m.kanbanWheel(x, y, up) {
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
	if m.cockpitCopyWheel(x, y, -3*wheelDelta(up)) {
		return
	}
	if t := m.cockpitThread(); t != nil {
		t.follow.Scroll(-3 * wheelDelta(up))
		m.cockpitTouch()
		return
	}
	m.cockpitSync()
	if _, ok := m.InboxDetailScroll(3 * wheelDelta(up)); ok {
		m.cockpitTouch()
	}
}

func (m *OS) cockpitThread() *agentThread {
	e, ok := m.cockpitAgent()
	if !ok {
		return nil
	}
	return m.Inbox.threads.byKey[agentKey(e)]
}

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

func (m *OS) ckTabFolds() bool {
	switch m.cockpitTab() {
	case "Changes", "Checkpoints":
		return true
	}
	return false
}

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
		t.follow.Bottom()
		t.fromTop = m.Inbox.ck.tab == "Changes"
	}
	m.cockpitTouch()
}

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
	m.clearLayoutRefusal()
	if cmd, ok := m.kanbanKey(action); ok {
		return cmd, true
	}
	if cmd, ok := m.cockpitRegionKey(action); ok {
		return cmd, true
	}
	if cmd, ok := m.cockpitLayoutKey(action); ok {
		return cmd, true
	}
	switch action {
	case config.ActionInboxSort:
		if m.Inbox.ck.board {
			return nil, false
		}
		m.cockpitOpenMenu("group")
		return nil, true
	case config.ActionInboxRegionNext, config.ActionInboxRegionPrev:
		d := 1
		if action == config.ActionInboxRegionPrev {
			d = -1
		}
		return m.CockpitStepRegion(d), true
	case config.ActionInboxEscape:
		return m.CockpitEscape(), true
	case config.ActionInboxNextNeeds:
		m.CockpitNextNeeds()
		return nil, true
	case config.ActionInboxAllow, config.ActionInboxAllowAlways, config.ActionInboxReject:
		letter := "X"
		switch action {
		case config.ActionInboxAllow:
			letter = "a"
		case config.ActionInboxAllowAlways:
			letter = "A"
		}
		cmd, _ := m.CockpitAnswerLetter(letter)
		return cmd, true
	case config.ActionInboxReply:
		if it, held := m.cockpitHeldItem(); held {
			m.ShowNotification(m.cockpitHeldReplyWords(it), "info", m.Settings.NotificationDuration)
			return nil, true
		}
		return m.cockpitReply(), true
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
		m.OpenHelpAtCategory(HelpCategoryAgents)
		return nil, true
	case config.ActionInboxTabPrev, config.ActionInboxTabNext:
		d := 1
		if action == config.ActionInboxTabPrev {
			d = -1
		}
		m.CockpitTab(d)
		return nil, true
	case config.ActionInboxFold:
		m.CockpitFold()
		return nil, true
	case config.ActionInboxPin:
		m.CockpitPin()
		return nil, true
	case config.ActionInboxExpand:
		if t := m.cockpitThread(); t != nil {
			t.expandAll = !t.expandAll
			t.gen++
			m.cockpitTouch()
		}
		return nil, true
	case config.ActionInboxCodeWrap:
		m.CockpitCodeWrap()
		return nil, true
	case config.ActionInboxCodeLeft:
		m.CockpitCodeScroll(-1)
		return nil, true
	case config.ActionInboxCodeRight:
		m.CockpitCodeScroll(1)
		return nil, true
	case config.ActionInboxCodeCopy:
		return m.CockpitCodeCopy(), true
	case config.ActionInboxBlockSource:
		m.CockpitBlockToggle(false)
		return nil, true
	case config.ActionInboxBlockPicture:
		m.CockpitBlockToggle(true)
		return nil, true
	case config.ActionInboxArtifact:
		return m.CockpitArtifactOpen(), true
	case config.ActionInboxDetailDown, config.ActionInboxDetailUp:
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
		t.follow.Scroll(-3 * d)
		m.cockpitTouch()
		return nil, true
	case config.ActionInboxPromptPrev, config.ActionInboxPromptNext:
		m.CockpitPromptJump(action == config.ActionInboxPromptNext)
		return nil, true
	case config.ActionInboxNewAgent:
		if _, c, ok := m.cockpitCard(); ok {
			return m.CockpitNewAgentFor(c), true
		}
		return m.CockpitNewAgent(), true
	case config.ActionInboxBoard:
		return m.CockpitToggleBoard(), true
	case config.ActionInboxDispatchMode:
		return m.CockpitDispatchMode(), true
	case config.ActionInboxDispatchPause:
		return m.CockpitDispatchPause(), true
	case config.ActionInboxViewList:
		m.CockpitToggleList()
		return nil, true
	case config.ActionInboxTake:
		return m.CockpitTake(), true
	case config.ActionInboxShip:
		return m.CockpitShip(), true
	case config.ActionInboxCompare:
		return m.CockpitCompare(), true
	case config.ActionInboxWrite:
		return m.cockpitReply(), true
	case config.ActionInboxFilter:
		m.CockpitCycleFilter()
		return nil, true
	case config.ActionInboxChild:
		return nil, m.CockpitNextChild()
	case config.ActionInboxJumpBack:
		return m.CockpitJumpBack()
	case config.ActionInboxGo:
		if cmd, ok := m.cockpitChildGo(); ok {
			return cmd, true
		}
		if r, ok := m.cockpitSelected(); ok && r.kind == ckRowHead {
			m.CockpitFold()
			return nil, true
		}
		if !m.cockpitWideFocus() && !m.Inbox.threads.open {
			return m.CockpitOpenFocus(true), true
		}
		return m.CockpitGo(), true
	case config.ActionInboxUp, config.ActionInboxDown, config.ActionInboxPageUp, config.ActionInboxPageDown,
		config.ActionInboxFirst, config.ActionInboxLast:
		if m.inboxPlanMove(action, false) {
			return nil, true
		}
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
			if m.CockpitThreadBottom() {
				return nil, true
			}
			d = 1 << 20
		}
		m.CockpitMove(d)
		return nil, true
	}
	return nil, false
}

func (m *OS) leaveInboxForPane() {
	ck := &m.Inbox.ck
	back := ckBack{key: ck.key, akey: ck.akey, scroll: ck.scroll}
	m.CloseInbox()
	ck.back = &back
	key := m.sidebarAgentKeyOr("prefix_inbox", "ctrl+b i")
	m.ShowNotification("Press "+key+" to go back to the agents.", "info", m.Settings.NotificationDuration*2)
}

// InboxMentionListOpen reports whether the reply editor's @ list is open.
// ctrl+p and ctrl+n move in that list, so the palette does not take ctrl+p
// while it is open.
func (m *OS) InboxMentionListOpen() bool {
	ed := m.Inbox.reply.editor
	return ed != nil && ed.comp != nil && ed.comp.MentionOpen()
}
