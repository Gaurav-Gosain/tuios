package main

import (
	"slices"
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// tuios workspaces: a session has up to nine, and shows one at a time. The
// sidebar shows the selected session's workspaces under its row (and any
// other session's that uses more than one), as numbered pills. A click
// shows a workspace, for every client of the session; a pane dropped on a
// pill moves there.

// holdMax is the longest a workspace switch keeps the last frame while the
// new workspace's panes attach.
const holdMax = 800 * time.Millisecond

// wsHold is a session as it showed before its workspace changed, kept on
// screen until the panes of the new one have their first frame, so a
// switch never shows empty cards.
type wsHold struct {
	s     proto.Session
	since time.Time
}

// snapshotSessions copies the sessions as they show now, for holdSwitches.
func (a *App) snapshotSessions() map[string]proto.Session {
	out := make(map[string]proto.Session, len(a.st.Sessions))
	for _, s := range a.st.Sessions {
		if s.ID != a.selected {
			continue
		}
		c := s
		c.Panes = slices.Clone(s.Panes)
		c.Layout = s.Layout.Clone()
		out[s.ID] = c
	}
	return out
}

// holdSwitches keeps the old frame of each session whose workspace changed
// with this state, and starts attaching the new workspace's panes.
func (a *App) holdSwitches(before map[string]proto.Session) {
	for _, s := range a.st.Sessions {
		old, ok := before[s.ID]
		if !ok || old.Workspace == 0 || old.Workspace == s.Workspace {
			continue
		}
		if a.holds == nil {
			a.holds = map[string]*wsHold{}
		}
		if _, held := a.holds[s.ID]; !held {
			a.holds[s.ID] = &wsHold{s: old, since: time.Now()}
		}
		a.leaveWorkspace(old)
		a.startSlide(old, s.Workspace)
		for _, id := range shownPanes(&s) {
			a.ensureTerm(id)
		}
	}
}

// heldPanes are the panes a hold keeps on screen, whose terminals stay.
func (a *App) heldPanes(live map[string]bool) {
	for _, h := range a.holds {
		for _, p := range h.s.Panes {
			live[p.ID] = true
		}
	}
	for _, sl := range a.slides {
		for _, p := range sl.from.Panes {
			live[p.ID] = true
		}
	}
	for k, l := range a.wsLeft {
		// Only the session shown keeps the workspaces it left, and their
		// terminals: the others go, as their panes do.
		if l.s.ID != a.selected {
			delete(a.wsLeft, k)
			continue
		}
		for _, p := range l.s.Panes {
			live[p.ID] = true
		}
	}
	// The scratch of the session shown keeps its terminal, so that the
	// dropdown shows at once.
	if s := a.session(a.selected); s != nil {
		for _, p := range s.Scratch {
			live[p.ID] = true
		}
	}
}

// shownSession is the session to draw: the held one while the panes of
// its new workspace attach, then the session itself.
func (a *App) shownSession(c *ui.Context, s *proto.Session) *proto.Session {
	h := a.holds[s.ID]
	if h == nil {
		return s
	}
	ready := true
	for _, id := range shownPanes(s) {
		a.ensureTerm(id)
		if e := a.terms[id]; e == nil || e.term == nil && e.err == "" {
			ready = false
		}
	}
	waited := time.Since(h.since)
	if ready || waited >= holdMax || h.s.Workspace == s.Workspace {
		delete(a.holds, s.ID)
		a.defer_(a.focusTerminal)
		return s
	}
	c.After(holdMax - waited)
	return &h.s
}

// showWorkspace shows one of a session's workspaces.
func (a *App) showWorkspace(s *proto.Session, n int) {
	if s == nil || n < 1 || n > 9 || n == s.Workspace {
		return
	}
	a.slideNow(s, n)
	a.call(s.ID, "workspace", proto.WorkspaceParams{Session: s.ID, Workspace: n}, nil)
}

// stepWorkspace shows the next or the previous workspace that holds panes.
func (a *App) stepWorkspace(s *proto.Session, d int) {
	if s == nil || len(s.Workspaces) < 2 {
		return
	}
	i := slices.IndexFunc(s.Workspaces, func(w proto.Workspace) bool { return w.N == s.Workspace })
	if i < 0 {
		return
	}
	i = (i + d + len(s.Workspaces)) % len(s.Workspaces)
	a.showWorkspace(s, s.Workspaces[i].N)
}

// newWorkspace shows the first workspace that holds no pane.
func (a *App) newWorkspace(s *proto.Session) {
	if n := freeWorkspace(s); n > 0 {
		a.showWorkspace(s, n)
	}
}

func freeWorkspace(s *proto.Session) int {
	for n := 1; n <= 9; n++ {
		if !slices.ContainsFunc(s.Workspaces, func(w proto.Workspace) bool { return w.N == n && w.Panes > 0 }) && n != s.Workspace {
			return n
		}
	}
	return 0
}

// movePaneTo moves a pane to another of its session's workspaces. The
// workspace shown stays.
func (a *App) movePaneTo(s *proto.Session, pane string, n int) {
	if s == nil || n < 1 || n > 9 || n == s.Workspace {
		return
	}
	if pane == "" {
		pane = a.focusOf(s)
	}
	if pane == "" {
		return
	}
	a.call(s.ID, "workspace", proto.WorkspaceParams{Session: s.ID, Pane: pane, Workspace: n}, nil)
}

// workspaceName is a workspace's label: its name, or its number.
func workspaceName(w proto.Workspace) string {
	if w.Name != "" {
		return w.Name
	}
	return strconv.Itoa(w.N)
}

// workspaceBar shows a session's workspaces as pills under its row: the
// selected session's always, another's when it uses more than one.
func (a *App) workspaceBar(c *ui.Context, pal *palette, s *proto.Session) {
	selected := s.ID == a.selected
	if len(s.Workspaces) < 2 && !selected || len(s.Workspaces) == 0 {
		return
	}
	// The pills' numbers start where the session's name does.
	inset := float32(rowInset - 8)
	if pal.dark {
		inset++
	}
	ui.Row(c.Key("ws-"+s.ID)).Height(hRow-4).Padding(0, 10, 2, inset).Gap(2).AlignItems(ui.Center).Margin(0, 0, 2, 0).Children(func() {
		for _, w := range s.Workspaces {
			a.workspacePill(c, pal, s, w)
		}
		if selected && freeWorkspace(s) > 0 {
			a.newWorkspaceButton(c, pal, s)
		}
	})
}

// newWorkspaceButton adds a workspace to s; a pane dropped on it moves
// there.
func (a *App) newWorkspaceButton(c *ui.Context, pal *palette, s *proto.Session) {
	sid := s.ID
	b := ui.ButtonBase(c.Key("ws-new")).Size(22, 22).Radius(rRow).Center().TextColor(pal.textFaint).Label(locales.WorkspaceNew())
	pointerTooltip(c, b, "ws-new-tip", locales.WorkspaceNewTip())
	// An empty workspace has no pill: a pane dropped on the
	// button goes to the first free workspace, as one dropped on
	// a pill goes to that pill's.
	if d, ok := ui.DragOver[paneDrag](b); ok && d.session == sid {
		b.Background(pal.rowSelected).Border(1, pal.accent).TextColor(pal.text)
	} else if b.Hovered() {
		b.Background(pal.rowHover).TextColor(pal.text)
	}
	if d, ok := ui.Drop[paneDrag](b); ok && d.session == sid {
		pane := d.pane
		a.defer_(func() {
			if s := a.session(sid); s != nil {
				a.movePaneTo(s, pane, freeWorkspace(s))
			}
		})
	}
	b.Children(func() { ui.Icon(c, plusIcon).Size(12, 12) })
	if b.Clicked() {
		a.defer_(func() { a.newWorkspace(a.session(sid)) })
	}
}

func (a *App) workspacePill(c *ui.Context, pal *palette, s *proto.Session, w proto.Workspace) {
	cur := w.N == s.Workspace
	sid, n := s.ID, w.N
	pill := ui.Row(c.Key("ws-"+strconv.Itoa(w.N))).Height(22).MinWidth(26).Padding(0, 8).Gap(5).Radius(rRow).AlignItems(ui.Center).Justify(ui.Center)
	over := false
	if d, ok := ui.DragOver[paneDrag](pill); ok && d.session == sid && !cur {
		over = true
	}
	if d, ok := ui.Drop[paneDrag](pill); ok && d.session == sid && !cur {
		pane := d.pane
		a.defer_(func() { a.movePaneTo(a.session(sid), pane, n) })
	}
	bg := ui.Transparent
	fg := pal.textMuted
	switch {
	case over:
		bg, fg = pal.rowSelected, pal.text
	case cur && !pal.dark:
		// The light theme's selected ground is white, the same as the
		// card the strip sits on: the current pill takes the accent's
		// chip there and in the sidebar, its text at 5:1 or more.
		bg, fg = pal.accent.Alpha(0.16), pal.accentText
	case cur:
		bg, fg = pal.rowSelected, pal.text
	case pill.Hovered():
		bg, fg = pal.rowHover, pal.text
	}
	pill.Background(pill.AnimateColor("bg", bg, ui.Animation{Duration: dHoverOut}))
	if over {
		pill.Border(1, pal.accent)
	}
	tip := locales.WorkspaceTip(w.Panes, workspaceName(w))
	pointerTooltip(c, pill, "ws-tip-"+strconv.Itoa(w.N), tip)
	if pill.Clicked() && !cur {
		a.defer_(func() {
			if s := a.session(sid); s != nil {
				if a.selected != sid {
					a.selectSession(sid)
				}
				a.showWorkspace(s, n)
			}
		})
	}
	pill.Children(func() {
		t := oneLine(c, workspaceName(w), fsSmall, wMedium).TextColor(fg).FontFeatures("tnum").MaxWidth(96)
		if cur {
			t.FontWeight(wSemibold)
		}
		// The dot keeps its room while there is none, so that a pill
		// does not change its width as its panes change their state.
		ui.Box(c).Size(6, 6).Radius(3).Shrink(0).Background(workspaceDot(pal, w))
	})
}

// workspaceDot is the color of the dot a pill shows for the most urgent
// status of its panes; none while they are idle.
func workspaceDot(pal *palette, w proto.Workspace) ui.Color {
	switch {
	case w.Status == proto.Waiting:
		return pal.attention
	case w.Status == proto.Failed:
		return pal.danger
	case w.Unread:
		return pal.done
	case w.Status == proto.Working:
		return pal.textFaint
	}
	return ui.Transparent
}

// The workspace strip stands in for the sidebar's workspace pills while
// the sidebar hides, as the TUI's top bar does: the session's workspaces,
// the button that adds one, and how many panes the shown one holds. It sits
// in the top left pane's header after the sidebar's buttons, in the title
// bar's line, so it takes no row of the panes' room.
const (
	stripLead     = 6  // from the sidebar's buttons to the first pill
	stripCountGap = 6  // from the last button to the pane count
	stripRule     = 14 // from the pane count to the rule before the title
)

// wsStripW is the room the strip takes, 0 when it does not show. At its
// full width it holds the pills, the new-workspace button and the pane
// count. In a header too narrow for that it keeps the pills alone
// (compact), so the workspaces still show; a header too narrow for the
// pills and a title keeps no strip.
func (a *App) wsStripW(c *ui.Context, s *proto.Session, card float32) (w float32, compact bool) {
	if s == nil || len(s.Workspaces) == 0 {
		return 0, false
	}
	pills := float32(stripLead)
	for i, ws := range s.Workspaces {
		weight := wMedium
		if ws.N == s.Workspace {
			weight = wSemibold
		}
		pills += max(26, 2*8+min(natural(c, workspaceName(ws), fsSmall, weight), 96)+5+6)
		if i > 0 {
			pills += 2
		}
	}
	full := pills
	if freeWorkspace(s) > 0 {
		full += 2 + 22
	}
	full += stripCountGap + natural(c, locales.SidebarPanes(stripPanes(s)), fsSmall, 0)
	full += stripRule + 1
	pills += stripRule + 1
	// At its full width the strip leaves the header its buttons, the
	// title's first titleKeep DIPs and its state mark. The pills alone
	// leave the mark and a title cut down to titleLeast with room to
	// spare (planHeader), as the TUI's bar keeps its tabs in a narrow
	// pane.
	switch {
	case card <= 0 || card >= a.chrome.headerPad+full+titleKeep+120:
		return full, false
	case card >= a.chrome.headerPad+pills+120:
		return pills, true
	}
	return 0, false
}

// stripPanes is the number of panes the shown workspace holds.
func stripPanes(s *proto.Session) int {
	for _, w := range s.Workspaces {
		if w.N == s.Workspace && w.Panes > 0 {
			return w.Panes
		}
	}
	return len(s.Panes)
}

// workspaceStrip draws the strip, in the row of the sidebar's buttons;
// compact draws the pills alone (wsStripW).
func (a *App) workspaceStrip(c *ui.Context, pal *palette, s *proto.Session, compact bool) {
	ui.Row(c.Key("ws-strip")).AlignItems(ui.Center).Gap(2).Margin(0, 0, 0, stripLead-2).Shrink(0).Children(func() {
		for _, w := range s.Workspaces {
			a.workspacePill(c, pal, s, w)
		}
		if !compact && freeWorkspace(s) > 0 {
			a.newWorkspaceButton(c, pal, s)
		}
		if !compact {
			ui.Text(c, locales.SidebarPanes(stripPanes(s))).FontSize(fsSmall).TextColor(pal.textFaint).FontFeatures("tnum").
				SingleLine().Shrink(0).Margin(0, 0, 0, stripCountGap-2)
		}
		// A rule ends the strip, so that the pane's title after it does
		// not read as one of the strip's labels.
		ui.Box(c).Size(1, 14).Shrink(0).Margin(0, 0, 0, stripRule-2).Background(pal.kbdBorder)
	})
}
