package app

import (
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The board as columns: one column per stage [tasks] shows, side by side,
// each with its cards. It is the board's default; B shows the list of
// cockpit_board.go instead, and under ckOneColumn the list is drawn whatever
// the choice. The rows are the list's rows in another order: a head per
// stage, then its cards, so the cursor, t, enter and the Card reads work on
// the same rows. h and l move between columns, j and k inside one, H and L
// move the card a stage, and a card dragged onto another column moves there.
// Nothing here runs a timer: every change is a key, a click or a task event.

// The widths of a column: the narrowest a column of cards is drawn, and the
// widest, past which the columns leave the rest of the row empty.
const (
	kbMinW = 26
	kbMaxW = 48
)

// ckKanban is the columns' own state.
type ckKanban struct {
	// first is the first column drawn when they do not all fit, by its
	// place among the stages shown.
	first int
	// scroll is each column's first card drawn, by stage.
	scroll map[string]int
	// cols are the columns of the last frame, for the mouse.
	cols []kbCol
	// drag is the card a press on it may drag, nil when none.
	drag *kbDrag
	// add is the line a new card's title is typed into, nil when closed.
	add *kbNewCard
}

// kbCol is a column as the last frame drew it, in screen cells.
type kbCol struct{ g, x0, x1 int }

// kbDrag is a press on a card, and where the pointer took it since.
type kbDrag struct {
	key          string
	from         int
	x0, y0, x, y int
	// moving says the pointer left the cell it was pressed on: the press
	// is a drag, not a click.
	moving bool
	// over is the stage of the column under the pointer, -1 for none.
	over int
}

// kbNewCard is the line a new card's title is typed into.
type kbNewCard struct {
	session, stage string
	ed             *composer.Model
}

// kbPlace is where a column is drawn.
type kbPlace struct {
	i, x, w int
	slim    bool
}

// ckCardMsg is the reply to a move or an add from the columns.
type ckCardMsg struct {
	row    session.TaskRow
	err    error
	failed string
	// focus puts the cursor on the card.
	focus bool
}

// kanbanOn reports whether the board is drawn as columns.
func (m *OS) kanbanOn() bool {
	ck := &m.Inbox.ck
	return ck.board && !ck.boardList && m.GetRenderWidth() >= ckOneColumn
}

// kanbanDrawable reports whether the columns have cards to show, and not a
// word about why there are none.
func (m *OS) kanbanDrawable() bool {
	st := &m.Inbox
	scope := m.ckScope()
	return !st.Unsupported && m.IsDaemonSession && !st.ck.noBoard && st.ck.boardErr == "" && (scope == "" || scope == "local")
}

// kbShown is the stages [tasks] shows, by their place in TaskStages.
func (m *OS) kbShown() []int {
	var out []int
	for g, s := range session.TaskStages {
		if m.ckStageShows(s) {
			out = append(out, g)
		}
	}
	return out
}

// buildKanbanRows lays the stages out as columns: a head for every stage
// shown, then its cards unless the column is folded. Done lists the newest
// first, since it only grows.
func (m *OS) buildKanbanRows(list []sidebarAgentEntry) []ckRow {
	ck := &m.Inbox.ck
	items := m.boardStageItems(list)
	var rows []ckRow
	for _, g := range m.kbShown() {
		stage := session.TaskStages[g]
		cards := items[g]
		if stage == session.TaskStageDone {
			// Oldest first within each rank, so reversed the newest
			// leads. A sort of its own would add a sort to the binary.
			slices.Reverse(cards)
		}
		head := ckRow{kind: ckRowHead, group: g, agent: -1, count: len(cards), lines: 1, key: "bh:" + stage}
		head.folded = ck.stageFolded(g)
		head.rest = len(cards) == 0
		if head.folded {
			head.hidden = cards
			rows = append(rows, head)
			continue
		}
		rows = append(rows, head)
		for _, r := range cards {
			r.lines = 1
			rows = append(rows, r)
		}
	}
	return rows
}

// kbColumn is the place among the stages shown of the cursor's column, 0
// when the cursor is on none.
func (m *OS) kbColumn() int {
	r, ok := m.cockpitSelected()
	if !ok {
		return 0
	}
	return max(slices.Index(m.kbShown(), r.group), 0)
}

// kbColumnRows are the rows the cursor can rest on in the column of stage
// g, by their index in the rows.
func (m *OS) kbColumnRows(g int) []int {
	var out []int
	for i, r := range m.cockpitRows() {
		if r.group == g && r.cursorable() {
			out = append(out, i)
		}
	}
	return out
}

// kbSelect puts the cursor on row i of the rows.
func (m *OS) kbSelect(i int) {
	ck := &m.Inbox.ck
	m.cockpitSetCursor(i)
	ck.pending = ""
	ck.follow = true
	m.cockpitTouch()
	m.cockpitReplyFollow()
	m.cockpitSync()
}

// kanbanMoveColumn moves the cursor d columns, to the card at the same
// place in the new column, or its last.
func (m *OS) kanbanMoveColumn(d int) {
	shown := m.kbShown()
	if len(shown) == 0 {
		return
	}
	ck := &m.Inbox.ck
	cur := m.kbColumn()
	t := min(max(cur+d, 0), len(shown)-1)
	if t == cur {
		return
	}
	pos := max(slices.Index(m.kbColumnRows(shown[cur]), ck.idx), 0)
	rows := m.kbColumnRows(shown[t])
	if len(rows) == 0 {
		return
	}
	m.kbSelect(rows[min(pos, len(rows)-1)])
}

// kanbanFocusColumn puts the cursor on the column of stage g, on its first
// card.
func (m *OS) kanbanFocusColumn(g int) {
	if rows := m.kbColumnRows(g); len(rows) > 0 {
		m.kbSelect(rows[0])
	}
}

// kanbanMoveCard moves the cursor d cards inside its column.
func (m *OS) kanbanMoveCard(d int) {
	r, ok := m.cockpitSelected()
	if !ok {
		if shown := m.kbShown(); len(shown) > 0 {
			m.kanbanFocusColumn(shown[0])
		}
		return
	}
	rows := m.kbColumnRows(r.group)
	at := slices.Index(rows, m.Inbox.ck.idx)
	if at < 0 || len(rows) == 0 {
		return
	}
	m.kbSelect(rows[min(max(at+d, 0), len(rows)-1)])
}

// kanbanStep moves the card under the cursor to the shown stage before or
// after its own.
func (m *OS) kanbanStep(next bool) tea.Cmd {
	_, c, ok := m.cockpitCard()
	if !ok {
		return nil
	}
	g := slices.Index(session.TaskStages, c.Stage)
	for {
		if next {
			g++
		} else {
			g--
		}
		if g < 0 || g >= len(session.TaskStages) {
			return nil
		}
		if m.ckStageShows(session.TaskStages[g]) {
			return m.kanbanSetStage(c, session.TaskStages[g])
		}
	}
}

// kanbanSetStage moves card c to stage with set-task-stage. The reply and
// the event stream both bring the moved row.
func (m *OS) kanbanSetStage(c session.TaskRow, stage string) tea.Cmd {
	if c.Stage == stage {
		return nil
	}
	call, nonce := m.inboxCaller(), m.inboxNonce()
	return func() tea.Msg {
		raw, err := call("set-task-stage", map[string]any{"session": c.Session, "id": c.ID, "stage": stage, "human_nonce": nonce}, 5*time.Second)
		msg := ckCardMsg{err: err, failed: "The card did not move: "}
		if err == nil {
			msg.err = jsonx.Unmarshal(raw, &msg.row)
		}
		return msg
	}
}

// applyCardReply takes the reply to a move or an add.
func (m *OS) applyCardReply(msg ckCardMsg) {
	ck := &m.Inbox.ck
	if msg.err != nil {
		m.ShowNotification(msg.failed+printableTitle(msg.err.Error()), "error", m.Settings.NotificationDuration)
		return
	}
	m.applyTaskEvent(session.TaskActionStaged, msg.row)
	if msg.focus {
		ck.key, ck.akey = "t:"+msg.row.Session+"/"+msg.row.ID, ""
	}
	ck.follow = true
	m.cockpitTouchRows()
	m.inboxChanged()
}

// kanbanAddSession is the session a new card goes to: the one this client
// is attached to, "" when it is not attached to one here.
func (m *OS) kanbanAddSession() string {
	if m.AttachedHost != "" || !m.IsDaemonSession {
		return ""
	}
	return m.SessionName
}

// kanbanNewCard is n: the line for a new card's title, in the cursor's
// column. A card is never added to Done: n there adds to Todo.
func (m *OS) kanbanNewCard() tea.Cmd {
	ck := &m.Inbox.ck
	sess := m.kanbanAddSession()
	if sess == "" {
		m.ShowNotification("A new card goes to the session you are attached to. Attach to a session to add one.", "info", m.Settings.NotificationDuration)
		return nil
	}
	stage := session.TaskStageTodo
	if r, ok := m.cockpitSelected(); ok && session.TaskStages[r.group] != session.TaskStageDone {
		stage = session.TaskStages[r.group]
	}
	if !m.ckStageShows(stage) {
		if shown := m.kbShown(); len(shown) > 0 {
			stage = session.TaskStages[shown[0]]
		}
	}
	ed := newLineEditor(200, "Type the title. Press enter to add the card.")
	ck.kb.add = &kbNewCard{session: sess, stage: stage, ed: ed}
	m.kanbanFocusColumn(slices.Index(session.TaskStages, stage))
	m.cockpitTouch()
	return nil
}

// CockpitNewCardOpen reports whether the line for a new card is open, where
// every printable key is text.
func (m *OS) CockpitNewCardOpen() bool { return m.Inbox.ck.kb.add != nil }

// CockpitNewCardKey gives a key to the new card's line: enter adds the card,
// esc closes the line, and every other key edits it.
func (m *OS) CockpitNewCardKey(msg tea.KeyPressMsg) tea.Cmd {
	ck := &m.Inbox.ck
	a := ck.kb.add
	if a == nil {
		return nil
	}
	defer m.cockpitTouch()
	switch a.ed.Update(msg, m.ProcessingRemoteKeys) {
	case composer.EventSend:
		return m.kanbanAddSend()
	case composer.EventClose:
		ck.kb.add = nil
	case composer.EventPaste:
		return m.RequestHostPaste()
	}
	return nil
}

// CockpitNewCardPaste puts a paste into the new card's line, on one line.
func (m *OS) CockpitNewCardPaste(text string) {
	if a := m.Inbox.ck.kb.add; a != nil && text != "" {
		a.ed.Paste(text, m.ProcessingRemoteKeys)
		m.cockpitTouch()
	}
}

// kanbanAddSend adds the typed card with add-task and closes the line. An
// empty line adds nothing.
func (m *OS) kanbanAddSend() tea.Cmd {
	ck := &m.Inbox.ck
	a := ck.kb.add
	text, ok := a.ed.Submit()
	if !ok {
		// Invisible characters were removed; the line says so.
		return nil
	}
	ck.kb.add = nil
	title := strings.TrimSpace(text)
	if title == "" {
		return nil
	}
	call, nonce := m.inboxCaller(), m.inboxNonce()
	return func() tea.Msg {
		raw, err := call("add-task", map[string]any{"session": a.session, "title": title, "stage": a.stage, "human_nonce": nonce}, 5*time.Second)
		msg := ckCardMsg{err: err, failed: "The card was not added: ", focus: true}
		if err == nil {
			msg.err = jsonx.Unmarshal(raw, &msg.row)
		}
		return msg
	}
}

// kanbanKey handles the board's own keys. B switches the layout and H and L
// move a card in both layouts; the rest act only on the columns. handled
// is false for a key the cockpit handles as it always does.
func (m *OS) kanbanKey(action string) (tea.Cmd, bool) {
	ck := &m.Inbox.ck
	if !ck.board {
		return nil, false
	}
	switch action {
	case config.ActionInboxBoardLayout:
		ck.boardList = !ck.boardList
		ck.kb.add, ck.kb.drag = nil, nil
		ck.scroll, ck.follow = 0, true
		m.cockpitTouchRows()
		m.saveSidebarState()
		return nil, true
	case config.ActionInboxStagePrev, config.ActionInboxStageNext:
		return m.kanbanStep(action == config.ActionInboxStageNext), true
	}
	if !m.kanbanOn() {
		return nil, false
	}
	switch action {
	case config.ActionInboxBack:
		m.kanbanMoveColumn(-1)
	case config.ActionInboxOpen:
		m.kanbanMoveColumn(1)
	case config.ActionInboxUp:
		m.kanbanMoveCard(-1)
	case config.ActionInboxDown:
		m.kanbanMoveCard(1)
	case config.ActionInboxPageUp:
		m.kanbanMoveCard(-10)
	case config.ActionInboxPageDown:
		m.kanbanMoveCard(10)
	case config.ActionInboxFirst:
		m.kanbanMoveCard(-1 << 20)
	case config.ActionInboxLast:
		m.kanbanMoveCard(1 << 20)
	case config.ActionInboxNewAgent:
		return m.kanbanNewCard(), true
	case config.ActionInboxGo:
		r, ok := m.cockpitSelected()
		switch {
		case !ok:
		case r.kind == ckRowHead && r.folded:
			m.CockpitFold()
		case r.kind == ckRowCard:
			return m.cockpitCardGo(r, ck.cards[r.card]), true
		}
	case config.ActionInboxTabPrev, config.ActionInboxTabNext, config.ActionInboxExpand,
		config.ActionInboxDetailDown, config.ActionInboxDetailUp:
		// The columns have no focus area for these to act on.
	default:
		return nil, false
	}
	return nil, true
}

// kanbanPress arms a drag of the card a press landed on.
func (m *OS) kanbanPress(h ckHit, x, y int) {
	ck := &m.Inbox.ck
	ck.kb.drag = nil
	if !m.kanbanOn() {
		return
	}
	for _, r := range ck.rows {
		if r.key == h.key && r.kind == ckRowCard {
			ck.kb.drag = &kbDrag{key: h.key, from: r.group, x0: x, y0: y, x: x, y: y, over: -1}
			return
		}
	}
}

// kanbanColumnAt is the stage of the column at screen column x, -1 for
// none.
func (m *OS) kanbanColumnAt(x int) int {
	for _, c := range m.Inbox.ck.kb.cols {
		if x >= c.x0 && x < c.x1 {
			return c.g
		}
	}
	return -1
}

// kanbanDragTo follows the pointer while a press on a card is down. It
// reports whether a drag took the motion.
func (m *OS) kanbanDragTo(x, y int) bool {
	d := m.Inbox.ck.kb.drag
	if d == nil {
		return false
	}
	if x == d.x && y == d.y {
		return true
	}
	d.x, d.y = x, y
	d.moving = d.moving || x != d.x0 || y != d.y0
	d.over = m.kanbanColumnAt(x)
	m.Inbox.ck.hover = ""
	m.cockpitTouch()
	return true
}

// CockpitRelease ends a press on a card: a drag to another column moves the
// card to its stage. handled is false when no drag was under way, and the
// release goes on as before.
func (m *OS) CockpitRelease(x, y int) (tea.Cmd, bool) {
	ck := &m.Inbox.ck
	d := ck.kb.drag
	if d == nil {
		return nil, false
	}
	ck.kb.drag = nil
	m.cockpitTouch()
	if !d.moving || !m.ShowInbox {
		return nil, false
	}
	g := m.kanbanColumnAt(x)
	if g < 0 {
		return nil, true
	}
	for _, r := range ck.rows {
		if r.key == d.key && r.kind == ckRowCard && r.card < len(ck.cards) {
			m.cockpitSelectKey(d.key)
			return m.kanbanSetStage(ck.cards[r.card], session.TaskStages[g]), true
		}
	}
	return nil, true
}

// kanbanWheel scrolls the column under the pointer by a card. It reports
// whether the columns took the wheel.
func (m *OS) kanbanWheel(x, _ int, up bool) bool {
	if !m.kanbanOn() {
		return false
	}
	ck := &m.Inbox.ck
	g := m.kanbanColumnAt(x)
	if g < 0 {
		return true
	}
	if ck.kb.scroll == nil {
		ck.kb.scroll = map[string]int{}
	}
	s := session.TaskStages[g]
	if up {
		ck.kb.scroll[s] = max(ck.kb.scroll[s]-1, 0)
	} else {
		ck.kb.scroll[s]++
	}
	ck.follow = false
	m.cockpitTouch()
	return true
}

// kanbanHints are the footer's keys on the columns.
func (m *OS) kanbanHints() []overlay.Hint {
	ck := &m.Inbox.ck
	if ck.kb.add != nil {
		return []overlay.Hint{{Key: overlay.EnterKey(), Label: "add card"}, {Key: "esc", Label: "cancel", Priority: overlay.HintEssential}}
	}
	var hints []overlay.Hint
	r, ok := m.cockpitSelected()
	switch {
	case ok && r.kind == ckRowCard:
		c := ck.cards[r.card]
		switch {
		case c.Holder != nil:
			hints = append(hints, m.keyHints(config.ActionInboxGo, "open pane")...)
		case c.Stage != session.TaskStageDone && c.Blocked == nil:
			hints = append(hints, m.keyHints(config.ActionInboxGo, "start agent")...)
		}
		if prev, next := m.inboxKey(config.ActionInboxStagePrev), m.inboxKey(config.ActionInboxStageNext); prev != "" && next != "" {
			hints = append(hints, overlay.Hint{Key: prev + " " + next, Label: "move card"})
		}
		if win := m.ckTakeWindow(c); win != "" && (c.Holder == nil || c.Holder.Window != win) && c.Stage != session.TaskStageDone {
			hints = append(hints, m.keyHints(config.ActionInboxTake, "take")...)
		}
	case ok && r.folded:
		hints = append(hints, m.keyHints(config.ActionInboxFold, "open column")...)
	}
	hints = append(hints, m.keyHints(config.ActionInboxNewAgent, "new card", config.ActionInboxBoardLayout, "list",
		config.ActionInboxBoard, "agents", config.ActionInboxSelect, "search")...)
	hints = append(hints, overlay.Hint{Key: "?", Label: "all keys"})
	return append(hints, m.keyHints(config.ActionInboxClose, "close")...)
}

// kbTitleLines is a card's title wrapped to w cells, two lines at most.
func kbTitleLines(title string, w int) []string {
	lines := strings.Split(ansi.Wordwrap(title, max(w, 4), ""), "\n")
	if len(lines) > 2 {
		lines = []string{lines[0], strings.Join(lines[1:], " ")}
	}
	for i := range lines {
		lines[i] = ckTrunc(lines[i], w)
	}
	return lines
}

// kanbanPlace lays the columns out across w cells: the cursor's column and
// every column with cards at kbMinW or wider, the folded and the empty ones
// as narrow as their head. When they do not fit, the run of columns that
// holds the cursor's is drawn, and left and right count the columns past
// each edge.
func (m *OS) kanbanPlace(heads []ckRow, cur, w int) (places []kbPlace, left, right int) {
	ck := &m.Inbox.ck
	n := len(heads)
	if n == 0 {
		return nil, 0, 0
	}
	wide := false
	slim := func(i int) bool {
		h := heads[i]
		return i != cur && (h.folded || h.count == 0 && !wide)
	}
	slimW := func(i int) int {
		return ckWidth(ckSeg{t: ckStageTitle(heads[i].group) + "  " + strconv.Itoa(heads[i].count)}) + 4
	}
	need := func(i int) int {
		if slim(i) {
			return slimW(i)
		}
		return kbMinW
	}
	room := func(a, b int) int {
		r := w - 2 - (b - a)
		if a > 0 {
			r -= 2
		}
		if b < n-1 {
			r -= 2
		}
		return r
	}
	fits := func(a, b int) bool {
		sum := 0
		for i := a; i <= b; i++ {
			sum += need(i)
		}
		return sum <= room(a, b)
	}
	first := min(max(ck.kb.first, 0), n-1)
	first = min(first, cur)
	last := first
	for {
		last = first
		for last+1 < n && fits(first, last+1) {
			last++
		}
		if cur <= last || first >= cur {
			break
		}
		first++
	}
	for first > 0 && fits(first-1, last) {
		first--
	}
	ck.kb.first = first
	// Share the room among the columns of cards. When they would pass
	// kbMaxW, the empty columns take cards' widths too.
	var fullW, extra int
	for pass := range 2 {
		wide = pass == 1
		slims, fulls := 0, 0
		for i := first; i <= last; i++ {
			if slim(i) {
				slims += slimW(i)
			} else {
				fulls++
			}
		}
		fullW = (room(first, last) - slims) / max(fulls, 1)
		extra = room(first, last) - slims - fullW*fulls
		if fullW <= kbMaxW {
			break
		}
	}
	if fullW > kbMaxW {
		fullW, extra = kbMaxW, 0
	}
	x := 1
	if first > 0 {
		x += 2
	}
	for i := first; i <= last; i++ {
		p := kbPlace{i: i, x: x, w: fullW, slim: slim(i)}
		if p.slim {
			p.w = slimW(i)
		} else if extra > 0 {
			p.w++
			extra--
		}
		places = append(places, p)
		x += p.w + 1
	}
	return places, first, n - 1 - last
}

// drawKanban draws the columns between rows y0 and y1.
func (m *OS) drawKanban(c *ckCanvas, l ckLook, y0, y1, top int) {
	ck := &m.Inbox.ck
	kb := &ck.kb
	pal := l.pal
	rows := m.cockpitRows()
	sel, selOK := m.cockpitSelected()
	// The heads, and the cards under each.
	var heads []ckRow
	var cards [][]ckRow
	for _, r := range rows {
		switch r.kind {
		case ckRowHead:
			heads = append(heads, r)
			cards = append(cards, nil)
		case ckRowCard:
			if len(cards) > 0 {
				cards[len(cards)-1] = append(cards[len(cards)-1], r)
			}
		}
	}
	cur := 0
	for i, h := range heads {
		if selOK && h.group == sel.group {
			cur = i
		}
	}
	places, left, right := m.kanbanPlace(heads, cur, c.w)
	kb.cols = kb.cols[:0]
	if kb.scroll == nil {
		kb.scroll = map[string]int{}
	}
	total := 0
	for _, h := range heads {
		total += h.count
	}
	arrowL, arrowR, up, down := "‹", "›", "↑", "↓"
	if l.ascii {
		arrowL, arrowR, up, down = "<", ">", "^", "v"
	}
	if left > 0 {
		x := c.text(0, y0, 0, ckSeg{t: arrowL + strconv.Itoa(left), fg: pal.AccentBright, attrs: ckBold})
		ck.hits = append(ck.hits, ckHit{x0: 0, y0: top + y0, x1: x, y1: top + y0 + 1, kind: ckHitColumn, group: heads[places[0].i-1].group})
	}
	if right > 0 {
		x := c.textR(c.w, y0, ckSeg{t: strconv.Itoa(right) + arrowR, fg: pal.AccentBright, attrs: ckBold})
		ck.hits = append(ck.hits, ckHit{x0: x, y0: top + y0, x1: c.w, y1: top + y0 + 1, kind: ckHitColumn, group: heads[places[len(places)-1].i+1].group})
	}
	drag := kb.drag
	if drag != nil && !drag.moving {
		drag = nil
	}
	for _, p := range places {
		h := heads[p.i]
		stage := session.TaskStages[h.group]
		x, w := p.x, p.w
		focused := p.i == cur
		drop := drag != nil && drag.over == h.group && drag.from != h.group
		kb.cols = append(kb.cols, kbCol{g: h.group, x0: x, x1: x + w})
		// The head: the stage and its count. The cursor rests on it in
		// an empty or a folded column.
		headCursor := selOK && sel.key == h.key
		headGround := l.canvas
		if headCursor {
			headGround = l.cursor
		}
		c.fill(x, y0, w, 1, headGround)
		titleInk := pal.FgDim
		switch {
		case drop:
			titleInk = pal.AccentBright
		case focused:
			titleInk = pal.Fg
		}
		hx := c.text(x+1, y0, x+w, ckSeg{t: ckStageTitle(h.group), fg: overlay.Readable(titleInk, headGround), attrs: ckBold},
			ckSeg{t: "  " + strconv.Itoa(h.count), fg: overlay.Readable(pal.FgMute, headGround)})
		if h.folded {
			c.text(hx+1, y0, x+w, ckSeg{t: l.g(ckGlyphFold), fg: overlay.Readable(pal.FgMute, headGround)})
		}
		c.mark(pal, x, y0, w, 1, overlay.RowState{Cursor: headCursor, Focused: headCursor})
		ck.hits = append(ck.hits, ckHit{x0: x, y0: top + y0, x1: x + w, y1: top + y0 + 1, kind: ckHitHead, group: h.group})
		ruleInk := l.edge
		if focused || drop {
			ruleInk = pal.Accent
		}
		c.hline(x, y0+1, w, hzGlyph(), ruleInk)
		by := y0 + 2
		if drop {
			c.fill(x, by, w, y1-by, l.hover)
			c.mark(pal, x, by, w, y1-by, overlay.RowState{Hover: true})
		}
		if a := kb.add; a != nil && focused {
			by = m.drawKanbanAdd(c, l, a, x, by, w)
		}
		col := cards[p.i]
		switch {
		case h.folded:
			if !p.slim {
				words := strconv.Itoa(h.count) + " cards are folded."
				if key := m.inboxKey(config.ActionInboxFold); key != "" {
					words += " Press " + key + " to open them."
				}
				for i, line := range wrapPlain(words, w-2) {
					c.text(x+1, by+i, x+w, ckSeg{t: line, fg: pal.FgMute})
				}
			}
		case len(col) == 0 && !p.slim:
			c.text(x+1, by, x+w, ckSeg{t: "No cards", fg: pal.FgMute})
			if total == 0 && kb.add == nil && m.kanbanAddSession() != "" {
				if key := m.inboxKey(config.ActionInboxNewAgent); key != "" {
					c.text(x+1, by+1, x+w, ckSeg{t: "Press " + key + " to add one.", fg: pal.FgMute})
				}
			}
		}
		// The cards, from the column's scroll, the cursor's card in view.
		inner := w - 3
		heights := make([]int, len(col))
		titles := make([][]string, len(col))
		for j, r := range col {
			titles[j] = kbTitleLines(printableTitle(ck.cards[r.card].Title), inner)
			heights[j] = len(titles[j]) + 1
		}
		scroll := min(max(kb.scroll[stage], 0), max(len(col)-1, 0))
		if at := slices.IndexFunc(col, func(r ckRow) bool { return selOK && r.key == sel.key }); at >= 0 && ck.follow {
			scroll = min(scroll, at)
			for scroll < at {
				span := 0
				for j := scroll; j <= at; j++ {
					span += heights[j] + 1
				}
				if by+span-1 <= y1 {
					break
				}
				scroll++
			}
		}
		kb.scroll[stage] = scroll
		y, shown := by, scroll
		for j := scroll; j < len(col); j++ {
			if y+heights[j] > y1 {
				break
			}
			r := col[j]
			cursor := selOK && r.key == sel.key
			m.drawKanbanCard(c, l, r, titles[j], x, y, w, heights[j], cursor, r.key == ck.hover && drag == nil, top)
			y += heights[j] + 1
			shown = j + 1
		}
		// What the scroll hides, on the rule.
		var more []ckSeg
		if scroll > 0 {
			more = append(more, ckSeg{t: " " + up + strconv.Itoa(scroll), fg: pal.FgMute})
		}
		if n := len(col) - shown; n > 0 {
			more = append(more, ckSeg{t: " " + down + strconv.Itoa(n), fg: pal.FgMute})
		}
		if len(more) > 0 {
			more = append(more, ckSeg{t: " "})
			c.textR(x+w-1, y0+1, more...)
		}
		ck.hits = append(ck.hits, ckHit{x0: x, y0: top + y0, x1: x + w, y1: top + y1, kind: ckHitColumn, group: h.group})
	}
	// The card under a drag follows the pointer.
	if drag != nil {
		for _, r := range rows {
			if r.key == drag.key && r.kind == ckRowCard {
				t := " " + ckTrunc(printableTitle(ck.cards[r.card].Title), 28) + " "
				gx, gy := min(drag.x+1, c.w-ckWidth(ckSeg{t: t})), drag.y-top
				end := c.text(gx, gy, 0, ckSeg{t: t, fg: pal.PillFg, bg: pal.Accent, attrs: ckBold})
				c.mark(pal, gx, gy, end-gx, 1, ckOn)
				break
			}
		}
	}
}

// drawKanbanAdd draws the line a new card's title is typed into at y, and
// returns the row under it.
func (m *OS) drawKanbanAdd(c *ckCanvas, l ckLook, a *kbNewCard, x, y, w int) int {
	pal := l.pal
	c.fill(x, y, w, 3, l.cursor)
	c.text(x+2, y, x+w-1, ckSeg{t: "New card", fg: overlay.Readable(pal.FgDim, l.cursor), attrs: ckBold})
	if lines := a.ed.Render(max(w-4, 8), composerStyles(pal), l.cursor); len(lines) > 0 {
		c.draw(x+2, y+1, w-4, lines[0])
	}
	for i := range 3 {
		c.text(x, y+i, 0, ckSeg{t: "▌", fg: pal.Accent})
	}
	c.mark(pal, x+1, y, w-1, 3, overlay.RowState{Cursor: true, Focused: true})
	return y + 4
}

// drawKanbanCard draws one card: its title, then its holder's mark and name,
// or Blocked and why, or free, with its id and age on the right.
func (m *OS) drawKanbanCard(c *ckCanvas, l ckLook, r ckRow, title []string, x, y, w, h int, cursor, hover bool, top int) {
	ck := &m.Inbox.ck
	pal := l.pal
	card := ck.cards[r.card]
	ground := l.panel
	switch {
	case cursor:
		ground = l.cursor
	case hover:
		ground = l.hover
	}
	c.fill(x, y, w, h, ground)
	ink := func(fg color.Color) color.Color { return overlay.Readable(fg, ground) }
	bar, barInk := "▏", l.edgePanel
	if cursor {
		bar, barInk = "▌", pal.Accent
	}
	if cursor || pal.Depth == overlay.Depth16 {
		for i := range h {
			c.text(x, y+i, 0, ckSeg{t: bar, fg: barInk})
		}
	}
	right := x + w - 1
	for i, t := range title {
		c.text(x+2, y+i, right, ckSeg{t: t, fg: ink(pal.Fg), attrs: ckBold})
	}
	my := y + h - 1
	rx := c.textR(right, my, ckSeg{t: card.ID, fg: ink(pal.FgMute)}, ckSeg{t: "  " + inboxWait(card.StageAt, time.Now()), fg: ink(pal.FgMute)}) - 1
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
	lx := x + 2
	switch {
	case card.Blocked != nil:
		lx = c.text(lx, my, rx, ckSeg{t: l.g(ckGlyphFailed) + " ", fg: ink(pal.Warn)}, ckSeg{t: "Blocked", fg: ink(pal.Warn), attrs: ckBold}) + 1
		c.text(lx, my, rx, ckSeg{t: ckTrunc(printableTitle(card.Blocked.Reason), rx-lx), fg: ink(pal.FgDim)})
	case card.Holder != nil:
		g, gInk := l.ckMark(state, e.DoneSeen)
		lx = c.text(lx, my, rx, ckSeg{t: g + " ", fg: ink(gInk)})
		if card.Session != m.SessionName {
			holder += " in " + printableTitle(card.Session)
		}
		c.text(lx, my, rx, ckSeg{t: ckTrunc(holder, rx-lx), fg: ink(pal.Fg)})
	case card.Stage == session.TaskStageDone:
		c.text(lx, my, rx, ckSeg{t: l.ckStateMark("done", ckGlyphReview) + " ", fg: ink(pal.Success)}, ckSeg{t: "done", fg: ink(pal.FgMute)})
	default:
		c.text(lx, my, rx, ckSeg{t: l.ckStateMark("idle", ckGlyphIdle) + " ", fg: ink(pal.FgMute)}, ckSeg{t: "free", fg: ink(pal.FgMute)})
	}
	c.mark(pal, x+1, y, w-1, h, overlay.RowState{Cursor: cursor, Focused: cursor, Hover: hover})
	ck.hits = append(ck.hits, ckHit{x0: x, y0: top + y, x1: x + w, y1: top + y + h, kind: ckHitRow, key: r.key})
}
