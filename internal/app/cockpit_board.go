package app

import (
	"cmp"
	"encoding/json"
	"errors"
	"image/color"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

// The board: b groups the fleet column by task stage instead of by agent
// state. A card held by an agent pane is drawn with that agent's mark and
// line two, so who holds what reads in one glance. The cards come through
// the Inbox watcher (inboxListCards and the task events), and a card's prompt
// and history are read with get-task for the Card tab, after the frame.

// ckTaskDetail is the Card tab's detail of one card.
type ckTaskDetail struct {
	key     string
	prompt  string
	history []session.TaskEvent
	err     string
	loading bool
}

// ckTaskMsg is a get-task reply.
type ckTaskMsg struct {
	key     string
	prompt  string
	history []session.TaskEvent
	err     error
}

// ckTakeMsg is a claim-task reply.
type ckTakeMsg struct {
	row session.TaskRow
	err error
}

// sortCards orders cards by session, then by number.
func sortCards(cards []session.TaskRow) {
	num := func(id string) uint64 {
		n, _ := strconv.ParseUint(strings.TrimPrefix(id, "t"), 10, 64)
		return n
	}
	// sort.Slice, not slices.SortFunc: see docs/perf.md, "Where the bytes go".
	sort.Slice(cards, func(i, j int) bool {
		a, b := cards[i], cards[j]
		return cmp.Or(strings.Compare(a.Session, b.Session), cmp.Compare(num(a.ID), num(b.ID))) < 0
	})
}

// applyBoardSnapshot takes the watcher's listing of the cards.
func (m *OS) applyBoardSnapshot(msg InboxSnapshotMsg) {
	ck := &m.Inbox.ck
	ck.cards, ck.noBoard, ck.boardErr = msg.Cards, msg.NoBoard, msg.BoardErr
	if ck.cards == nil {
		ck.cards = []session.TaskRow{}
	}
	ck.cardsGen++
}

// applyTaskEvent folds one task event into the cards.
func (m *OS) applyTaskEvent(action string, row session.TaskRow) {
	ck := &m.Inbox.ck
	i := slices.IndexFunc(ck.cards, func(c session.TaskRow) bool { return c.Session == row.Session && c.ID == row.ID })
	switch {
	case action == session.TaskActionRemoved:
		if i >= 0 {
			ck.cards = slices.Delete(ck.cards, i, i+1)
		}
	case i >= 0:
		ck.cards[i] = row
	default:
		ck.cards = append(ck.cards, row)
		sortCards(ck.cards)
	}
	ck.cardsGen++
}

// stageFolded reports whether stage g shows its head only. Done starts
// folded.
func (ck *cockpitState) stageFolded(g int) bool {
	s := session.TaskStages[g]
	return ck.boardFolded[s] != (s == session.TaskStageDone)
}

// ckStageTitle is a stage as its head says it.
func ckStageTitle(g int) string {
	s := session.TaskStages[g]
	return strings.ToUpper(s[:1]) + s[1:]
}

// ckStagesShown is [tasks] stages as the rows key holds it, joined once per
// config load: a frame compares a pointer, not the list.
func (m *OS) ckStagesShown() string {
	ck := &m.Inbox.ck
	if ck.stagesCfg != m.UserConfig {
		ck.stagesCfg, ck.stages = m.UserConfig, ""
		if m.UserConfig != nil {
			ck.stages = strings.Join(m.UserConfig.Tasks.Stages, ",")
		}
	}
	return ck.stages
}

// ckHeldCards indexes the cards by holder, session and window id, built once
// per change of the cards.
func (m *OS) ckHeldCards() map[string][]session.TaskRow {
	ck := &m.Inbox.ck
	if ck.held == nil || ck.heldGen != ck.cardsGen {
		ck.held, ck.heldGen = map[string][]session.TaskRow{}, ck.cardsGen
		for _, c := range ck.cards {
			if c.Holder != nil {
				k := c.Session + "\x00" + c.Holder.Window
				ck.held[k] = append(ck.held[k], c)
			}
		}
	}
	return ck.held
}

// ckStageShows reports whether [tasks] stages shows stage.
func (m *OS) ckStageShows(stage string) bool {
	return m.UserConfig == nil || m.UserConfig.Tasks.Shows(stage)
}

// buildBoardRows groups the cards by stage. A card shows when its holder is
// in list, which the selector and the search have narrowed, or with no
// selector when the search matches its own words.
func (m *OS) buildBoardRows(list []sidebarAgentEntry) []ckRow {
	ck := &m.Inbox.ck
	st := &m.Inbox
	byKey := make(map[string]int, len(list))
	for i, e := range list {
		if e.Host == "" {
			byKey[e.SessionID+"\x00"+e.WindowID] = i
		}
	}
	// A killed session's cards are gone, and no event says so.
	var live map[string]bool
	if c := m.DaemonClient; c != nil {
		if names := c.AvailableSessionNames(); len(names) > 0 {
			live = make(map[string]bool, len(names))
			for _, n := range names {
				live[n] = true
			}
		}
	}
	searching := st.agents.query != "" || st.Select != ""
	stages := make([][]ckRow, len(session.TaskStages))
	for i, c := range ck.cards {
		g := slices.Index(session.TaskStages, c.Stage)
		if g < 0 || (live != nil && !live[c.Session]) || !m.ckStageShows(c.Stage) {
			continue
		}
		idx := -1
		if c.Holder != nil {
			if j, ok := byKey[c.Session+"\x00"+c.Holder.Window]; ok {
				idx = j
			}
		}
		if searching && idx < 0 {
			holder := ""
			if c.Holder != nil {
				holder = c.Holder.Name
			}
			if st.Select != "" || !fuzzy.Match(st.agents.query, c.ID+" "+c.Title+" "+holder) {
				continue
			}
		}
		stages[g] = append(stages[g], ckRow{kind: ckRowCard, group: g, card: i, agent: idx, key: "t:" + c.Session + "/" + c.ID})
	}
	// Blocked first, then a holder that needs the person, then the oldest.
	rank := func(r ckRow) int {
		c := ck.cards[r.card]
		state := ""
		switch {
		case r.agent >= 0:
			state = list[r.agent].State
		case c.Holder != nil:
			state = c.Holder.State
		}
		switch {
		case c.Blocked != nil:
			return 0
		case sidebarAttention(state):
			return 1
		}
		return 2
	}
	var rows []ckRow
	ck.tall = 0
	for g, items := range stages {
		if len(items) == 0 {
			continue
		}
		sortx.ShortStableFunc(items, func(a, b ckRow) int {
			return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(ck.cards[a.card].StageAt, ck.cards[b.card].StageAt))
		})
		stage := session.TaskStages[g]
		if len(rows) > 0 {
			rows = append(rows, ckRow{kind: ckRowGap, group: g, lines: 1, key: "bg:" + stage})
		}
		head := ckRow{kind: ckRowHead, group: g, agent: -1, count: len(items), lines: 1, key: "bh:" + stage}
		if ck.stageFolded(g) {
			head.folded, head.hidden = true, items
			rows = append(rows, head)
			continue
		}
		rows = append(rows, head)
		shown := 0
		for _, r := range items {
			if ck.oneLine && !ck.boardOpened[stage] && shown >= ckFoldAt {
				continue
			}
			shown++
			r.lines = 2
			ck.tall += 2
			if ck.oneLine {
				r.lines = 1
			}
			rows = append(rows, r)
		}
		if hidden := len(items) - shown; hidden > 0 {
			rows = append(rows, ckRow{kind: ckRowMore, group: g, count: hidden, lines: 1, key: "bm:" + stage})
		}
	}
	return rows
}

