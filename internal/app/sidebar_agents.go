package app

import "sort"

// The agents section answers "what should I handle next", which is a different
// question from the one the rest of the rail answers. Its two controls live in
// its own header and nowhere else: a filter, because a rail watching six
// sessions is not always a rail you want six sessions' worth of alarm from, and
// a sort, because "loudest" and "newest" are both reasonable readings of a list
// of running agents and neither is right for everyone.

// Agents-section filter and sort values. They are the strings written to
// sidebar.json, so they are the wire format as much as they are the state.
const (
	sidebarAgentsAll      = "all"
	sidebarAgentsSession  = "session"
	sidebarAgentsPriority = "priority"
	sidebarAgentsRecent   = "recent"
)

// sidebarAgentsFilter and sidebarAgentsSort read the model's controls, folding
// an absent or unrecognised value (an older state file, a hand-edited one) back
// to the default rather than rendering an empty section nobody asked for.
func (m *OS) sidebarAgentsFilter() string {
	if m.SidebarAgentFilter == sidebarAgentsSession {
		return sidebarAgentsSession
	}
	return sidebarAgentsAll
}

func (m *OS) sidebarAgentsSort() string {
	if m.SidebarAgentSort == sidebarAgentsRecent {
		return sidebarAgentsRecent
	}
	return sidebarAgentsPriority
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

// SidebarCycleAgentsSort flips the agents section between priority and recency,
// and persists the choice.
func (m *OS) SidebarCycleAgentsSort() {
	if m.sidebarAgentsSort() == sidebarAgentsPriority {
		m.SidebarAgentSort = sidebarAgentsRecent
	} else {
		m.SidebarAgentSort = sidebarAgentsPriority
	}
	m.saveSidebarState()
}

// sidebarAgentPriority ranks a pane by how much it should be the next thing the
// user touches. Done is not a state anything detects: it is idle and unread,
// and it stops being done the moment you look at it, which is exactly what the
// DoneSeen bit records. So a finished pane outranks nothing that is still
// moving, and a finished pane already reviewed sinks below one still working.
//
// Deliberately not sessiontree.AgentRank, which puts done-unread above working.
// That ranking answers "what is the loudest thing in this session" for the
// session row's one rolled-up glyph, where a finished-unread pane may well be
// the most interesting thing a session has to say. This one answers "what
// should I handle next", and a working pane needs nothing from anybody.
func sidebarAgentPriority(state string, doneSeen bool) int {
	switch state {
	case "errored":
		return 5
	case "needs_input":
		return 4
	case "working":
		return 3
	case "done":
		if doneSeen {
			return 1
		}
		return 2
	default: // idle, and any state this build does not know
		return 0
	}
}

// sidebarFilterAgents drops the panes the section's filter hides. It returns the
// kept entries and the total it was given, because the hint row a filter that
// hides everything leaves behind says how many are there in all.
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

// sidebarSortAgents orders the section. Both orders are stable, so panes at the
// same rank or the same instant keep the order the tree gave them and no row
// moves under the pointer for a reason the user cannot see.
func (m *OS) sidebarSortAgents(agents []sidebarAgentEntry) {
	if m.sidebarAgentsSort() == sidebarAgentsRecent {
		sort.SliceStable(agents, func(a, b int) bool { return agents[a].StateAt > agents[b].StateAt })
		return
	}
	sort.SliceStable(agents, func(a, b int) bool {
		return sidebarAgentPriority(agents[a].State, agents[a].DoneSeen) >
			sidebarAgentPriority(agents[b].State, agents[b].DoneSeen)
	})
}
