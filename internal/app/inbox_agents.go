package app

import (
	"cmp"
	"image/color"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/units"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

const (
	agentsSortNeedsYou = iota
	agentsSortName
	agentsSortRecent
	agentsSortSession
	agentsSortCount
)

const inboxAgentsTick = time.Minute

type inboxAgentsState struct {
	on       bool
	sort     int
	query    string
	tickGen  uint64
	tickStop chan struct{}

	list     []sidebarAgentEntry
	listAt   agentsListKey
	total    int
	inFrame  bool
	marks    map[string]bool
	marksGen uint64
	sheet    *bulkSheet
	resuming map[string]bool
	snooze   []string
}

type agentsListKey struct {
	sig            uint64
	sort           int
	query, select_ string
	built          bool
	cards          uint64
}

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
	ended := m.stopSignal()
	return func() tea.Msg {
		t := time.NewTimer(inboxAgentsTick)
		defer t.Stop()
		select {
		case <-t.C:
			return inboxAgentsTickMsg{gen: gen}
		case <-stop:
			return nil
		case <-ended:
			return nil
		}
	}
}

func (m *OS) stopInboxAgentsTick() {
	a := &m.Inbox.agents
	if a.tickStop == nil {
		return
	}
	close(a.tickStop)
	a.tickStop = nil
	a.tickGen++
}

func (m *OS) applyInboxAgentsTick(msg inboxAgentsTickMsg) tea.Cmd {
	a := &m.Inbox.agents
	if msg.gen != a.tickGen {
		return nil
	}
	m.tickStats.AgentsTicks++
	a.tickStop = nil
	if m.ShowInbox && m.ckManyMachines() {
		return tea.Batch(m.InboxAgentsTick(), refreshFederationCmd())
	}
	return m.InboxAgentsTick()
}

func agentKey(e sidebarAgentEntry) string {
	return e.Host + "\x00" + e.SessionID + "\x00" + e.WindowID
}

func (m *OS) inboxAgentsList() []sidebarAgentEntry {
	a := &m.Inbox.agents
	if a.inFrame && a.listAt.built {
		return a.list
	}
	key := agentsListKey{
		sig: m.sidebarSignature(), sort: a.sort,
		query: a.query, select_: m.Inbox.Select, built: true,
	}
	if m.Inbox.Select != "" {
		key.cards = m.Inbox.ck.cardsGen
	}
	if a.listAt == key {
		return a.list
	}
	all := append(m.agentEntries(m.BuildSessionTree().Sessions), m.remoteAgentEntries()...)
	kept := all[:0]
	for _, e := range all {
		if m.agentsKeep(e) {
			kept = append(kept, e)
		}
	}
	a.total = len(all)
	a.list, a.listAt = m.agentsSort(kept), key
	return a.list
}

const remoteAgentRowsMax = 256

func (m *OS) remoteAgentEntries() []sidebarAgentEntry {
	var out []sidebarAgentEntry
	for _, h := range m.FederationHosts {
		if h.Name == m.attachedMachine() {
			continue
		}
		n := 0
		for _, s := range h.Sessions {
			for _, a := range s.Agents {
				if n == remoteAgentRowsMax {
					break
				}
				n++
				out = append(out, sidebarAgentEntry{
					SessionID: s.Name, SessionLabel: firstNonEmptyStr(s.DisplayName, s.Name),
					WindowID: a.ID, Title: a.Title, State: a.State, StateAt: a.StateAt,
					Harness: a.Harness, Message: a.Message, AgentKind: a.Kind, Meta: a.Meta,
					Subagents: a.Subagents, Settled: a.Settled, Quiet: a.Quiet, WindowIndex: -1, Foreign: true, Host: h.Name,
				})
			}
		}
	}
	return out
}

func (m *OS) agentsKeep(e sidebarAgentEntry) bool {
	a := &m.Inbox.agents
	if sel := m.Inbox.selector; sel != nil {
		t := session.SelectorTarget{
			Host: e.Host, Session: e.SessionID, Name: e.Title, State: e.State,
			Harness: e.Harness, NeedsYou: sidebarAgentGroup(e.State, e.DoneSeen) == sidebarGroupNeedsYou,
		}
		if e.Host == "" {
			for _, c := range m.ckHeldCards()[e.SessionID+"\x00"+e.WindowID] {
				t.Tasks, t.Stages = append(t.Tasks, c.ID), append(t.Stages, c.Stage)
			}
		}
		if !sel.Match(t) {
			return false
		}
	}
	if a.query != "" {
		return agentSearchTier(a.query, e) >= 0
	}
	return true
}