// CockpitToggleBoard is b: the board, or the agents again.
func (m *OS) CockpitToggleBoard() {
	ck := &m.Inbox.ck
	ck.board = !ck.board
	ck.scroll, ck.idx, ck.key, ck.akey, ck.follow = 0, 0, "", "", true
	m.cockpitTouchRows()
}

// CockpitBoardOn reports whether the fleet column shows the board.
func (m *OS) CockpitBoardOn() bool { return m.Inbox.ck.board }

// cockpitCard is the card of the row under the cursor.
func (m *OS) cockpitCard() (ckRow, session.TaskRow, bool) {
	r, ok := m.cockpitSelected()
	if !ok || r.kind != ckRowCard || r.card >= len(m.Inbox.ck.cards) {
		return ckRow{}, session.TaskRow{}, false
	}
	return r, m.Inbox.ck.cards[r.card], true
}

// ckTakeWindow is the pane t takes a card for: the focused pane behind the
// agents view, when it is in the card's session on this machine.
func (m *OS) ckTakeWindow(c session.TaskRow) string {
	if m.AttachedHost != "" || !m.IsDaemonSession || c.Session != m.SessionName {
		return ""
	}
	if w := m.GetFocusedWindow(); w != nil {
		return w.ID
	}
	return ""
}

// CockpitTake is t: claim the card under the cursor for the focused pane.
func (m *OS) CockpitTake() tea.Cmd {
	_, c, ok := m.cockpitCard()
	if !ok {
		return nil
	}
	win := m.ckTakeWindow(c)
	switch {
	case win == "":
		m.ShowNotification("t takes a card for the pane behind the agents view. Attach to session "+printableTitle(c.Session)+" to take "+c.ID+".", "info", m.Settings.NotificationDuration)
		return nil
	case c.Holder != nil && c.Holder.Window == win:
		m.ShowNotification("The pane already holds "+c.ID+".", "info", m.Settings.NotificationDuration)
		return nil
	}
	call, nonce := m.inboxCaller(), m.inboxNonce()
	return func() tea.Msg {
		raw, err := call("claim-task", map[string]any{"session": c.Session, "id": c.ID, "window": win, "human_nonce": nonce}, 5*time.Second)
		msg := ckTakeMsg{err: err}
		if err == nil {
			msg.err = json.Unmarshal(raw, &msg.row)
		}
		return msg
	}
}

