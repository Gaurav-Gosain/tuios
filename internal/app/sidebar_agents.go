package app

import (
	"image/color"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The agents section answers "what should I handle next", which is a different
// question from the one the rest of the rail answers. Its two controls live in
// its own header and nowhere else: a filter, because a rail watching six
// sessions is not always a rail you want six sessions' worth of alarm from, and
// a sort, because "what needs me", "loudest" and "newest" are all reasonable
// readings of a list of running agents and none is right for everyone.

// Agents-section filter and sort values. They are the strings written to
// sidebar.json, so they are the wire format as much as they are the state.
const (
	sidebarAgentsAll      = "all"
	sidebarAgentsSession  = "session"
	sidebarAgentsNeedsYou = "needs_you"
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

// sidebarAgentsSort is needs-you unless the user picked another. An explicit
// "priority" in a state file is a choice the user made with the header
// control, so it stands; only an empty or unknown value takes the default.
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

// The groups of the needs-you order, in the order they are drawn. Each is what
// a row wants from the person looking at the rail.
const (
	// sidebarGroupNeedsYou is a pane blocked on a person: an approval, a
	// question, or an error.
	sidebarGroupNeedsYou = iota
	// sidebarGroupFinished is a pane that finished and has not been looked at.
	sidebarGroupFinished
	// sidebarGroupWorking is a pane in flight. It needs nothing yet.
	sidebarGroupWorking
	// sidebarGroupIdle is everything at rest: idle, a finished pane already
	// looked at, a pane whose state nothing can read, and any state this build
	// does not know.
	sidebarGroupIdle
)

// sidebarAgentGroup is the needs-you group a pane belongs to.
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

// sidebarSortAgents orders the section. Every order is stable, so panes at the
// same rank or the same instant keep the order the tree gave them and no row
// moves under the pointer for a reason the user cannot see.
//
// The needs-you order is groups only. Inside a group the rows keep the tree's
// order, which is spawn order: sessions as the daemon created them (or as the
// user dragged them), panes in the order they were opened. So a row moves only
// when its group changes, which is when what it wants from you changed, and
// never because a neighbour did something. Priority sorts errored above
// needs_input and recency sorts by the last state change, and both of those
// reorder a group every time one of its members moves.
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

// sidebarFoldClock is the clock the rail's signature reads the minute from
// for the fold. Tests replace it.
var sidebarFoldClock = time.Now

// sidebarAgentFoldID is the identity of the fold entry: no session is called
// this, so the entry never matches a pane.
const sidebarAgentFoldID = "\x00fold"

// sidebarAgentFoldMin is the fewest rows the fold takes. One row folded into
// one line saves nothing and hides a name.
const sidebarAgentFoldMin = 2

// sidebarAgentRests reports whether a row has been at rest long enough to
// fold: idle, unknown or a finished turn already seen, in that state for at
// least the threshold, not the focused pane and with nothing queued for it. A
// row that needs the person, a finished turn not yet seen and a working agent
// never fold.
func sidebarAgentRests(e sidebarAgentEntry, threshold time.Duration, now time.Time) bool {
	if threshold <= 0 || e.Fold > 0 || e.Focused || e.Queued > 0 || e.StateAt <= 0 {
		return false
	}
	if sidebarAgentGroup(e.State, e.DoneSeen) != sidebarGroupIdle {
		return false
	}
	return now.Sub(time.Unix(0, e.StateAt)) >= threshold
}

// sidebarFoldAgents folds the rows long at rest into one entry at the end of
// the section, "+3 at rest", when there are at least two of them and the
// person has not unfolded them. The rest keep their order. It is computed
// while the rail is rebuilt, from the state stamps the rows already carry, so
// it costs no timer: the rail's signature folds the minute once an agent has
// been seen, which is what moves a row over the threshold.
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

// refoldAgentsOnPaneFocus folds the rows at rest again when a pane takes the
// focus while the rail does not have the keyboard: the end of an unfold a
// click made. With the keyboard on the rail, ExitSidebarFocus does it.
func (m *OS) refoldAgentsOnPaneFocus() {
	if !m.sidebarAgentsUnfolded || m.SidebarFocused {
		return
	}
	m.sidebarAgentsUnfolded = false
	m.sidebarCache.invalidate()
}

// sidebarAgentFoldRow draws the fold line, "+3 at rest", muted, at the name
// column. On a tall section the second line names the folded panes.
func (m *OS) sidebarAgentFoldRow(e sidebarAgentEntry, cw int, pal overlay.Palette, st sidebarRowState, names bool) string {
	var rowBg color.Color
	fg := pal.FgMute
	if st.lit() {
		rowBg, fg = pal.Surface, pal.Fg
	}
	indent := sidebarNameCol
	text := "+" + strconv.Itoa(e.Fold) + " at rest"
	if names {
		indent++
		text, fg = e.FoldNames, pal.FgMute
	}
	return sidebarFit(sidebarStyle(rowBg, nil).Render(strings.Repeat(" ", indent))+
		sidebarStyle(rowBg, fg).Render(overlay.Truncate(text, max(sidebarNameAvail(cw, 0)-(indent-sidebarNameCol), 1))), cw, rowBg)
}
