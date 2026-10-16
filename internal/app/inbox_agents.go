package app

import (
	"cmp"
	"image/color"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

// The agents view of the Inbox: every agent tuios knows of, in one list, with
// what each one is doing and how long it has done it. tab in the Inbox
// switches between it and the attention list. Both share the Inbox's state:
// the selector line, the reply editor, and the key that opens it.
//
// The rows are the rail's agent model (sidebarAgents over BuildSessionTree):
// the attached session from live state, the others and the linked machines
// from the listings the client already keeps. One model, so the rail and this
// list cannot disagree about an agent. Nothing here polls. The list changes
// when the state the rail reads changes, and the only timer is the one that
// moves the "time in state" labels on: one tick a minute, and only while the
// view is on screen.
//
// Cost with many agents: the list is built once per change of the rail's
// signature and of the view's own filter, sort and search, and only the rows
// on screen are drawn, each kept until it changes. See
// BenchmarkInboxAgents200.

// Quick filters, f in the view. Each is a group of rows by what they want.
const (
	agentsFilterAll = iota
	agentsFilterNeedsYou
	agentsFilterWorking
	agentsFilterDone
	agentsFilterError
	agentsFilterCount
)

// agentsFilterWords names each quick filter, as the header says it.
var agentsFilterWords = [agentsFilterCount]string{"all", "needs you", "working", "done", "error"}

// Sorts, s in the view.
const (
	agentsSortNeedsYou = iota
	agentsSortName
	agentsSortRecent
	agentsSortSession
	agentsSortCount
)

// agentsSortWords names each sort, as the header says it.
var agentsSortWords = [agentsSortCount]string{"needs you", "name", "recent", "session"}

// inboxAgentsTick is how often the time labels move on.
const inboxAgentsTick = time.Minute

// inboxAgentsState is the view's own state.
type inboxAgentsState struct {
	// on says the Inbox shows the agents and not the attention list. It
	// stays when the Inbox closes, so it opens again on the same view.
	on bool
	// key is the selected agent, by session, window and host, so the cursor
	// follows the agent and not a row when the list re-sorts.
	key     string
	sel     int
	scroll  int
	filter  int
	sort    int
	query   string
	tickGen uint64
	// tickStop ends the armed minute tick, nil when none is armed. Closing
	// it makes the tick's command return at once, so a view that closed
	// does not wake the client once more a minute later.
	tickStop chan struct{}

	// list is the filtered, sorted rows, and listAt what they were built
	// from.
	list   []sidebarAgentEntry
	listAt agentsListKey
	// inFrame is set while a frame is drawn. The list cannot change
	// during one, so a frame reads it from list instead of hashing the
	// rail's signature again for every caller.
	inFrame bool
	// counts are the header's counts over every agent, filter or not.
	counts agentsCounts
	// mail is each agent's unread mail, by window, from the mailbox mirror.
	mail map[string]int
	// rows keeps each drawn row until what it shows changes.
	rows map[agentsRowKey]string
	// panel is the last panel drawn, and panelAt what it was drawn from, so
	// a frame drawn for another reason costs the key and nothing more.
	panel   agentsPanel
	panelAt agentsPanelKey
}

// agentsPanel is a drawn panel.
type agentsPanel struct {
	content string
	geo     overlay.Geometry
	rows    []overlayRowHit
}

// agentsPanelKey is what the panel depends on beyond the list.
type agentsPanelKey struct {
	list          agentsListKey
	sel, scroll   int
	width, height int
	minute        int64
	theme         uint64
	live          bool
	keys          *config.KeybindRegistry
}

// agentsListKey is what the list was built from.
type agentsListKey struct {
	sig            uint64
	filter, sort   int
	query, select_ string
	built          bool
}

// agentsCounts are the header's counts.
type agentsCounts struct {
	needsYou, working, done, total int
}

// agentsRowKey is what one drawn row depends on.
type agentsRowKey struct {
	session, window, host   string
	title, state, kind      string
	doneSeen                bool
	minute                  int64
	queued, subagents, mail int
	width                   int
	selected, focused       bool
	theme                   uint64
	sessionLabel            string
	// wrapped marks a row kept with its ground, as the list beside the
	// thread draws it (agentsRowWrapped).
	wrapped bool
}

// inboxAgentsTickMsg moves the time labels on. gen is the tick it belongs to:
// a tick from before the view closed and opened again is dropped.
type inboxAgentsTickMsg struct{ gen uint64 }

// InboxAgentsOn reports whether the Inbox shows the agents view.
func (m *OS) InboxAgentsOn() bool { return m.Inbox.agents.on }

// InboxToggleAgents switches between the attention list and the agents view.
func (m *OS) InboxToggleAgents() tea.Cmd {
	a := &m.Inbox.agents
	m.stopInboxAgentsTick()
	a.on = !a.on
	m.Inbox.reply.editor = nil
	m.Inbox.threads.open = false
	if !a.on {
		m.stopThreadWatch()
	}
	return tea.Batch(m.InboxAgentsTick(), m.InboxThreadFetch())
}

// InboxAgentsTick arms the minute tick while the view is on screen and none
// is armed. The input path calls it after every key while the Inbox is open,
// which is how a view opened again gets its tick back; it is nil otherwise.
func (m *OS) InboxAgentsTick() tea.Cmd {
	a := &m.Inbox.agents
	if !m.ShowInbox || !a.on || a.tickStop != nil {
		return nil
	}
	a.tickGen++
	gen := a.tickGen
	stop := make(chan struct{})
	a.tickStop = stop
	return func() tea.Msg {
		t := time.NewTimer(inboxAgentsTick)
		defer t.Stop()
		select {
		case <-t.C:
			return inboxAgentsTickMsg{gen: gen}
		case <-stop:
			return nil
		}
	}
}

// stopInboxAgentsTick ends the armed minute tick, if one is armed. The view
// calls it when it closes or the Inbox does.
func (m *OS) stopInboxAgentsTick() {
	a := &m.Inbox.agents
	if a.tickStop == nil {
		return
	}
	close(a.tickStop)
	a.tickStop = nil
	// A tick whose timer fired as it was stopped is dropped.
	a.tickGen++
}

// applyInboxAgentsTick takes a tick: the next frame draws the new minute, and
// the next tick is armed while the view is still on screen.
func (m *OS) applyInboxAgentsTick(msg inboxAgentsTickMsg) tea.Cmd {
	a := &m.Inbox.agents
	if msg.gen != a.tickGen {
		return nil
	}
	a.tickStop = nil
	return m.InboxAgentsTick()
}

// agentKey is an agent's identity in the view.
func agentKey(e sidebarAgentEntry) string {
	return e.Host + "\x00" + e.SessionID + "\x00" + e.WindowID
}

// inboxAgentsList is the view's rows, built again only when the rail's
// signature, the filter, the sort, the search or the selector changed.
func (m *OS) inboxAgentsList() []sidebarAgentEntry {
	a := &m.Inbox.agents
	if a.inFrame && a.listAt.built {
		return a.list
	}
	key := agentsListKey{
		sig: m.sidebarSignature(), filter: a.filter, sort: a.sort,
		query: a.query, select_: m.Inbox.Select, built: true,
	}
	if a.listAt == key {
		return a.list
	}
	all := m.agentEntries(m.BuildSessionTree().Sessions)
	a.counts = agentsCounts{total: len(all)}
	a.mail = m.agentsMail()
	kept := all[:0]
	for _, e := range all {
		switch sidebarAgentGroup(e.State, e.DoneSeen) {
		case sidebarGroupNeedsYou:
			a.counts.needsYou++
		case sidebarGroupWorking:
			a.counts.working++
		}
		if e.State == "done" {
			a.counts.done++
		}
		if m.agentsKeep(e) {
			kept = append(kept, e)
		}
	}
	a.list, a.listAt = m.agentsSort(kept), key
	m.agentsClamp()
	return kept
}

// agentsMail counts the unread mail in each agent's inbox, in one pass over
// the mirror.
func (m *OS) agentsMail() map[string]int {
	out := make(map[string]int)
	for i := range m.AgentMail.Messages {
		mm := &m.AgentMail.Messages[i]
		if mm.Kind == "message" && mm.ReadAt == 0 && mm.To != session.AgentInboxHuman {
			out[mm.To]++
		}
	}
	return out
}

// agentsKeep reports whether a row passes the quick filter, the selector and
// the search.
func (m *OS) agentsKeep(e sidebarAgentEntry) bool {
	a := &m.Inbox.agents
	switch a.filter {
	case agentsFilterNeedsYou:
		if sidebarAgentGroup(e.State, e.DoneSeen) != sidebarGroupNeedsYou {
			return false
		}
	case agentsFilterWorking:
		if e.State != "working" {
			return false
		}
	case agentsFilterDone:
		if e.State != "done" {
			return false
		}
	case agentsFilterError:
		if e.State != "errored" {
			return false
		}
	}
	if sel := m.Inbox.selector; sel != nil {
		t := session.SelectorTarget{
			Host: e.Host, Session: e.SessionID, Name: e.Title, State: e.State,
			Harness: e.Harness, NeedsYou: sidebarAgentGroup(e.State, e.DoneSeen) == sidebarGroupNeedsYou,
		}
		if !sel.Match(t) {
			return false
		}
	}
	if a.query != "" {
		return fuzzy.Match(a.query, e.Title+" "+agentSessionWords(e)+" "+e.Message)
	}
	return true
}

// agentsSort orders the rows by the sort in force, into a new slice. Ties keep
// the tree's order, so the list does not shuffle on every frame. It sorts
// indexes, not the rows: a row is a large struct, and moving two hundred of
// them about cost more than everything else a rebuild does.
func (m *OS) agentsSort(rows []sidebarAgentEntry) []sidebarAgentEntry {
	by := m.Inbox.agents.sort
	idx := make([]int, len(rows))
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(i, j int) int {
		x, y := &rows[i], &rows[j]
		switch by {
		case agentsSortName:
			return strings.Compare(strings.ToLower(x.Title), strings.ToLower(y.Title))
		case agentsSortRecent:
			return cmp.Compare(y.StateAt, x.StateAt)
		case agentsSortSession:
			return strings.Compare(agentSessionWords(*x), agentSessionWords(*y))
		}
		// Needs you first, then the longest wait.
		gx, gy := sidebarAgentGroup(x.State, x.DoneSeen), sidebarAgentGroup(y.State, y.DoneSeen)
		if gx != gy {
			return gx - gy
		}
		return cmp.Compare(x.StateAt, y.StateAt)
	})
	out := make([]sidebarAgentEntry, len(rows))
	for i, j := range idx {
		out[i] = rows[j]
	}
	return out
}