// applyTake takes a claim-task reply. The event stream brings the same row.
func (m *OS) applyTake(msg ckTakeMsg) {
	if msg.err != nil {
		m.ShowNotification("The card was not taken: "+printableTitle(msg.err.Error()), "error", m.Settings.NotificationDuration)
		return
	}
	m.applyTaskEvent(session.TaskActionClaimed, msg.row)
	m.inboxChanged()
}

// CockpitNewAgentFor is n on a card: the spawn sheet with the card's prompt,
// or its title while the prompt is not read, as the task. Start in this
// folder claims the card for the new pane. A draft kept from esc keeps its
// own task, and the card is not tied to it.
func (m *OS) CockpitNewAgentFor(c session.TaskRow) tea.Cmd {
	cmd := m.CockpitNewAgent()
	s := m.Inbox.ck.spawn
	if s == nil {
		return cmd
	}
	if strings.TrimSpace(s.task.Text()) != "" {
		s.notice = "The sheet keeps the task you were writing. Clear it to use " + c.ID + "'s."
		return cmd
	}
	text := c.Title
	if d := m.Inbox.ck.card; d != nil && d.key == ckTaskKey(c) && d.prompt != "" {
		text = d.prompt
	}
	s.task.SetText(text)
	m.spawnFollowTask(s)
	s.card = &spawnCard{session: c.Session, id: c.ID}
	return cmd
}

// spawnCard is the card a sheet opened on, which Start claims for the new
// pane.
type spawnCard struct{ session, id string }

// spawnClaim claims the sheet's card for the pane start-agent opened. It runs
// in the start's command, after the start answered.
func spawnClaim(call inboxVerbCall, nonce string, card *spawnCard, sess, win string) string {
	if card == nil || win == "" || sess != card.session {
		return ""
	}
	_, err := call("claim-task", map[string]any{"session": card.session, "id": card.id, "window": win, "human_nonce": nonce}, 5*time.Second)
	if err != nil {
		return " " + card.id + " was not taken: " + printableTitle(err.Error())
	}
	return " It holds " + card.id + "."
}

// ckTaskKey is what a card's detail was read for: a change to any of these
// adds to its history.
func ckTaskKey(c session.TaskRow) string {
	k := c.Session + "/" + c.ID + "/" + strconv.FormatInt(c.StageAt, 10)
	if c.Holder != nil {
		k += "/" + c.Holder.Window + strconv.FormatInt(c.Holder.HeldAt, 10)
	}
	if c.Blocked != nil {
		k += "/b" + strconv.FormatInt(c.Blocked.At, 10)
	}
	return k
}

// cockpitCardFetch reads the selected card's prompt and history when the
// Card tab shows it and none are read for its current state.
func (m *OS) cockpitCardFetch() tea.Cmd {
	ck := &m.Inbox.ck
	ck.cardWant = false
	_, c, ok := m.cockpitCard()
	if !ok || m.cockpitTab() != "Card" || (m.DaemonClient == nil && m.Inbox.call == nil) {
		return nil
	}
	key := ckTaskKey(c)
	if d := ck.card; d != nil && d.key == key {
		return nil
	}
	ck.card = &ckTaskDetail{key: key, loading: true}
	call, sess, id := m.inboxCaller(), c.Session, c.ID
	return func() tea.Msg {
		raw, err := call("get-task", map[string]any{"session": sess, "id": id}, threadReadTimeout)
		msg := ckTaskMsg{key: key, err: err}
		if err == nil {
			var res struct {
				Prompt  string              `json:"prompt"`
				History []session.TaskEvent `json:"history"`
			}
			msg.err = json.Unmarshal(raw, &res)
			msg.prompt, msg.history = res.Prompt, res.History
		}
		return msg
	}
}

