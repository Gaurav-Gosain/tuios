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

const agentStateWaiting session.AgentState = "waiting"

func displayState(state string, doneSeen bool, running int) string {
	if running > 0 && (state == string(session.AgentStateDone) || state == string(session.AgentStateIdle)) {
		return string(agentStateWaiting)
	}
	return sidebarGlyphState(state, doneSeen)
}

const subagentListMax = 16

const subagentTextMax = 80

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

func clampBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

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

func subagentName(s sessiontree.Subagent) string {
	if d := printableTitle(s.Description); d != "" {
		return d
	}
	if t := printableTitle(s.Type); t != "" {
		return t
	}
	return "subagent"
}

func subagentNote(s sessiontree.Subagent) string {
	switch s.State {
	case session.SubagentRunning:
		if now := printableTitle(toolLineLabel(s.Now)); now != "" {
			return now
		}
		return printableTitle(toolLineLabel(s.Last))
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

func subagentToolCount(n int) string {
	switch {
	case n == 1:
		return sidebarAgentSep() + "1 tool"
	case n > 1:
		return sidebarAgentSep() + strconv.Itoa(n) + " tools"
	}
	return ""
}

func subagentAge(s sessiontree.Subagent, now time.Time) string {
	if s.EndedAt > 0 {
		now = time.Unix(0, s.EndedAt)
	}
	return railAgentAge(string(session.AgentStateWorking), s.StartedAt, now)
}

func subagentLine(s sessiontree.Subagent) string {
	mark := agentStateIndicator(subagentMarkState(s))
	return mark + agentMarkGap(mark) + subagentName(s) + sidebarAgentSep() + subagentNote(s)
}

func runningSubagents(list []sessiontree.Subagent) int {
	n := 0
	for _, s := range list {
		if s.Running() {
			n++
		}
	}
	return n
}

func subagentsRunText(running, total int) string {
	if running <= 0 {
		return ""
	}
	return strconv.Itoa(running) + " of " + strconv.Itoa(total) + " run"
}

func subagentsWaitText(n int) string {
	if n <= 0 {
		return ""
	}
	return "waits for " + session.SubagentsText(n)
}

func (m *OS) paneSubagentList(sessionID, windowID string) []sessiontree.Subagent {
	w, sum := m.findPane(sessionID, windowID)
	switch {
	case w != nil:
		return w.AgentSubagentList
	case sum != nil:
		return subagentsFromWire(nil, sum.SubagentList)
	}
	return nil
}

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

type sidebarChildPlan struct {
	rows []sidebarAgentEntry
	cut  bool
}

type sidebarChildParent struct {
	kids    []sessiontree.Subagent
	running int
	head    bool
	open    bool
}

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

func sidebarCapRows(n int, v config.SidebarSubagentView) int {
	if v.MaxRows > 0 {
		return min(n, v.MaxRows)
	}
	return n
}

func sidebarSubagentKey(sessionID, windowID string) string {
	return sessionID + "\x00" + windowID
}

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

func sidebarSubagentHidden(v config.SidebarSubagentView, s sessiontree.Subagent, now time.Time) bool {
	return v.Hide && !s.Running() && s.EndedAt > 0 && !time.Unix(0, s.EndedAt).Add(v.HideAfter).After(now)
}

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
				kids = nil
			} else {
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

func sidebarChildIndent() int { return sidebarNameCol() + 1 }

func (m *OS) sidebarChildLead(e sidebarAgentEntry, rowBg color.Color, pal overlay.Palette, second bool) string {
	pad := sidebarStyle(rowBg, nil)
	if !m.Settings.SidebarSubagents.Tree {
		return pad.Render(strings.Repeat(" ", sidebarChildIndent()))
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
	if w > sidebarChildIndent() {
		lead = overlay.Truncate(lead, sidebarChildIndent())
		w = lipgloss.Width(lead)
	}
	return sidebarStyle(rowBg, pal.FgMute).Render(lead) + pad.Render(strings.Repeat(" ", sidebarChildIndent()-w))
}

func sidebarTreePipe(branch string) string {
	switch {
	case strings.HasPrefix(branch, "├"):
		return "│"
	case strings.HasPrefix(branch, "|"):
		return "|"
	}
	return " "
}

func sidebarSubagentNames(list []sessiontree.Subagent) string {
	names := make([]string, 0, len(list))
	for _, s := range list {
		names = append(names, subagentName(s))
	}
	return strings.Join(names, ", ")
}

func (m *OS) sidebarChildRow(e sidebarAgentEntry, cw int, pal overlay.Palette, st sidebarRowState, second bool) string {
	rowBg := sidebarRowBg(st, pal)
	pad := sidebarStyle(rowBg, nil)
	fields := m.Settings.SidebarSubagents.Fields
	lead := m.sidebarChildLead(e, rowBg, pal, second)
	if e.Head {
		if second {
			if e.Open || len(e.HeadKids) == 0 {
				return sidebarFit(lead, cw, rowBg)
			}
			indent := sidebarChildIndent() + 2
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
		avail := max(cw-sidebarChildIndent()-2, 1)
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
			sidebarStyle(rowBg, fg).Render(overlay.Truncate(text, max(cw-sidebarChildIndent()-2, 1))), cw, rowBg)
	}
	s := *e.Child
	ended := !s.Running()
	if second {
		if !fields.Note {
			return sidebarFit(lead, cw, rowBg)
		}
		indent := sidebarChildIndent() + 2
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
	avail := max(cw-sidebarChildIndent()-2-rightW, 1)
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
	body := lead + sidebarStyle(rowBg, markFg).Render(mark) + pad.Render(agentMarkGap(mark)) + text
	if right == "" {
		return sidebarFit(body, cw, rowBg)
	}
	gap := max(cw-lipgloss.Width(body)-rightW+1, 1)
	return sidebarFit(body+pad.Render(strings.Repeat(" ", gap))+right, cw, rowBg)
}

func sidebarAgentsHaveChildren(agents []sidebarAgentEntry) bool {
	return slices.ContainsFunc(agents, func(e sidebarAgentEntry) bool { return len(e.Children) > 0 })
}

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

type subagentHideMsg struct{}

func (m *OS) noteSubagentHide(at time.Time) {
	if m.subagentHideDue.IsZero() || at.Before(m.subagentHideDue) {
		m.subagentHideDue = at
	}
}

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
	if due.IsZero() || (m.subagentHideArmed.After(now) && !m.subagentHideArmed.After(due)) {
		return nil
	}
	m.subagentHideArmed = due
	return m.tickUntilStop(due.Sub(now), func(time.Time) tea.Msg { return subagentHideMsg{} })
}

func (m *OS) subagentHideOn() bool {
	if m.Settings.SidebarSubagents.Hide {
		return true
	}
	return m.secondarySubagentView().Hide
}

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