// agentSessionWords is where an agent runs, as its row says it: the session,
// with the machine in front for one on another machine.
func agentSessionWords(e sidebarAgentEntry) string {
	s := e.SessionLabel
	if s == "" {
		s = e.SessionID
	}
	if e.Host != "" {
		return e.Host + ":" + s
	}
	return s
}

// agentsClamp keeps the cursor on the selected agent, or in the list when it
// is gone.
func (m *OS) agentsClamp() {
	a := &m.Inbox.agents
	if a.key != "" {
		for i, e := range a.list {
			if agentKey(e) == a.key {
				a.sel = i
				return
			}
		}
	}
	a.sel = min(max(a.sel, 0), max(len(a.list)-1, 0))
	if len(a.list) > 0 {
		a.key = agentKey(a.list[a.sel])
	}
}

// inboxAgentsMove moves the cursor by delta rows.
func (m *OS) inboxAgentsMove(delta int) {
	a := &m.Inbox.agents
	list := m.inboxAgentsList()
	if len(list) == 0 {
		return
	}
	a.sel = min(max(a.sel+delta, 0), len(list)-1)
	a.key = agentKey(list[a.sel])
}

// inboxAgentsSelect puts the cursor on row idx.
func (m *OS) inboxAgentsSelect(idx int) {
	a := &m.Inbox.agents
	list := m.inboxAgentsList()
	if idx < 0 || idx >= len(list) {
		return
	}
	a.sel, a.key = idx, agentKey(list[idx])
}

