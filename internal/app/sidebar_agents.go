package app

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const (
	sidebarAgentsAll      = "all"
	sidebarAgentsSession  = "session"
	sidebarAgentsNeedsYou = "needs_you"
	sidebarAgentsPriority = "priority"
	sidebarAgentsRecent   = "recent"
)

func (m *OS) sidebarAgentsFilter() string {
	if m.SidebarAgentFilter == sidebarAgentsSession {
		return sidebarAgentsSession
	}
	return sidebarAgentsAll
}

func (m *OS) sidebarAgentsSort() string {
	switch m.SidebarAgentSort {
	case sidebarAgentsPriority, sidebarAgentsRecent:
		return m.SidebarAgentSort
	}
	return sidebarAgentsNeedsYou
}

// SidebarCycleAgentsFilter flips the agents section between every session and
// the attached one, and persists the choice.
func (m *OS) SidebarCycleAgentsFilter() {
	if m.sidebarAgentsFilter() == sidebarAgentsAll {
		m.SidebarAgentFilter = sidebarAgentsSession
	} else {
		m.SidebarAgentFilter = sidebarAgentsAll
	}
	m.saveSidebarState()
}

// SidebarCycleAgentsSort steps the agents section through its orders, needs
// you, then priority, then recency, and persists the choice.
func (m *OS) SidebarCycleAgentsSort() {
	switch m.sidebarAgentsSort() {
	case sidebarAgentsNeedsYou:
		m.SidebarAgentSort = sidebarAgentsPriority
	case sidebarAgentsPriority:
		m.SidebarAgentSort = sidebarAgentsRecent
	default:
		m.SidebarAgentSort = sidebarAgentsNeedsYou
	}
	m.saveSidebarState()
}

const (
	sidebarGroupNeedsYou = iota
	sidebarGroupFinished
	sidebarGroupWorking
	sidebarGroupIdle
)

func sidebarAgentGroup(state string, doneSeen bool) int {
	switch {
	case sidebarAttention(state):
		return sidebarGroupNeedsYou
	case state == "done" && !doneSeen:
		return sidebarGroupFinished
	case state == "working":
		return sidebarGroupWorking
	default:
		return sidebarGroupIdle
	}
}

func sidebarAgentPriority(state string, doneSeen bool) int {
	switch state {
	case "errored":
		return 5
	case "needs_input":
		return 4
	case "working":
		return 3
	case "limit_reached":
		return 2
	case "done":
		if doneSeen {
			return 1
		}
		return 2
	default:
		return 0
	}
}

func (m *OS) sidebarFilterAgents(agents []sidebarAgentEntry) ([]sidebarAgentEntry, int) {
	total := len(agents)
	if m.sidebarAgentsFilter() != sidebarAgentsSession {
		return agents, total
	}
	attached := m.sidebarCurrentSessionID()
	kept := make([]sidebarAgentEntry, 0, total)
	for _, e := range agents {
		if e.SessionID == attached {
			kept = append(kept, e)
		}
	}
	return kept, total
}

func (m *OS) sidebarSortAgents(agents []sidebarAgentEntry) {
	switch m.sidebarAgentsSort() {
	case sidebarAgentsRecent:
		sort.SliceStable(agents, func(a, b int) bool { return agents[a].StateAt > agents[b].StateAt })
	case sidebarAgentsPriority:
		sort.SliceStable(agents, func(a, b int) bool {
			return sidebarAgentPriority(agents[a].State, agents[a].DoneSeen) >
				sidebarAgentPriority(agents[b].State, agents[b].DoneSeen)
		})
	default:
		sort.SliceStable(agents, func(a, b int) bool {
			return sidebarAgentGroup(agents[a].State, agents[a].DoneSeen) <
				sidebarAgentGroup(agents[b].State, agents[b].DoneSeen)
		})
	}
}

var sidebarFoldClock = time.Now

const sidebarAgentFoldID = "\x00fold"

const sidebarAgentFoldMin = 2

func sidebarAgentRests(e sidebarAgentEntry, threshold time.Duration, now time.Time) bool {
	if threshold <= 0 || e.Fold > 0 || e.Focused || e.Queued > 0 {
		return false
	}
	if e.Settled {
		g := sidebarAgentGroup(e.State, e.DoneSeen)
		return g != sidebarGroupNeedsYou && g != sidebarGroupWorking
	}
	if e.StateAt <= 0 {
		return false
	}
	if e.Subagents > 0 {
		return false
	}
	if sidebarAgentGroup(e.State, e.DoneSeen) != sidebarGroupIdle {
		return false
	}
	return now.Sub(time.Unix(0, e.StateAt)) >= threshold
}