func agentSearchTier(query string, e sidebarAgentEntry) int {
	q := strings.ToLower(strings.TrimSpace(query))
	name := strings.ToLower(printableTitle(e.Title))
	switch {
	case q == "":
		return 0
	case name == q:
		return 0
	case strings.Contains(name, q):
		return 1
	case fuzzy.Match(q, e.Title+" "+agentSessionWords(e)):
		return 2
	case agentWordsMatch(q, e.Message):
		return 3
	}
	return -1
}

func agentWordsMatch(q, text string) bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.' && r != '/'
	})
	for w := range strings.FieldsSeq(q) {
		if !slices.ContainsFunc(words, func(t string) bool { return strings.HasPrefix(t, w) }) {
			return false
		}
	}
	return true
}

func (m *OS) inboxAgentsBest() (sidebarAgentEntry, bool) {
	q := m.Inbox.agents.query
	if q == "" {
		return sidebarAgentEntry{}, false
	}
	best, tier := sidebarAgentEntry{}, -1
	for _, e := range m.inboxAgentsList() {
		if t := agentSearchTier(q, e); t >= 0 && (tier < 0 || t < tier) {
			best, tier = e, t
		}
	}
	return best, tier >= 0
}

func (m *OS) agentsSort(rows []sidebarAgentEntry) []sidebarAgentEntry {
	by := m.Inbox.agents.sort
	idx := make([]int, len(rows))
	for i := range idx {
		idx[i] = i
	}
	q := m.Inbox.agents.query
	slices.SortStableFunc(idx, func(i, j int) int {
		x, y := &rows[i], &rows[j]
		if q != "" {
			if c := cmp.Compare(agentSearchTier(q, *x), agentSearchTier(q, *y)); c != 0 {
				return c
			}
		}
		switch by {
		case agentsSortName:
			return strings.Compare(strings.ToLower(x.Title), strings.ToLower(y.Title))
		case agentsSortRecent:
			return cmp.Compare(y.StateAt, x.StateAt)
		case agentsSortSession:
			return strings.Compare(agentSessionWords(*x), agentSessionWords(*y))
		}
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
	if !ok {
		return nil
	}
	who := printableTitle(e.Title)
	if who == "" {
		who = shortWindowLabel(e.WindowID)
	}
	ctx := sidebarStateWords(e.State)
	if e.StateAt > 0 {
		ctx += " " + units.Waited(e.StateAt, time.Now())
	}
	if e.Host != "" {
		if e.State == "needs_input" {
			m.ShowNotification(who+" is waiting on a prompt. Answer it first.", "warning", m.Settings.NotificationDuration)
			return nil
		}
		m.openHostReply(e.Host, e.SessionID, e.WindowID, who, ctx)
		return nil
	}
	m.openInboxReply(e.SessionID, e.WindowID, who, ctx, false)
	return nil
}

func splitSelectorSearch(text string) (selector string, query string) {
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

func noColor(c color.Color) bool {
	if c == nil {
		return true
	}
	_, ok := c.(lipgloss.NoColor)
	return ok
}

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

func (m *OS) inboxAgentsSelectApply() {
	st := &m.Inbox
	sel, query := splitSelectorSearch(st.selectDraft())
	if sel == "" {
		m.setInboxSelector(nil)
	} else {
		home, _ := os.UserHomeDir()
		parsed, err := session.ParseSelector(sel, home)
		if err != nil {
			st.selectErr = err.Error()
			return
		}
		m.setInboxSelector(parsed)
	}
	st.agents.query = query
	st.selectEd = nil
	st.selectEditing = false
	st.selectErr = ""
	st.ck.scroll, st.ck.follow = 0, true
	if best, ok := m.inboxAgentsBest(); ok {
		st.ck.key, st.ck.akey, st.ck.pending = "", agentKey(best), ""
		m.cockpitTouchRows()
		m.cockpitRows()
		m.cockpitClamp()
		m.cockpitSync()
	}
	m.cockpitTouch()
}