// cockpitNoteCard is called while a frame draws the Card tab: a card changed
// by an event is read after the frame (cardWant), never in it.
func (m *OS) cockpitNoteCard(c session.TaskRow) {
	if d := m.Inbox.ck.card; d == nil || d.key != ckTaskKey(c) {
		m.Inbox.ck.cardWant = true
	}
}

// applyCard takes a get-task reply.
func (m *OS) applyCard(msg ckTaskMsg) {
	d := m.Inbox.ck.card
	if d == nil || d.key != msg.key {
		return
	}
	d.loading = false
	d.prompt, d.history, d.err = msg.prompt, msg.history, ""
	if msg.err != nil {
		verr, ok := errors.AsType[*session.VerbCallError](msg.err)
		switch {
		case ok && verr.Code == session.ErrVerbUnknownVerb:
			d.err = "This daemon has no task cards. " + m.restartHint()
		default:
			d.err = "tuios cannot read the card now: " + printableTitle(msg.err.Error())
		}
	}
	m.cockpitTouch()
}

// ckCardHints are the footer's keys on a card row.
func (m *OS) ckCardHints(c session.TaskRow) []overlay.Hint {
	var hints []overlay.Hint
	if c.Holder != nil {
		hints = append(hints, m.keyHints(config.ActionInboxGo, "open pane")...)
	}
	if m.ckTakeWindow(c) != "" && (c.Holder == nil || c.Holder.Window != m.ckTakeWindow(c)) && c.Stage != session.TaskStageDone {
		hints = append(hints, m.keyHints(config.ActionInboxTake, "take")...)
	}
	return hints
}

// cockpitCardGo is enter on a card: the holder's pane, or the Card tab on a
// narrow screen for a free card.
func (m *OS) cockpitCardGo(r ckRow, c session.TaskRow) tea.Cmd {
	switch {
	case r.agent >= 0:
		return m.InboxAgentsGo()
	case c.Holder != nil:
		m.CloseInbox()
		m.inboxJump(session.AttentionItem{Session: c.Session, Window: c.Holder.Window})
		return nil
	}
	return m.CockpitOpenFocus(true)
}