func (m *OS) sidebarFoldAgents(agents []sidebarAgentEntry, now time.Time) []sidebarAgentEntry {
	threshold := m.Settings.SidebarAgentRestFold
	if threshold <= 0 || m.sidebarAgentsUnfolded || len(agents) < sidebarAgentFoldMin {
		return agents
	}
	n := 0
	for _, e := range agents {
		if sidebarAgentRests(e, threshold, now) {
			n++
		}
	}
	if n < sidebarAgentFoldMin {
		return agents
	}
	kept := make([]sidebarAgentEntry, 0, len(agents)-n+1)
	names := make([]string, 0, n)
	for _, e := range agents {
		if sidebarAgentRests(e, threshold, now) {
			names = append(names, printableTitle(e.Title))
			continue
		}
		kept = append(kept, e)
	}
	return append(kept, sidebarAgentEntry{
		SessionID:   sidebarAgentFoldID,
		WindowIndex: -1,
		Fold:        n,
		FoldNames:   strings.Join(names, ", "),
	})
}

// SidebarUnfoldAgents shows the rows the fold took, until the rail lets go of
// the keyboard. A click can unfold them while the rail does not have the
// keyboard, and then there is no letting go to wait for: they fold again on
// a click outside the rail or when a pane is focused
// (RefoldAgentsAfterClickAway, refoldAgentsOnPaneFocus).
func (m *OS) SidebarUnfoldAgents() {
	m.sidebarAgentsUnfolded = true
	m.sidebarCache.invalidate()
}

// RefoldAgentsAfterClickAway folds the rows at rest again after a click
// outside the rail, when the rail does not have the keyboard: the pointer's
// way of letting go of it.
func (m *OS) RefoldAgentsAfterClickAway() {
	m.refoldAgentsOnPaneFocus()
}

func (m *OS) refoldAgentsOnPaneFocus() {
	if !m.sidebarAgentsUnfolded || m.SidebarFocused {
		return
	}
	m.sidebarAgentsUnfolded = false
	m.sidebarCache.invalidate()
}

func (m *OS) sidebarAgentFoldRow(e sidebarAgentEntry, cw int, pal overlay.Palette, st sidebarRowState, names bool) string {
	rowBg := sidebarRowBg(st, pal)
	fg := pal.FgMute
	if st.lit() {
		fg = pal.Fg
	}
	indent := sidebarNameCol()
	text := "+" + strconv.Itoa(e.Fold) + " at rest"
	if names {
		indent++
		text, fg = e.FoldNames, pal.FgMute
	}
	return sidebarFit(sidebarStyle(rowBg, nil).Render(strings.Repeat(" ", indent))+
		sidebarStyle(rowBg, fg).Render(overlay.Truncate(text, max(sidebarNameAvail(cw, 0)-(indent-sidebarNameCol()), 1))), cw, rowBg)
}

func (m *OS) railAgentOrder() []sidebarAgentEntry {
	all, _ := m.sidebarFilterAgents(m.agentEntries(m.BuildSessionTree().Sessions))
	agents := all[:0:0]
	for _, e := range all {
		if e.Host == "" {
			agents = append(agents, e)
		}
	}
	m.sidebarSortAgents(agents)
	now := time.Now()
	folded := m.sidebarFoldAgents(agents, now)
	if n := len(folded); n == 0 || folded[n-1].Fold == 0 {
		return sidebarNestAgents(agents)
	}
	awake := sidebarNestAgents(append([]sidebarAgentEntry(nil), folded[:len(folded)-1]...))
	for _, e := range agents {
		if sidebarAgentRests(e, m.Settings.SidebarAgentRestFold, now) {
			awake = append(awake, e)
		}
	}
	return awake
}

// CycleAgent focuses the agent pane delta places from the focused one in the
// rail's order (railAgentOrder), wrapping at both ends. From a pane that runs
// no agent, next goes to the first and previous to the last. An agent in
// another session or on another workspace is reached the way choosing its row
// in the rail reaches it.
func (m *OS) CycleAgent(delta int) {
	if m.refuseAgentsOff() {
		return
	}
	agents := m.railAgentOrder()
	n := len(agents)
	if n == 0 {
		m.ShowNotification("No agents running", "info", m.Settings.NotificationDuration)
		return
	}
	next := n - 1
	if delta > 0 {
		next = 0
	}
	for i, e := range agents {
		if e.Focused {
			next = ((i+delta)%n + n) % n
			if next == i {
				m.ShowNotification("No other agents", "info", m.Settings.NotificationDuration)
				return
			}
			break
		}
	}
	e := agents[next]
	m.sidebarFocusWindow(sidebarRowHit{
		Kind:        sidebarRowAgent,
		SessionID:   e.SessionID,
		WindowID:    e.WindowID,
		WindowIndex: e.WindowIndex,
	})
}
