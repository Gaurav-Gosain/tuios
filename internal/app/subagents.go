package app

import (
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
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
	// cut says some subagent has no row of its own for want of room: finished
	// ones were left out, or a parent's rows end in "+N more", or no parent
	// has any. A row the options hide (a shut heading, the row cap, an early
	// hide) is not a cut: the person chose it, and the room it frees may still
	// go to the second lines.
	cut bool
}

// sidebarChildParent is what the plan knows of one parent row's subagents.
type sidebarChildParent struct {
	// kids are the subagents the options leave on the rail, in start order.
	kids []sessiontree.Subagent
	// running is how many of kids run.
	running int
	// head says the parent has a heading line over its rows.
	head bool
	// open says the rows under the heading are drawn. Always true without a
	// heading.
	open bool
}

// rowLines is how many lines n subagents take under this parent, the
// heading included, with the row cap applied.
func (c sidebarChildParent) rowLines(n int, v config.SidebarSubagentView) int {
	if n <= 0 {
		return 0
	}
	lines := 0
	if c.head {
		lines++
	}
	if c.open {
		lines += sidebarCapRows(n, v)
	}
	return lines
}

// sidebarCapRows is n under appearance.sidebar.subagent_max_rows.
func sidebarCapRows(n int, v config.SidebarSubagentView) int {
	if v.MaxRows > 0 {
		return min(n, v.MaxRows)
	}
	return n
}

// sidebarSubagentKey is the identity a heading's open or shut state is kept
// under: the pane.
func sidebarSubagentKey(sessionID, windowID string) string {
	return sessionID + "\x00" + windowID
}

// sidebarSubagentsOpen reports whether a pane's subagent rows are open under
// their heading: what the person last chose for the pane on this rail, else
// what appearance.sidebar.subagent_collapse starts with.
func (m *OS) sidebarSubagentsOpen(sessionID, windowID string) bool {
	if open, ok := m.sidebarSubagentFold[sidebarSubagentKey(sessionID, windowID)]; ok {
		return open
	}
	return m.Settings.SidebarSubagents.Collapse != config.SidebarSubagentCollapseCollapsed
}

// SidebarToggleSubagents opens or shuts a pane's subagent rows under their
// heading, and keeps the choice for this rail until the client ends. It
// reports false when the pane has no heading on this rail (no rows, no
// collapse option, or no subagent left after the hide option), so the key
// falls through and nothing is stored for a heading nobody can see.
func (m *OS) SidebarToggleSubagents(sessionID, windowID string) bool {
	v := m.Settings.SidebarSubagents
	if !v.Rows || !v.Collapsible() || windowID == "" {
		return false
	}
	if len(m.sidebarSubagentsShown(m.paneSubagentList(sessionID, windowID), time.Now())) == 0 {
		return false
	}
	open := m.sidebarSubagentsOpen(sessionID, windowID)
	if m.sidebarSubagentFold == nil {
		m.sidebarSubagentFold = make(map[string]bool, 1)
	}
	m.sidebarSubagentFold[sidebarSubagentKey(sessionID, windowID)] = !open
	m.sidebarSubagentFoldGen++
	m.sidebarCache.invalidate()
	return true
}

// sidebarSubagentsShown is list without the finished subagents that
// appearance.sidebar.subagent_hide_finished hides by now. It notes the
// earliest hide still to come, so Update can ask for a frame then.
func (m *OS) sidebarSubagentsShown(list []sessiontree.Subagent, now time.Time) []sessiontree.Subagent {
	v := m.Settings.SidebarSubagents
	if !v.Hide || len(list) == 0 {
		return list
	}
	var out []sessiontree.Subagent
	for i, s := range list {
		if s.Running() || s.EndedAt <= 0 {
			if out != nil {
				out = append(out, s)
			}
			continue
		}
		at := time.Unix(0, s.EndedAt).Add(v.HideAfter)
		if at.After(now) {
			m.noteSubagentHide(at)
			if out != nil {
				out = append(out, s)
			}
			continue
		}
		if out == nil {
			out = make([]sessiontree.Subagent, i, len(list))
			copy(out, list[:i])
		}
	}
	if out == nil {
		return list
	}
	return out
}