// inboxAgentSelected is the agent under the cursor.
func (m *OS) inboxAgentSelected() (sidebarAgentEntry, bool) {
	a := &m.Inbox.agents
	list := m.inboxAgentsList()
	if a.sel < 0 || a.sel >= len(list) {
		return sidebarAgentEntry{}, false
	}
	return list[a.sel], true
}

// InboxAgentsCycleFilter steps the quick filter: all, needs you, working,
// done, error.
func (m *OS) InboxAgentsCycleFilter() {
	a := &m.Inbox.agents
	a.filter = (a.filter + 1) % agentsFilterCount
	a.sel, a.scroll = 0, 0
}

// InboxAgentsCycleSort steps the sort: needs you, name, recent, session.
func (m *OS) InboxAgentsCycleSort() {
	a := &m.Inbox.agents
	a.sort = (a.sort + 1) % agentsSortCount
	a.scroll = 0
}

// InboxAgentsGo goes to the selected agent's pane and closes the Inbox, as
// enter on an Inbox item does.
func (m *OS) InboxAgentsGo() tea.Cmd {
	e, ok := m.inboxAgentSelected()
	if !ok {
		return nil
	}
	m.CloseInbox()
	m.inboxJump(session.AttentionItem{Session: e.SessionID, Window: e.WindowID, Host: e.Host})
	return nil
}

