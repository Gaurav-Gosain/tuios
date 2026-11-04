package app

import (
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The children's headline: one line under a parent agent's row that says how
// its child agents are doing, "4 agents · 1 needs you · 2 working · 1 done",
// and folds their rows away when the person shuts it.
//
// It counts the agents other agents started (the parent link, see
// sidebar_agent_nest.go), every level under the parent, since those are the
// rows the fold hides. A harness's own subagents are left out: they have a
// heading and a count of their own (subagents.go), in their own words (run,
// done, failed), and a pane at rest with subagents at work already reads
// "waiting". Counting them here too would say the same work twice.
//
// Everything is read from the rows the section already built: no verb, no
// timer. The open or shut state is client memory, kept beside the subagent
// headings' and under the same generation, so the rail's signature sees a
// toggle without a field of its own.

// sidebarKinKey is the key a parent's headline keeps its fold under, in the
// map the subagent headings use. The prefix keeps the two apart.
func sidebarKinKey(sessionID, windowID string) string {
	return "kin\x00" + sidebarSubagentKey(sessionID, windowID)
}

// sidebarKinOpen reports whether a parent's child rows are drawn under its
// headline. Open until the person shuts it.
func (m *OS) sidebarKinOpen(sessionID, windowID string) bool {
	if open, ok := m.sidebarSubagentFold[sidebarKinKey(sessionID, windowID)]; ok {
		return open
	}
	return true
}

// SidebarToggleKin opens or shuts the child rows under a parent's headline.
// It reports false when the pane has no child rows on the rail, so the key
// falls through.
func (m *OS) SidebarToggleKin(sessionID, windowID string) bool {
	if windowID == "" || !m.sidebarHasKin(sessionID, windowID) {
		return false
	}
	if m.sidebarSubagentFold == nil {
		m.sidebarSubagentFold = make(map[string]bool, 1)
	}
	k := sidebarKinKey(sessionID, windowID)
	m.sidebarSubagentFold[k] = !m.sidebarKinOpen(sessionID, windowID)
	m.sidebarSubagentFoldGen++
	m.sidebarCache.invalidate()
	return true
}

// sidebarHasKin reports whether an agent pane of this machine is the parent
// of another agent pane the rail lists.
func (m *OS) sidebarHasKin(sessionID, windowID string) bool {
	for _, e := range m.agentEntries(m.BuildSessionTree().Sessions) {
		if e.Host == "" && e.Parent != nil && e.Parent.Session == sessionID && e.Parent.Window == windowID {
			return true
		}
	}
	return false
}

// sidebarKinHeads puts a headline row under each parent row of a nested list
// (sidebarNestAgents), and leaves out the rows under a shut one. A parent's
// children are the rows after it with a deeper Nest. A list with no nesting
// is returned as it is.
func (m *OS) sidebarKinHeads(agents []sidebarAgentEntry) []sidebarAgentEntry {
	nested := false
	for _, e := range agents {
		if e.Nest > 0 {
			nested = true
			break
		}
	}
	if !nested {
		return agents
	}
	out := make([]sidebarAgentEntry, 0, len(agents)+4)
	for i := 0; i < len(agents); i++ {
		p := agents[i]
		out = append(out, p)
		end := i + 1
		for end < len(agents) && agents[end].Nest > p.Nest {
			end++
		}
		if end == i+1 {
			continue
		}
		kids := agents[i+1 : end]
		open := m.sidebarKinOpen(p.SessionID, p.WindowID)
		out = append(out, sidebarAgentEntry{
			SessionID: p.SessionID, WindowID: p.WindowID, WindowIndex: p.WindowIndex,
			Title: p.Title, Foreign: p.Foreign,
			Kin: sidebarKinHeadline(kids), KinNames: sidebarKinNames(kids),
			Open: open, Nest: p.Nest + 1,
		})
		if !open {
			i = end - 1
		}
	}
	return out
}

// sidebarKinHeadline is the headline of a parent's child rows: how many, then
// how many are in each state, the ones that want the person first. A state
// with none is left out.
func sidebarKinHeadline(kids []sidebarAgentEntry) string {
	order := []string{"needs_input", "errored", "working", string(agentStateWaiting), "done", "idle"}
	counts := make(map[string]int, len(order))
	for _, k := range kids {
		counts[sidebarKinState(k)]++
	}
	n := len(kids)
	parts := []string{strconv.Itoa(n) + " agent"}
	if n != 1 {
		parts[0] += "s"
	}
	for _, s := range order {
		c := counts[s]
		if c == 0 {
			continue
		}
		word := sidebarStateWords(s)
		if s == "needs_input" {
			word = agentNeedsYou(c)
		}
		parts = append(parts, strconv.Itoa(c)+" "+word)
	}
	return strings.Join(parts, sidebarAgentSep())
}

// sidebarKinState is the state a child counts under: its display state, with
// a finished turn counted as done whether or not the person saw it, and
// anything at rest that is not done counted as idle.
func sidebarKinState(e sidebarAgentEntry) string {
	switch s := displayState(e.State, e.DoneSeen, e.Subagents); s {
	case "needs_input", "errored", "working", string(agentStateWaiting):
		return s
	}
	if e.State == string(session.AgentStateDone) {
		return "done"
	}
	return "idle"
}

// sidebarKinNames is the second line of a shut headline on a tall section:
// the names of the agents it holds.
func sidebarKinNames(kids []sidebarAgentEntry) string {
	names := make([]string, 0, len(kids))
	for _, k := range kids {
		names = append(names, sidebarAgentName(k))
	}
	return strings.Join(names, ", ")
}

// sidebarKinRow draws a headline line: the fold mark in the children's mark
// column, then the headline. The second line of a tall section names the
// children while they are folded away.
func (m *OS) sidebarKinRow(e sidebarAgentEntry, cw int, pal overlay.Palette, st sidebarRowState, second bool) string {
	rowBg := sidebarRowBg(st, pal)
	pad := sidebarStyle(rowBg, nil)
	indent := e.nestIndent()
	if second {
		if e.Open || e.KinNames == "" {
			return sidebarFit("", cw, rowBg)
		}
		lead := pad.Render(strings.Repeat(" ", sidebarNameCol()+indent))
		return sidebarFit(lead+sidebarStyle(rowBg, pal.FgMute).Render(
			overlay.Truncate(e.KinNames, sidebarNameAvailIn(cw, 0, indent))), cw, rowBg)
	}
	mark := m.Settings.GetRailFoldOpenGlyph()
	if !e.Open {
		mark = m.Settings.GetRailFoldShutGlyph()
	}
	fg := pal.FgDim
	if st.lit() {
		fg = pal.Fg
	}
	body := sidebarStyle(rowBg, fg).Render(overlay.Truncate(e.Kin, sidebarNameAvailIn(cw, 0, indent)))
	return sidebarComposeGroupRow(indent, pad.Render(" "), sidebarStyle(rowBg, pal.FgMute).Render(mark), body, "", cw, rowBg)
}