// drawCardRow draws a card's row: its mark, title and age, and on the second
// line its holder, or Blocked and why, or free.
func (m *OS) drawCardRow(c *ckCanvas, l ckLook, r ckRow, cursor, hover bool, x, y, w, top int) {
	ck := &m.Inbox.ck
	pal := l.pal
	right := x + w - 2
	card := ck.cards[r.card]
	ground := l.panel
	switch {
	case cursor:
		ground = l.cursor
	case hover:
		ground = l.hover
	}
	c.fill(x, y, w, r.lines, ground)
	defer c.mark(pal, x+1, y, w-1, r.lines, overlay.RowState{Cursor: cursor, Focused: cursor, Hover: hover})
	if cursor {
		for i := range r.lines {
			c.text(x, y+i, 0, ckSeg{t: "▌", fg: pal.Accent})
		}
	}
	ck.hits = append(ck.hits, ckHit{x0: x, y0: top + y, x1: x + w, y1: top + y + r.lines, kind: ckHitRow, key: r.key})
	ink := func(c color.Color) color.Color { return overlay.Readable(c, ground) }
	var e sidebarAgentEntry
	if r.agent >= 0 {
		e = ck.agents[r.agent]
	}
	holder, state := "", ""
	if card.Holder != nil {
		holder, state = printableTitle(card.Holder.Name), card.Holder.State
		if holder == "" {
			holder = shortWindowLabel(card.Holder.Window)
		}
	}
	if r.agent >= 0 {
		state = e.State
		if t := printableTitle(e.Title); t != "" {
			holder = t
		}
	}
	g, gInk := l.g(ckGlyphIdle), pal.FgMute
	switch {
	case card.Blocked != nil:
		g, gInk = l.g(ckGlyphFailed), pal.Warn
	case card.Holder != nil:
		g, gInk = l.ckMark(state, e.DoneSeen)
	}
	c.text(x+2, y, right, ckSeg{t: g, fg: gInk})
	nx := x + 2 + agentMarkLead()
	if r.lines != 1 || w >= 48 {
		nx = max(nx, x+5)
	}
	rx := c.textR(right, y, ckSeg{t: inboxWait(card.StageAt, time.Now()), fg: ink(pal.FgMute)})
	title := printableTitle(card.Title)
	if r.lines == 1 {
		// One line: the title to a fixed width, then the holder.
		tw := max(min(rx-1-nx-10, 30), 8)
		c.text(nx, y, nx+tw, ckSeg{t: ckTrunc(title, tw-1), fg: ink(pal.Fg), attrs: ckBold})
		who, whoInk := "free", pal.FgMute
		switch {
		case card.Blocked != nil:
			who, whoInk = "blocked", pal.Warn
		case holder != "":
			who, whoInk = holder, pal.FgDim
		case card.Stage == session.TaskStageDone:
			who = "done"
		}
		c.text(nx+tw, y, rx-1, ckSeg{t: ckTrunc(who, rx-1-nx-tw), fg: ink(whoInk)})
		return
	}
	c.text(nx, y, rx-1, ckSeg{t: ckTrunc(title, rx-1-nx), fg: ink(pal.Fg), attrs: ckBold})
	rx2 := c.textR(right, y+1, ckSeg{t: card.ID, fg: ink(pal.FgMute)}) - 2
	switch {
	case card.Blocked != nil:
		lx := c.text(nx, y+1, rx2, ckSeg{t: "Blocked", fg: ink(pal.Warn), attrs: ckBold}) + 2
		c.text(lx, y+1, rx2, ckSeg{t: ckTrunc(printableTitle(card.Blocked.Reason), rx2-lx), fg: ink(pal.FgDim)})
	case card.Holder != nil:
		lx := c.text(nx, y+1, rx2, ckSeg{t: ckPad(ckTrunc(holder, 12), 13), fg: ink(pal.Fg)})
		harness := ckHarness(card.Holder.Harness)
		if r.agent >= 0 {
			harness = ckHarness(e.Harness)
		}
		if card.Session != m.SessionName {
			// A card of another session says where its holder is.
			harness = ckTrunc(printableTitle(card.Session), 9)
		}
		lx = c.text(lx, y+1, rx2, ckSeg{t: ckPad(harness, 9), fg: ink(pal.FgDim)})
		if r.agent >= 0 {
			verb, rest, verbInk, _ := m.ckRowLine2(l, ckRow{kind: ckRowAgent, group: ckWorking, agent: r.agent}, e)
			lx = c.text(lx, y+1, rx2, ckSeg{t: verb, fg: ink(verbInk), attrs: ckBold})
			if rest != "" {
				if verb != "" {
					lx += 2
				}
				c.text(lx, y+1, rx2, ckSeg{t: ckTrunc(rest, rx2-lx), fg: ink(pal.FgDim)})
			}
		}
	case card.Stage == session.TaskStageDone:
		c.text(nx, y+1, rx2, ckSeg{t: "done", fg: ink(pal.FgMute)})
	default:
		c.text(nx, y+1, rx2, ckSeg{t: "free", fg: ink(pal.FgMute)})
	}
}