// InboxAgentsReply opens the reply editor for the selected agent.
func (m *OS) InboxAgentsReply() tea.Cmd {
	e, ok := m.inboxAgentSelected()
	if !ok || !m.inboxReplySupported() {
		return nil
	}
	who := printableTitle(e.Title)
	if who == "" {
		who = shortWindowLabel(e.WindowID)
	}
	if e.Host != "" {
		m.ShowNotification(who+" is on "+printableTitle(e.Host)+". Attach there to reply", "info", m.Settings.NotificationDuration)
		return nil
	}
	ctx := sidebarStateWords(e.State)
	if e.StateAt > 0 {
		ctx += " " + inboxWait(e.StateAt, time.Now())
	}
	m.openInboxReply(e.SessionID, e.WindowID, who, ctx, false)
	return nil
}

// inboxAgentsSearch applies what the selector line holds in the agents view:
// key:value terms are the selector, the rest is a search over the name, the
// session and the last message.
func (m *OS) inboxAgentsSearch(text string) (selector string, query string) {
	var sel, words []string
	for f := range strings.FieldsSeq(text) {
		if k, _, ok := strings.Cut(f, ":"); ok && k != "" {
			sel = append(sel, f)
			continue
		}
		words = append(words, f)
	}
	return strings.Join(sel, " "), strings.Join(words, " ")
}

// agentsRows is how many list rows the view shows.
const agentsRows = 16

// renderInboxAgents draws the agents view.
func (m *OS) renderInboxAgents() (string, overlay.Geometry, []overlayRowHit) {
	st := &m.Inbox
	a := &st.agents
	list := m.inboxAgentsList()
	a.inFrame = true
	defer func() { a.inFrame = false }()
	title := m.agentsTitle()
	hints := m.inboxAgentsHints()
	if len(list) == 0 {
		lines := []string{"No agents are running.", "Start one with tuios start-agent."}
		if a.counts.total > 0 {
			lines = []string{"No agent matches " + m.agentsNarrowWords() + ".", "f shows the next group, and / changes the search."}
		}
		return m.emptyPanel(title, inboxWidth, inboxEmptyRows, lines[0], lines[1:], overlay.Hint{Key: "tab", Label: "Inbox"}, hints)
	}
	replyDetail, replyHints, replying := m.inboxReplyDetail()
	if replying {
		hints = replyHints
	}
	// The thread beside the list on a wide screen, or in its place when it
	// is open on a narrow one. See inbox_thread.go.
	if m.GetRenderWidth() >= threadWideMin || st.threads.open {
		if !replying {
			hints = m.inboxThreadHints()
		}
		return m.renderAgentsWithThread(list, title, hints)
	}
	now := time.Now()
	minute := now.Unix() / 60
	gen := theme.Generation()
	key := agentsPanelKey{
		list: a.listAt, sel: a.sel, scroll: a.scroll, width: m.GetRenderWidth(), height: m.GetRenderHeight(),
		minute: minute, theme: gen, live: st.Live, keys: m.KeybindRegistry,
	}
	if !replying && a.panelAt == key && a.panel.content != "" {
		return a.panel.content, a.panel.geo, a.panel.rows
	}
	content, geo, rows := m.renderListOverlay(listOverlay{
		Title:      title,
		Width:      inboxWidth,
		MaxVisible: agentsRows,
		Count:      len(list),
		Selected:   a.sel,
		Scroll:     &a.scroll,
		Hints:      hints,
		DetailFor: func(width int) []string {
			if replying {
				return replyDetail(width)
			}
			return nil
		},
		DetailStyled: replying,
		DetailAt:     m.inboxReplyDetailAt(replying),
		Unfocused:    replying,
		Position: func() (int, int) {
			return a.sel + 1, len(list)
		},
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			e := list[i]
			return m.agentsRow(e, selected, !replying, rowBg, pal, width, minute, gen, now)
		},
	})
	if replying {
		a.panel, a.panelAt = agentsPanel{}, agentsPanelKey{}
	} else {
		a.panel, a.panelAt = agentsPanel{content: content, geo: geo, rows: rows}, key
	}
	return content, geo, rows
}

