package app

import (
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// The subagents of an agent pane, as the client draws them: the waiting mark
// on a pane whose main agent is at rest while subagents it started still
// run, and a row per subagent under its pane's row on the rail. The daemon
// keeps the list (internal/session/agent_subagents.go); this only draws what
// the last sync carried, and a client of an older daemon, which sends only
// the count, draws the count as before.
//
// Nothing here runs a timer. A child row's elapsed time moves with the
// rail's minute bucket, and a finished row goes when the daemon prunes it.

// agentStateWaiting is a display state, never an agent state: a pane whose
// agent is idle or done while subagents it started still run. The daemon
// keeps the pane done, so a wait, an alert or the Inbox see a finished turn;
// only the mark and the words say the work goes on.
const agentStateWaiting session.AgentState = "waiting"

// displayState is the state a pane's mark and state words show: waiting for
// a pane at rest with running subagents, else the state with the unread bit
// folded in (sidebarGlyphState). working, needs_input, errored and unknown
// show as they are, since the main agent's own state says more than "at
// rest". Every surface that draws a pane's mark calls it, so the rail, a
// title bar and the switcher cannot disagree.
func displayState(state string, doneSeen bool, running int) string {
	if running > 0 && (state == string(session.AgentStateDone) || state == string(session.AgentStateIdle)) {
		return string(agentStateWaiting)
	}
	return sidebarGlyphState(state, doneSeen)
}

// subagentListMax bounds the subagents a client keeps for one pane, whatever
// a daemon sends.
const subagentListMax = 16

// subagentTextMax bounds each string of one, in bytes.
const subagentTextMax = 80

// subagentsFromWire is the synced list as the client keeps it, bounded, and
// cur itself when nothing changed, so the rail's signature sees no change.
func subagentsFromWire(cur []sessiontree.Subagent, in []session.SubagentInfo) []sessiontree.Subagent {
	if len(in) == 0 {
		return nil
	}
	in = in[:min(len(in), subagentListMax)]
	out := make([]sessiontree.Subagent, len(in))
	for i, s := range in {
		out[i] = sessiontree.Subagent{
			ID:          clampBytes(s.ID, subagentTextMax),
			Type:        clampBytes(s.Type, subagentTextMax),
			Description: clampBytes(s.Description, subagentTextMax),
			State:       clampBytes(s.State, subagentTextMax),
			StartedAt:   s.StartedAt,
			EndedAt:     s.EndedAt,
			Now:         clampBytes(s.Now, subagentTextMax),
			Last:        clampBytes(s.Last, subagentTextMax),
			Tools:       s.Tools,
			Result:      clampBytes(s.Result, subagentTextMax),
		}
	}
	if slices.Equal(cur, out) {
		return cur
	}
	return out
}

// clampBytes cuts s to n bytes on a rune boundary.
func clampBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// subagentMarkState is the agent state whose mark and ink a subagent's row
// wears: working while it runs, done, errored when it failed, and idle's
// hollow circle when it was stopped.
func subagentMarkState(s sessiontree.Subagent) string {
	switch s.State {
	case session.SubagentRunning:
		return string(session.AgentStateWorking)
	case session.SubagentDone:
		return string(session.AgentStateDone)
	case session.SubagentFailed:
		return string(session.AgentStateErrored)
	}
	return string(session.AgentStateIdle)
}

// subagentName is what a subagent's row calls it: its description, else its
// type, else "subagent".
func subagentName(s sessiontree.Subagent) string {
	if d := printableTitle(s.Description); d != "" {
		return d
	}
	if t := printableTitle(s.Type); t != "" {
		return t
	}
	return "subagent"
}

// subagentNote is the second line of a subagent's row: the tool it runs, the
// tool it ran last while between calls, and once it ended, how.
func subagentNote(s sessiontree.Subagent) string {
	switch s.State {
	case session.SubagentRunning:
		if now := printableTitle(s.Now); now != "" {
			return now
		}
		return printableTitle(s.Last)
	case session.SubagentDone:
		return "done" + subagentToolCount(s.Tools)
	case session.SubagentFailed:
		if r := printableTitle(s.Result); r != "" {
			return "failed: " + r
		}
		return "failed"
	}
	return "stopped"
}

// subagentToolCount is " · 14 tools" for a finished row, or nothing.
func subagentToolCount(n int) string {
	switch {
	case n == 1:
		return sidebarAgentSep() + "1 tool"
	case n > 1:
		return sidebarAgentSep() + strconv.Itoa(n) + " tools"
	}
	return ""
}

// subagentAge is how long a subagent ran, as a rail row says an age: from
// its start to now while it runs, frozen at its length once it ended, and
// blank under railAgeFloor.
func subagentAge(s sessiontree.Subagent, now time.Time) string {
	if s.EndedAt > 0 {
		now = time.Unix(0, s.EndedAt)
	}
	return railAgentAge(string(session.AgentStateWorking), s.StartedAt, now)
}

// subagentLine is a subagent as the Inbox says it: its mark, its name, and
// what its row's second line says.
func subagentLine(s sessiontree.Subagent) string {
	return agentStateIndicator(subagentMarkState(s)) + " " + subagentName(s) + sidebarAgentSep() + subagentNote(s)
}

// runningSubagents counts the subagents of list that run.
func runningSubagents(list []sessiontree.Subagent) int {
	n := 0
	for _, s := range list {
		if s.Running() {
			n++
		}
	}
	return n
}

// subagentsRunText is the parent row's note when its subagents have rows:
// "3 of 4 run", or "" when none runs.
func subagentsRunText(running, total int) string {
	if running <= 0 {
		return ""
	}
	return strconv.Itoa(running) + " of " + strconv.Itoa(total) + " run"
}

// subagentsWaitText is how the Inbox says a finished pane still has work
// going on: "waits for 3 subagents", "" at none.
func subagentsWaitText(n int) string {
	if n <= 0 {
		return ""
	}
	return "waits for " + session.SubagentsText(n)
}

// paneSubagentList is the subagents a pane of this machine reported, as this
// client holds them.
func (m *OS) paneSubagentList(sessionID, windowID string) []sessiontree.Subagent {
	if sessionID == "" || sessionID == m.sidebarCurrentSessionID() {
		for _, w := range m.Windows {
			if w != nil && w.ID == windowID {
				return w.AgentSubagentList
			}
		}
	}
	if m.DaemonClient == nil || m.AttachedHost != "" {
		return nil
	}
	for _, w := range m.DaemonClient.SessionWindows(sessionID) {
		if w.ID == windowID {
			return subagentsFromWire(nil, w.SubagentList)
		}
	}
	return nil
}

// paneSubagentCount is how many subagents a pane of this machine is running,
// as this client holds it: the count, which an older daemon sends without
// the list.
func (m *OS) paneSubagentCount(sessionID, windowID string) int {
	if sessionID == "" || sessionID == m.sidebarCurrentSessionID() {
		for _, w := range m.Windows {
			if w != nil && w.ID == windowID {
				return w.AgentSubagents
			}
		}
	}
	if m.DaemonClient == nil || m.AttachedHost != "" {
		return 0
	}
	for _, w := range m.DaemonClient.SessionWindows(sessionID) {
		if w.ID == windowID {
			return w.Subagents
		}
	}
	return 0
}

// subagentDetailLines are the Inbox's lines about a pane's subagents: one
// per subagent, the same text as its rail row, under a line that says how
// many run.
func subagentDetailLines(list []sessiontree.Subagent) []string {
	if len(list) == 0 {
		return nil
	}
	head := "Subagents:"
	if n := runningSubagents(list); n > 0 {
		head = capitalize(subagentsWaitText(n)) + ":"
	}
	lines := make([]string, 0, len(list)+1)
	lines = append(lines, head)
	for _, s := range list {
		lines = append(lines, "  "+subagentLine(s))
	}
	return lines
}

// sidebarChildPlan is how many child rows each parent row of the agents
// section gets, and whether that is fewer than its pane has.
type sidebarChildPlan struct {
	// rows is the section's entries with the child entries in place.
	rows []sidebarAgentEntry
	// cut says some subagent has no row of its own: finished ones were left
	// out, or a parent's rows end in "+N more", or no parent has any.
	cut bool
}

// sidebarAgentChildren puts a row for each subagent under its pane's row,
// for a section whose budget holds room rows of one line each. The section
// never hides a parent row to show a child row:
//
//  1. room for every parent and every subagent: all of them;
//  2. else room without the finished ones: the running ones;
//  3. else each parent with running subagents gets at least one row, the
//     focused pane's parent first and then the one whose newest subagent
//     started last, and a parent with more than its rows ends in "+N more";
//  4. else none, and the parent's row says the count, as before.
//
// With appearance.sidebar.subagent_rows = "count" it is always step 4.
func (m *OS) sidebarAgentChildren(parents []sidebarAgentEntry, room int) sidebarChildPlan {
	all, running := 0, 0
	for _, p := range parents {
		all += len(p.Children)
		running += runningSubagents(p.Children)
	}
	if all == 0 {
		return sidebarChildPlan{rows: parents}
	}
	if !m.Settings.SidebarSubagentRows {
		return sidebarChildPlan{rows: parents}
	}
	n := len(parents)
	switch {
	case n+all <= room:
		return sidebarChildPlan{rows: sidebarWithChildren(parents, nil, false)}
	case running > 0 && n+running <= room:
		return sidebarChildPlan{rows: sidebarWithChildren(parents, nil, true), cut: true}
	}
	// Step 3: the parents with running subagents, in the order they get
	// their rows.
	slots := room - n
	var order []int
	for i, p := range parents {
		if runningSubagents(p.Children) > 0 {
			order = append(order, i)
		}
	}
	if len(order) == 0 || slots < len(order) {
		return sidebarChildPlan{rows: parents, cut: true}
	}
	newest := func(p sidebarAgentEntry) int64 {
		var at int64
		for _, s := range p.Children {
			if s.Running() {
				at = max(at, s.StartedAt)
			}
		}
		return at
	}
	slices.SortStableFunc(order, func(a, b int) int {
		pa, pb := parents[a], parents[b]
		switch {
		case pa.Focused != pb.Focused:
			if pa.Focused {
				return -1
			}
			return 1
		case newest(pa) != newest(pb):
			if newest(pa) > newest(pb) {
				return -1
			}
			return 1
		}
		return 0
	})
	alloc := make([]int, len(parents))
	for _, i := range order {
		alloc[i] = 1
	}
	slots -= len(order)
	for slots > 0 {
		gave := false
		for _, i := range order {
			if slots > 0 && alloc[i] < runningSubagents(parents[i].Children) {
				alloc[i]++
				slots--
				gave = true
			}
		}
		if !gave {
			break
		}
	}
	return sidebarChildPlan{rows: sidebarWithChildren(parents, alloc, true), cut: true}
}

// sidebarWithChildren is parents with their child entries after each one.
// alloc, when not nil, is how many rows each parent may use, the last of them
// "+N more" when its running subagents are more. runningOnly leaves the
// finished subagents out.
func sidebarWithChildren(parents []sidebarAgentEntry, alloc []int, runningOnly bool) []sidebarAgentEntry {
	out := make([]sidebarAgentEntry, 0, len(parents)*2)
	for i, p := range parents {
		var kids []sessiontree.Subagent
		for _, s := range p.Children {
			if !runningOnly || s.Running() {
				kids = append(kids, s)
			}
		}
		more := 0
		if alloc != nil {
			switch a := alloc[i]; {
			case a == 0:
				kids = nil
			case a < len(kids):
				// The newest of them, in start order, then the count of the
				// rest.
				more = len(kids) - (a - 1)
				kids = kids[len(kids)-(a-1):]
			}
		}
		p.ChildRows = len(kids)
		if more > 0 {
			p.ChildRows++
		}
		out = append(out, p)
		for j := range kids {
			child := sidebarAgentEntry{
				SessionID: p.SessionID, WindowID: p.WindowID, WindowIndex: p.WindowIndex,
				Title: p.Title, State: p.State, DoneSeen: p.DoneSeen, Foreign: p.Foreign, Host: p.Host,
				Child: &kids[j],
			}
			out = append(out, child)
		}
		if more > 0 {
			out = append(out, sidebarAgentEntry{
				SessionID: p.SessionID, WindowID: p.WindowID, WindowIndex: p.WindowIndex,
				Title: p.Title, State: p.State, DoneSeen: p.DoneSeen, Foreign: p.Foreign, Host: p.Host,
				More: more, Lead: len(kids) > 0,
			})
		}
	}
	return out
}

// sidebarChildIndent is where a child row's mark sits: one cell past the
// name spine, the note line's indent, so the row reads as part of its
// parent's entry.
const sidebarChildIndent = sidebarNameCol + 1

// sidebarChildRow draws one line of a subagent's row. The first line is its
// mark, its name and how long it ran at the right edge; the second, under
// a tall section, is the tool it runs or how it ended, in the quiet ink. A
// finished row is drawn muted, its mark too unless it failed, so the rows
// that still run stand out until the daemon takes the finished ones away.
func (m *OS) sidebarChildRow(e sidebarAgentEntry, cw int, pal overlay.Palette, st sidebarRowState, second bool) string {
	rowBg := sidebarRowBg(st, pal)
	pad := sidebarStyle(rowBg, nil)
	if e.More > 0 {
		text := "+" + strconv.Itoa(e.More) + " more"
		if !e.Lead {
			text = session.SubagentsText(e.More)
		}
		if second {
			return sidebarFit(pad.Render(""), cw, rowBg)
		}
		fg := pal.FgMute
		if st.lit() {
			fg = pal.Fg
		}
		return sidebarFit(pad.Render(strings.Repeat(" ", sidebarChildIndent+2))+
			sidebarStyle(rowBg, fg).Render(overlay.Truncate(text, max(cw-sidebarChildIndent-2, 1))), cw, rowBg)
	}
	s := *e.Child
	ended := !s.Running()
	if second {
		indent := sidebarChildIndent + 2
		return sidebarFit(pad.Render(strings.Repeat(" ", indent))+
			sidebarStyle(rowBg, pal.FgMute).Render(overlay.Truncate(subagentNote(s), max(cw-indent, 1))), cw, rowBg)
	}
	markState := subagentMarkState(s)
	markFg := agentGlyphColor(markState, pal)
	nameFg := pal.FgDim
	if ended {
		nameFg = pal.FgMute
		if s.State != session.SubagentFailed {
			markFg = pal.FgMute
		}
	}
	if st.lit() {
		nameFg = pal.Fg
	}
	mark := " "
	if m.Settings.SidebarShowGlyphs {
		mark = agentStateIndicator(markState)
	}
	age := subagentAge(s, time.Now())
	if age == "" && st.lit() {
		end := time.Now()
		if s.EndedAt > 0 {
			end = time.Unix(0, s.EndedAt)
		}
		age = agentElapsed(string(session.AgentStateWorking), s.StartedAt, end)
	}
	right := ""
	if age != "" {
		right = sidebarStyle(rowBg, pal.FgMute).Render(age)
	}
	rightW := len(age)
	if rightW > 0 {
		rightW++
	}
	avail := max(cw-sidebarChildIndent-2-rightW, 1)
	body := pad.Render(strings.Repeat(" ", sidebarChildIndent)) +
		sidebarStyle(rowBg, markFg).Render(mark) + pad.Render(" ") +
		sidebarStyle(rowBg, nameFg).Render(overlay.Truncate(subagentName(s), avail))
	if right == "" {
		return sidebarFit(body, cw, rowBg)
	}
	gap := max(cw-lipgloss.Width(body)-rightW+1, 1)
	return sidebarFit(body+pad.Render(strings.Repeat(" ", gap))+right, cw, rowBg)
}

// sidebarAgentsHaveChildren reports whether any agent row has subagents the
// daemon listed.
func sidebarAgentsHaveChildren(agents []sidebarAgentEntry) bool {
	return slices.ContainsFunc(agents, func(e sidebarAgentEntry) bool { return len(e.Children) > 0 })
}

// sidebarAgentChildCount is how many subagents the agent rows list.
func sidebarAgentChildCount(agents []sidebarAgentEntry) int {
	n := 0
	for _, e := range agents {
		n += len(e.Children)
	}
	return n
}