// drawCardFocus draws the Card tab: the card's title, stage, holder and
// block, its prompt, and its history.
func (m *OS) drawCardFocus(c *ckCanvas, l ckLook, r ckRow, card session.TaskRow, x, y, w, y1, top int, now time.Time) {
	st := &m.Inbox
	pal := l.pal
	right := x + w
	c.text(x, y, right, ckSeg{t: printableTitle(card.Title), fg: pal.Fg, attrs: ckBold})
	c.text(x, y+1, right, ckSeg{t: ckStageTitle(r.group), fg: pal.FgDim},
		ckSeg{t: sepWord() + inboxWait(card.StageAt, now) + sepWord() + printableTitle(card.Session) + " " + card.ID, fg: pal.FgMute})
	rule := y + 3
	if c.h < 30 {
		rule = y + 2
	}
	var e sidebarAgentEntry
	if r.agent >= 0 {
		e = st.ck.agents[r.agent]
	}
	rule = m.drawFocusTabs(c, l, m.cockpitTabs(e, r), x, w, rule, top)
	by := rule + 2
	if c.h < 30 {
		by = rule + 1
	}
	line := func(segs ...ckSeg) {
		if by < y1 {
			c.text(x, by, right, segs...)
		}
		by++
	}
	switch {
	case card.Holder != nil:
		who := printableTitle(card.Holder.Name)
		if who == "" {
			who = shortWindowLabel(card.Holder.Window)
		}
		line(ckSeg{t: "Held by ", fg: pal.FgDim}, ckSeg{t: who, fg: pal.Fg, attrs: ckBold},
			ckSeg{t: "  " + inboxWait(card.Holder.HeldAt, now), fg: pal.FgMute})
	case card.Stage != session.TaskStageDone && card.Blocked == nil:
		line(ckSeg{t: "Free. ", fg: pal.FgDim}, ckSeg{t: m.ckFreeWords(card), fg: pal.FgMute})
	}
	if b := card.Blocked; b != nil {
		line(ckSeg{t: "Blocked ", fg: pal.Warn, attrs: ckBold}, ckSeg{t: printableTitle(b.Reason), fg: pal.FgDim})
	}
	m.cockpitNoteCard(card)
	d := st.ck.card
	if d == nil || d.key != ckTaskKey(card) || d.loading {
		by++
		line(ckSeg{t: "Reading...", fg: pal.FgMute})
		return
	}
	if d.err != "" {
		by++
		line(ckSeg{t: d.err, fg: pal.Warning})
		return
	}
	if d.prompt != "" {
		by++
		line(ckSeg{t: "Prompt", fg: pal.FgDim, attrs: ckBold})
		for para := range strings.SplitSeq(d.prompt, "\n") {
			for s := range strings.SplitSeq(ansi.Wordwrap(printableTitle(para), max(w, 8), ""), "\n") {
				line(ckSeg{t: s, fg: pal.Fg})
			}
		}
	}
	if len(d.history) > 0 {
		by++
		line(ckSeg{t: "History", fg: pal.FgDim, attrs: ckBold})
		for _, ev := range slices.Backward(d.history) {
			words := ev.Action
			switch {
			case ev.Stage != "" && ev.Action == session.TaskActionStaged:
				words += " to " + ev.Stage
			case ev.Reason != "":
				words += ", " + printableTitle(ev.Reason)
			}
			line(ckSeg{t: ckPad(inboxWait(ev.At, now), 5), fg: pal.FgMute}, ckSeg{t: words, fg: pal.Fg},
				ckSeg{t: "  " + printableTitle(ev.By), fg: pal.FgMute})
		}
	}
}

// ckFreeWords says how a free card is taken.
func (m *OS) ckFreeWords(c session.TaskRow) string {
	if m.ckTakeWindow(c) != "" {
		return m.inboxKeyOr(config.ActionInboxTake, "t") + " takes it for the focused pane, " +
			m.inboxKeyOr(config.ActionInboxNewAgent, "n") + " starts an agent on it."
	}
	return "A pane takes it with tuios task claim " + c.ID + "."
}

// ckBoardEmptyLines is the board's empty state.
func (m *OS) ckBoardEmptyLines() []string {
	st := &m.Inbox
	back := m.inboxKeyOr(config.ActionInboxBoard, "b") + " shows the agents again."
	switch {
	case st.ck.noBoard:
		return []string{"This daemon has no task board.", m.restartHint()}
	case st.ck.boardErr != "":
		return []string{"tuios cannot list the cards: " + printableTitle(st.ck.boardErr), back}
	case m.ckScope() != "" && m.ckScope() != "local":
		return []string{"The board lists only the cards on this machine.", "It does not list the cards on " + ckHostLabel(m.ckScope()) + ". " + back}
	case st.Select != "" || st.agents.query != "":
		return []string{"No card matches the search.", "/ changes the search. " + back}
	}
	return []string{"No task cards yet.", "Add one with tuios task add. " + back}
}

// ckAgentCounts are the header's counts while the board is up: the agents by
// the group their state puts them in, as the fleet would count them.
func (m *OS) ckAgentCounts() [ckGroups]int {
	var n [ckGroups]int
	for _, e := range m.Inbox.ck.agents {
		switch sidebarAgentGroup(e.State, e.DoneSeen) {
		case sidebarGroupNeedsYou:
			n[ckNeeds]++
		case sidebarGroupFinished:
			n[ckReview]++
		case sidebarGroupWorking:
			n[ckWorking]++
		default:
			n[ckIdle]++
		}
	}
	return n
}