// agentsNarrowWords says what narrows the list, for the empty message.
func (m *OS) agentsNarrowWords() string {
	a := &m.Inbox.agents
	var parts []string
	if a.filter != agentsFilterAll {
		parts = append(parts, agentsFilterWords[a.filter])
	}
	if m.Inbox.Select != "" {
		parts = append(parts, m.Inbox.Select)
	}
	if a.query != "" {
		parts = append(parts, strconv.Quote(a.query))
	}
	if len(parts) == 0 {
		return "the list"
	}
	return strings.Join(parts, " and ")
}

// agentsTitle is the view's title: the counts over every agent, then what
// narrows or orders the list when it is not the default, in words, so a
// narrowed list never reads as a short one.
func (m *OS) agentsTitle() string {
	st := &m.Inbox
	a := &st.agents
	c := a.counts
	parts := []string{"Agents"}
	if c.needsYou > 0 {
		parts = append(parts, strconv.Itoa(c.needsYou)+" "+agentNeedsYou(c.needsYou))
	}
	parts = append(parts, strconv.Itoa(c.working)+" working", strconv.Itoa(c.done)+" done")
	if a.filter != agentsFilterAll {
		parts = append(parts, "show "+agentsFilterWords[a.filter])
	}
	if a.sort != agentsSortNeedsYou {
		parts = append(parts, "sort "+agentsSortWords[a.sort])
	}
	if a.query != "" {
		parts = append(parts, "search "+a.query)
	}
	title := strings.Join(parts, " · ")
	if st.Select != "" {
		title += " [select " + st.Select + "]"
	}
	if !st.Live {
		title += " (not connected)"
	}
	return title
}

// agentsRow draws one agent: its mark, name, session, state, time in state,
// and what waits for it, kept until any of those change.
func (m *OS) agentsRow(e sidebarAgentEntry, selected, focused bool, bg color.Color, pal overlay.Palette, width int, minute int64, gen uint64, now time.Time) string {
	a := &m.Inbox.agents
	k := agentsRowKey{
		session: e.SessionID, window: e.WindowID, host: e.Host, title: e.Title,
		state: e.State, kind: e.AgentKind, doneSeen: e.DoneSeen, minute: minute,
		queued: e.Queued, subagents: e.Subagents, mail: a.mail[e.WindowID],
		width: width, selected: selected, focused: focused, theme: gen,
		sessionLabel: e.SessionLabel,
	}
	if s, ok := a.rows[k]; ok {
		return s
	}
	s := agentsRowText(e, k.mail, bg, pal, width, now)
	if a.rows == nil || len(a.rows) > 1024 {
		a.rows = make(map[agentsRowKey]string)
	}
	a.rows[k] = s
	return s
}

