package app

import (
	"cmp"
	"image/color"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/session"
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

// Sorts, s in the view.
const (
	agentsSortNeedsYou = iota
	agentsSortName
	agentsSortRecent
	agentsSortSession
	agentsSortCount
)

// inboxAgentsTick is how often the time labels move on.
const inboxAgentsTick = time.Minute

// inboxAgentsState is the view's own state.
type inboxAgentsState struct {
	// on says the Inbox shows the agents and not the attention list. It
	// stays when the Inbox closes, so it opens again on the same view.
	on      bool
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
	// marks are the marked agents, by key, and marksGen changes with them.
	// See inbox_bulk.go.
	marks    map[string]bool
	marksGen uint64
	// sheet is the bulk allow's confirm sheet, nil when it is not up.
	sheet *bulkSheet
	// snooze are the items the snooze picker picks a length for.
	snooze []string
}

// agentsListKey is what the list was built from.
type agentsListKey struct {
	sig            uint64
	sort           int
	query, select_ string
	built          bool
}

// inboxAgentsTickMsg moves the time labels on. gen is the tick it belongs to:
// a tick from before the view closed and opened again is dropped.
type inboxAgentsTickMsg struct{ gen uint64 }

// InboxAgentsOn reports whether the Inbox shows the agents view.
func (m *OS) InboxAgentsOn() bool { return m.Inbox.agents.on }

// InboxAgentsTick arms the minute tick while the view is on screen and none
// is armed. The input path calls it after every key while the Inbox is open,
// which is how a view opened again gets its tick back; it is nil otherwise.
func (m *OS) InboxAgentsTick() tea.Cmd {
	a := &m.Inbox.agents
	if !m.ShowInbox || a.tickStop != nil {
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
	m.tickStats.AgentsTicks++
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
		sig: m.sidebarSignature(), sort: a.sort,
		query: a.query, select_: m.Inbox.Select, built: true,
	}
	if a.listAt == key {
		return a.list
	}
	all := m.agentEntries(m.BuildSessionTree().Sessions)
	kept := all[:0]
	for _, e := range all {
		if m.agentsKeep(e) {
			kept = append(kept, e)
		}
	}
	a.list, a.listAt = m.agentsSort(kept), key
	return a.list
}

// agentsKeep reports whether a row passes the selector and the search.
func (m *OS) agentsKeep(e sidebarAgentEntry) bool {
	a := &m.Inbox.agents
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

// InboxAgentsCycleSort steps the sort: needs you, name, recent, session.
func (m *OS) InboxAgentsCycleSort() {
	a := &m.Inbox.agents
	a.sort = (a.sort + 1) % agentsSortCount
}

// InboxAgentsGo goes to the selected agent's pane and closes the Inbox, as
// enter on an Inbox item does.
func (m *OS) InboxAgentsGo() tea.Cmd {
	e, ok := m.cockpitAgent()
	if !ok {
		return nil
	}
	m.CloseInbox()
	m.inboxJump(session.AttentionItem{Session: e.SessionID, Window: e.WindowID, Host: e.Host})
	return nil
}

// InboxAgentsReply opens the reply editor for the selected agent.
func (m *OS) InboxAgentsReply() tea.Cmd {
	e, ok := m.cockpitAgent()
	if !ok || !m.inboxReplySupported() {
		return nil
	}
	who := printableTitle(e.Title)
	if who == "" {
		who = shortWindowLabel(e.WindowID)
	}
	ctx := sidebarStateWords(e.State)
	if e.StateAt > 0 {
		ctx += " " + inboxWait(e.StateAt, time.Now())
	}
	if e.Host != "" {
		if e.State == "needs_input" {
			m.ShowNotification(who+" is waiting on a prompt. Answer it first.", "info", m.Settings.NotificationDuration)
			return nil
		}
		m.openHostReply(e.Host, e.SessionID, e.WindowID, who, ctx)
		return nil
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

// inboxAgentsSelectApply puts what the selector line holds in force in the
// agents view: its key:value terms as the selector the attention list shares,
// and its words as the search.
func (m *OS) inboxAgentsSelectApply() {
	st := &m.Inbox
	sel, query := m.inboxAgentsSearch(st.selectDraft())
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
	st.selectEd = nil
	st.selectEditing = false
	st.selectErr = ""
	st.ck.scroll, st.ck.follow = 0, true
	m.cockpitTouch()
}