// sidebarSubagentHidden reports whether the hide option hides a subagent at
// now. The rail's signature folds it, so a row goes on the frame it should.
func sidebarSubagentHidden(v config.SidebarSubagentView, s sessiontree.Subagent, now time.Time) bool {
	return v.Hide && !s.Running() && s.EndedAt > 0 && !time.Unix(0, s.EndedAt).Add(v.HideAfter).After(now)
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
// With appearance.sidebar.subagent_rows = "count" it is always step 4. The
// other subagent options act before the steps: a hidden finished row is not
// there to place, a heading line costs one line, a shut heading costs only
// that line, and subagent_max_rows caps each parent's lines.
func (m *OS) sidebarAgentChildren(parents []sidebarAgentEntry, room int) sidebarChildPlan {
	v := m.Settings.SidebarSubagents
	if !v.Rows || !sidebarAgentsHaveChildren(parents) {
		return sidebarChildPlan{rows: parents}
	}
	now := time.Now()
	info := make([]sidebarChildParent, len(parents))
	all, running := 0, 0
	anyRunning := false
	for i, p := range parents {
		kids := m.sidebarSubagentsShown(p.Children, now)
		c := sidebarChildParent{kids: kids, running: runningSubagents(kids), head: v.Collapsible() && len(kids) > 0, open: true}
		if c.head {
			c.open = m.sidebarSubagentsOpen(p.SessionID, p.WindowID)
		}
		info[i] = c
		all += c.rowLines(len(kids), v)
		running += c.rowLines(c.running, v)
		anyRunning = anyRunning || c.running > 0
	}
	if all == 0 {
		return sidebarChildPlan{rows: parents}
	}
	n := len(parents)
	switch {
	case n+all <= room:
		return sidebarChildPlan{rows: sidebarWithChildren(parents, info, nil, false, v)}
	case anyRunning && n+running <= room:
		return sidebarChildPlan{rows: sidebarWithChildren(parents, info, nil, true, v), cut: true}
	}
	// Step 3: the parents with running subagents, in the order they get
	// their rows. Each takes its heading and, when open, one row.
	slots := room - n
	var order []int
	need := 0
	for i, c := range info {
		if c.running > 0 {
			order = append(order, i)
			need += c.rowLines(1, v)
		}
	}
	if len(order) == 0 || slots < need {
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
		if info[i].open {
			alloc[i] = 1
		}
	}
	slots -= need
	for slots > 0 {
		gave := false
		for _, i := range order {
			if slots > 0 && info[i].open && alloc[i] < sidebarCapRows(info[i].running, v) {
				alloc[i]++
				slots--
				gave = true
			}
		}
		if !gave {
			break
		}
	}
	return sidebarChildPlan{rows: sidebarWithChildren(parents, info, alloc, true, v), cut: true}
}

// sidebarWithChildren is parents with their child entries after each one: a
// heading when the parent has one, then a row per subagent while it is open.
// alloc, when not nil, is how many row lines each parent may use, and a
// parent at zero gets no lines at all; nil is each parent's capped count.
// When the lines are fewer than the subagents the last of them is "+N more".
// runningOnly leaves the finished subagents out.
func sidebarWithChildren(parents []sidebarAgentEntry, info []sidebarChildParent, alloc []int, runningOnly bool, v config.SidebarSubagentView) []sidebarAgentEntry {
	out := make([]sidebarAgentEntry, 0, len(parents)*2)
	for i, p := range parents {
		c := info[i]
		var kids []sessiontree.Subagent
		for _, s := range c.kids {
			if !runningOnly || s.Running() {
				kids = append(kids, s)
			}
		}
		if len(kids) == 0 || (alloc != nil && c.running == 0) {
			p.ChildRows = 0
			out = append(out, p)
			continue
		}
		lines := sidebarCapRows(len(kids), v)
		if alloc != nil {
			lines = alloc[i]
		}
		if !c.open {
			lines = 0
		}
		more := 0
		if lines < len(kids) {
			if lines == 0 {
				// A shut heading stands for them all.
				kids = nil
			} else {
				// The newest of them, in start order, then the count of the
				// rest.
				more = len(kids) - (lines - 1)
				kids = kids[len(kids)-(lines-1):]
			}
		}
		p.ChildRows = len(kids)
		if more > 0 {
			p.ChildRows++
		}
		if c.head {
			p.ChildRows++
		}
		out = append(out, p)
		sub := func() sidebarAgentEntry {
			return sidebarAgentEntry{
				SessionID: p.SessionID, WindowID: p.WindowID, WindowIndex: p.WindowIndex,
				Title: p.Title, State: p.State, DoneSeen: p.DoneSeen, Foreign: p.Foreign, Host: p.Host,
			}
		}
		if c.head {
			h := sub()
			h.Head = true
			h.HeadKids = c.kids
			h.Open = c.open
			h.Last = len(kids) == 0 && more == 0
			out = append(out, h)
		}
		for j := range kids {
			child := sub()
			child.Child = &kids[j]
			child.Last = j == len(kids)-1 && more == 0
			out = append(out, child)
		}
		if more > 0 {
			mr := sub()
			mr.More, mr.Lead, mr.Last = more, len(kids) > 0, true
			out = append(out, mr)
		}
	}
	return out
}

// sidebarChildIndent is where a child row's mark sits: one cell past the
// name spine, the note line's indent, so the row reads as part of its
// parent's entry.
const sidebarChildIndent = sidebarNameCol + 1

// sidebarChildLead is the indent in front of a child row's mark, and on its
// second line in front of the note: blanks for the rows as a list, and tree
// lines from the parent's mark column for subagent_rows = "tree". It is
// sidebarChildIndent cells wide on the first line either way, so the names
// line up whichever the person picks.
func (m *OS) sidebarChildLead(e sidebarAgentEntry, rowBg color.Color, pal overlay.Palette, second bool) string {
	pad := sidebarStyle(rowBg, nil)
	if !m.Settings.SidebarSubagents.Tree {
		return pad.Render(strings.Repeat(" ", sidebarChildIndent))
	}
	branch := m.Settings.GetRailTreeBranch()
	if e.Last {
		branch = m.Settings.GetRailTreeLast()
	}
	if second {
		branch = " "
		if !e.Last {
			branch = sidebarTreePipe(m.Settings.GetRailTreeBranch())
		}
	}
	lead := " " + branch
	w := lipgloss.Width(lead)
	if w > sidebarChildIndent {
		lead = overlay.Truncate(lead, sidebarChildIndent)
		w = lipgloss.Width(lead)
	}
	return sidebarStyle(rowBg, pal.FgMute).Render(lead) + pad.Render(strings.Repeat(" ", sidebarChildIndent-w))
}

// sidebarTreePipe is the line a tree branch continues down with: │ under ├,
// | under the ASCII set's |-, and a blank under a glyph set's own branch it
// cannot read a line from.
func sidebarTreePipe(branch string) string {
	switch {
	case strings.HasPrefix(branch, "├"):
		return "│"
	case strings.HasPrefix(branch, "|"):
		return "|"
	}
	return " "
}

// sidebarSubagentNames is the second line of a shut heading on a tall
// section: the names of the subagents it holds, as the fold line names the
// rows it folded.
func sidebarSubagentNames(list []sessiontree.Subagent) string {
	names := make([]string, 0, len(list))
	for _, s := range list {
		names = append(names, subagentName(s))
	}
	return strings.Join(names, ", ")
}

// sidebarChildRow draws one line of a subagent's row. The first line is its
// mark, its name and how long it ran at the right edge; the second, under
// a tall section, is the tool it runs or how it ended, in the quiet ink. A
// finished row is drawn muted, its mark too unless it failed, so the rows
// that still run stand out until the daemon takes the finished ones away.
// appearance.sidebar.subagent_fields says which of those it draws, and the
// heading line and the "+N more" line are drawn here too.
func (m *OS) sidebarChildRow(e sidebarAgentEntry, cw int, pal overlay.Palette, st sidebarRowState, second bool) string {
	rowBg := sidebarRowBg(st, pal)
	pad := sidebarStyle(rowBg, nil)
	fields := m.Settings.SidebarSubagents.Fields
	lead := m.sidebarChildLead(e, rowBg, pal, second)
	if e.Head {
		// The heading says how many; the agent's own row already says how
		// many of them run.
		if second {
			if e.Open || len(e.HeadKids) == 0 {
				return sidebarFit(lead, cw, rowBg)
			}
			indent := sidebarChildIndent + 2
			return sidebarFit(lead+pad.Render("  ")+
				sidebarStyle(rowBg, pal.FgMute).Render(overlay.Truncate(sidebarSubagentNames(e.HeadKids), max(cw-indent, 1))), cw, rowBg)
		}
		mark := m.Settings.GetRailFoldOpenGlyph()
		if !e.Open {
			mark = m.Settings.GetRailFoldShutGlyph()
		}
		fg := pal.FgDim
		if st.lit() {
			fg = pal.Fg
		}
		avail := max(cw-sidebarChildIndent-2, 1)
		return sidebarFit(lead+sidebarStyle(rowBg, pal.FgMute).Render(mark)+pad.Render(" ")+
			sidebarStyle(rowBg, fg).Render(overlay.Truncate(session.SubagentsText(len(e.HeadKids)), avail)), cw, rowBg)
	}
	if e.More > 0 {
		text := "+" + strconv.Itoa(e.More) + " more"
		if !e.Lead {
			text = session.SubagentsText(e.More)
		}
		if second {
			return sidebarFit(lead, cw, rowBg)
		}
		fg := pal.FgMute
		if st.lit() {
			fg = pal.Fg
		}
		return sidebarFit(lead+pad.Render("  ")+
			sidebarStyle(rowBg, fg).Render(overlay.Truncate(text, max(cw-sidebarChildIndent-2, 1))), cw, rowBg)
	}
	s := *e.Child
	ended := !s.Running()
	if second {
		if !fields.Note {
			return sidebarFit(lead, cw, rowBg)
		}
		indent := sidebarChildIndent + 2
		return sidebarFit(lead+pad.Render("  ")+
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
	if m.Settings.SidebarShowGlyphs && fields.Mark {
		mark = agentStateIndicator(markState)
	}
	age := ""
	if fields.Time {
		age = subagentAge(s, time.Now())
		if age == "" && st.lit() {
			end := time.Now()
			if s.EndedAt > 0 {
				end = time.Unix(0, s.EndedAt)
			}
			age = agentElapsed(string(session.AgentStateWorking), s.StartedAt, end)
		}
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
	name, kind := "", ""
	if fields.Name {
		name = subagentName(s)
	}
	if fields.Type {
		kind = printableTitle(s.Type)
		if name == "" && kind == "" {
			kind = "subagent"
		}
	}
	text := sidebarStyle(rowBg, nameFg).Render(overlay.Truncate(name, avail))
	switch {
	case name == "":
		text = sidebarStyle(rowBg, nameFg).Render(overlay.Truncate(kind, avail))
	case kind != "" && kind != name:
		nameW := min(lipgloss.Width(name), avail)
		if rest := avail - nameW - 1; rest > 1 {
			text += pad.Render(" ") + sidebarStyle(rowBg, pal.FgMute).Render(overlay.Truncate(kind, rest))
		}
	}
	body := lead + sidebarStyle(rowBg, markFg).Render(mark) + pad.Render(" ") + text
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

// sidebarSubagentDemand is how many lines the subagents under the agent
// rows ask for: a row each, a heading per agent when the rail draws one, no
// more than subagent_max_rows each, and none for an agent whose heading is
// shut. It asks; sidebarAgentChildren fits what it gets.
func (m *OS) sidebarSubagentDemand(agents []sidebarAgentEntry) int {
	v := m.Settings.SidebarSubagents
	if !v.Rows {
		return 0
	}
	n := 0
	for _, e := range agents {
		if len(e.Children) == 0 {
			continue
		}
		c := sidebarChildParent{head: v.Collapsible(), open: true}
		if c.head {
			c.open = m.sidebarSubagentsOpen(e.SessionID, e.WindowID)
		}
		n += c.rowLines(len(e.Children), v)
	}
	return n
}

// subagentHideMsg is the frame asked for when a finished subagent's row is
// due to go under appearance.sidebar.subagent_hide_finished.
type subagentHideMsg struct{}

// noteSubagentHide keeps at as the next hide due, when it is the earliest.
func (m *OS) noteSubagentHide(at time.Time) {
	if m.subagentHideDue.IsZero() || at.Before(m.subagentHideDue) {
		m.subagentHideDue = at
	}
}

// subagentHideCmd arms one frame for the earliest finished row due to go,
// so the row goes then even when nothing else draws a frame. Update asks it
// after every message, like loadingFrameCmd. It reads the attached
// session's panes and the due the last rail draw noted, which covers the
// panes of other sessions, and costs nothing while the option is off.
func (m *OS) subagentHideCmd() tea.Cmd {
	if !m.subagentHideOn() {
		m.subagentHideDue = time.Time{}
		return nil
	}
	now := time.Now()
	due := m.subagentHideDue
	if !due.After(now) {
		due = time.Time{}
	}
	for _, w := range m.Windows {
		if w == nil {
			continue
		}
		for _, s := range w.AgentSubagentList {
			if s.Running() || s.EndedAt <= 0 {
				continue
			}
			at := time.Unix(0, s.EndedAt).Add(m.subagentHideAfter())
			if at.After(now) && (due.IsZero() || at.Before(due)) {
				due = at
			}
		}
	}
	m.subagentHideDue = time.Time{}
	// A frame already armed at or before due covers it. One armed later, as
	// after a config change shortened the hide, does not: arm the earlier
	// one too. The later tick still comes and draws nothing new.
	if due.IsZero() || (m.subagentHideArmed.After(now) && !m.subagentHideArmed.After(due)) {
		return nil
	}
	m.subagentHideArmed = due
	return tea.Tick(due.Sub(now), func(time.Time) tea.Msg { return subagentHideMsg{} })
}

// subagentHideOn reports whether either rail hides finished rows early.
func (m *OS) subagentHideOn() bool {
	if m.Settings.SidebarSubagents.Hide {
		return true
	}
	return m.secondarySubagentView().Hide
}

// subagentHideAfter is the shorter hide delay of the two rails, for the
// frame that has to come at the first of them.
func (m *OS) subagentHideAfter() time.Duration {
	a, b := m.Settings.SidebarSubagents, m.secondarySubagentView()
	switch {
	case a.Hide && b.Hide:
		return min(a.HideAfter, b.HideAfter)
	case b.Hide:
		return b.HideAfter
	}
	return a.HideAfter
}

// secondarySubagentView is the subagent view of the rail on the other edge,
// the zero view when there is none.
func (m *OS) secondarySubagentView() config.SidebarSubagentView {
	if m.UserConfig == nil || m.secondarySidebarWidth() == 0 {
		return config.SidebarSubagentView{}
	}
	cfg := m.UserConfig.Appearance.Sidebar.Left
	if m.legacySidebarEdge() == sidebarLeft {
		cfg = m.UserConfig.Appearance.Sidebar.Right
	}
	return config.ResolveSidebarSubagentView(m.UserConfig.Appearance.Sidebar, cfg)
}