// agentsRowText draws an agent's row on bg at width cells.
func agentsRowText(e sidebarAgentEntry, mail int, bg color.Color, pal overlay.Palette, width int, now time.Time) string {
	stateInk := overlay.Readable(sidebarStateColor(e.State, e.DoneSeen, pal), bg)
	mark := agentStateIndicator(e.State)
	if mark == "" {
		mark = " "
	}
	words := agentRowStateWords(e)
	wait := ""
	if e.StateAt > 0 {
		wait = inboxWait(e.StateAt, now)
	}
	var extras []string
	if e.Queued > 0 {
		extras = append(extras, strconv.Itoa(e.Queued)+" queued")
	}
	if mail > 0 {
		extras = append(extras, strconv.Itoa(mail)+" mail")
	}
	if e.Subagents > 0 {
		extras = append(extras, strconv.Itoa(e.Subagents)+" sub")
	}
	extra := strings.Join(extras, " · ")

	// The columns, from the right: what waits, the time, the state, the
	// session. The name takes what is left. Every column has the same width
	// on every row, so the list reads down as a table.
	const markW, stateW, waitW = 2, 10, 4
	sessW := min(16, max(width/5, 6))
	extraW := 0
	if width >= 72 {
		extraW = min(20, width/5)
	}
	nameW := width - 2 - markW - sessW - 1 - stateW - 1 - waitW - extraW
	name := printableTitle(e.Title)
	if name == "" {
		name = shortWindowLabel(e.WindowID)
	}
	// Each column is one SGR span on the row's ground, written with
	// ansi.Style directly: lipgloss's Render measured and split every span
	// again, and a list of two hundred agents drawn again at once spent most
	// of its frame there.
	var b strings.Builder
	b.Grow(width + 160)
	// At 16 colours the grounds and some inks are overlay.NoColor, which
	// lipgloss left out and ansi.Style would write as black: such a colour
	// is not written at all.
	var ground ansi.Style
	if !noColor(bg) {
		ground = ground.BackgroundColor(ansi.Color(bg))
	}
	span := func(ink color.Color, bold bool, text string) {
		s := ground
		if !noColor(ink) {
			s = s.ForegroundColor(ansi.Color(ink))
		}
		if bold {
			s = s.Bold()
		}
		b.WriteString(s.Styled(text))
	}
	b.WriteString(ground.Styled("  "))
	span(stateInk, false, padTo(mark, markW))
	span(overlay.Readable(pal.Fg, bg), sidebarAttention(e.State), padTo(ansi.Truncate(name, max(nameW, 1), "…"), max(nameW, 1)))
	dim := overlay.Readable(pal.FgDim, bg)
	span(dim, false, padTo(ansi.Truncate(agentSessionWords(e), sessW, "…"), sessW)+" ")
	span(stateInk, false, padTo(ansi.Truncate(words, stateW, "…"), stateW)+" ")
	span(overlay.Readable(pal.FgMute, bg), false, padLeft(wait, waitW))
	if extraW > 0 && extra != "" {
		span(dim, false, "  "+ansi.Truncate(extra, extraW-2, "…"))
	}
	return b.String()
}

// noColor reports whether c is no colour at all: nil or overlay.NoColor.
func noColor(c color.Color) bool {
	if c == nil {
		return true
	}
	_, ok := c.(lipgloss.NoColor)
	return ok
}

// agentRowStateWords is an agent's state as its row says it: what it needs
// from you when it needs you, its state otherwise.
func agentRowStateWords(e sidebarAgentEntry) string {
	switch e.State {
	case "needs_input":
		switch e.AgentKind {
		case "approval", "question", "plan":
			return e.AgentKind
		}
		return "needs you"
	case "errored":
		return "error"
	}
	return sidebarStateWords(e.State)
}

// padTo pads s with spaces to w cells.
func padTo(s string, w int) string {
	if n := ansi.StringWidth(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// inboxAgentsHints are the view's keys.
func (m *OS) inboxAgentsHints() []overlay.Hint {
	hints := m.keyHints(config.ActionInboxGo, "go", config.ActionInboxReply, "reply")
	hints = append(hints, m.keyHints(config.ActionInboxFilter, "show", config.ActionInboxSort, "sort",
		config.ActionInboxSelect, "search", config.ActionInboxAgents, "Inbox", config.ActionInboxClose, "close")...)
	return hints
}

// inboxAgentsSelectApply puts what the selector line holds in force in the
// agents view: its key:value terms as the selector the attention list shares,
// and its words as the search.
func (m *OS) inboxAgentsSelectApply() {
	st := &m.Inbox
	sel, query := m.inboxAgentsSearch(st.selectDraft)
	if sel == "" {
		st.Select, st.selector = "", nil
	} else {
		home, _ := os.UserHomeDir()
		parsed, err := session.ParseSelector(sel, home)
		if err != nil {
			st.selectErr = err.Error()
			return
		}
		st.Select, st.selector = parsed.String(), parsed
	}
	st.agents.query = query
	st.selectEditing = false
	st.selectErr = ""
	st.agents.sel, st.agents.scroll = 0, 0
	st.agents.key = ""
}
