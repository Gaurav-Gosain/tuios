package main

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The parts of the sidebar that bring it to the TUI's rail (QA-018): a
// search field, the Needs you group first, the panes under each session,
// worktrees under their repository's session, Files and New session. They
// draw from the state and the agent lists the daemon's events keep, and
// ask for no frame of their own.

// sbPanesMax is how many panes a session lists before "+N more".
const sbPanesMax = 4

// sbPaneH is a pane row's height.
const sbPaneH = 26

// sbNeedsMax is how many agents Needs you lists before "+N more".
const sbNeedsMax = 5

// railSearch is the search field at the sidebar's top: it opens the
// palette, which finds sessions, panes, agents and commands.
func (a *App) railSearch(c *ui.Context, pal *palette) {
	row := ui.Row(c.Key("rail-search")).Height(hControl).Margin(0, 0, 6, 0).Padding(0, 6, 0, 10).Gap(8).AlignItems(ui.Center).
		Radius(rControl).Background(pal.chip).Cursor(ui.CursorPointer).Label(locales.CommonSearch())
	if row.Hovered() {
		row.Background(pal.rowHover).Border(1, pal.kbdBorder)
	}
	if row.Clicked() {
		c.Invalidate()
		a.openPalette()
	}
	row.Children(func() {
		ui.Icon(c, searchIcon).Size(14, 14).TextColor(pal.textFaint).Shrink(0)
		oneLine(c, locales.CommonSearch(), fsBody, 0).TextColor(pal.textFaint)
		ui.Spacer(c)
		if k := a.keys("palette.open"); k != "" {
			kbd(c, pal, k)
		}
	})
}

// railHeader is a section's header: its name, and a count after it.
func railHeader(c *ui.Context, pal *palette, key, name string, count int, fg ui.Color) {
	inset := float32(rowInset)
	if pal.dark {
		inset++
	}
	ui.Row(c.Key(key)).Height(hControl).Margin(4, 0, 2, 0).Padding(0, 6, 0, inset).Gap(6).AlignItems(ui.Center).Children(func() {
		oneLine(c, name, fsSmall, wMedium).TextColor(fg)
		if count > 0 {
			ui.Text(c, strconv.Itoa(count)).FontSize(fsSmall).TextColor(pal.textFaint).FontFeatures("tnum").Shrink(0)
		}
	})
}

// needsSection is the Needs you group, first in the sidebar as in the TUI
// rail: each agent of this machine that asks something, failed or has
// mail, the oldest wait first, as the badge counts them.
func (a *App) needsSection(c *ui.Context, pal *palette) {
	if !a.ck.loaded || len(a.sbNeeds) == 0 {
		return
	}
	shown := 0
	for k := range a.sbNeeds {
		if a.sbNeeds[k].agent >= 0 {
			shown++
		}
	}
	if shown == 0 {
		return
	}
	ui.Column(c.Key("needs")).Margin(0, 0, 12, 0).Children(func() {
		railHeader(c, pal, "needs-hdr", locales.StatusNeedsYou(), shown, pal.attnText)
		n := 0
		for k := range a.sbNeeds {
			nd := &a.sbNeeds[k]
			if nd.agent < 0 || nd.agent >= len(a.ck.agents) {
				continue
			}
			if n == sbNeedsMax {
				a.moreRow(c, pal, "needs-more", locales.SidebarMoreAgents(shown-sbNeedsMax), func() { a.showInCockpit("") })
				break
			}
			n++
			a.agentRow(c, pal, a.ck.agents[nd.agent], nd)
		}
	})
}

// moreRow is a "+N more" row under a list.
func (a *App) moreRow(c *ui.Context, pal *palette, key, label string, fn func()) {
	more := ui.Row(c.Key(key)).Height(sbMoreH).Padding(0, 10, 0, rowInset+24).AlignItems(ui.Center).Radius(rRow).Cursor(ui.CursorPointer)
	if more.Hovered() {
		more.Background(pal.rowHover)
	}
	if more.Clicked() {
		c.Invalidate()
		fn()
	}
	more.Children(func() {
		ui.Text(c, label).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine()
	})
}

// paneRows lists a session's panes that run no agent (the agent rows list
// those), when it has more than one: what runs in each and where, as the
// TUI rail's terminals. A click goes to the pane.
func (a *App) paneRows(c *ui.Context, pal *palette, s *proto.Session) {
	if len(s.Panes)+len(s.Others) < 2 {
		return
	}
	agents := map[string]bool{}
	for _, r := range a.sessionAgents(s, a.sbNeeds) {
		agents[a.ck.agents[r.i].Window] = true
	}
	var panes []*proto.Pane
	var nums []int // each pane's number in the session, from 1
	for i := range s.Panes {
		if p := &s.Panes[i]; !agents[p.ID] && p.Agent == "" {
			panes = append(panes, p)
			nums = append(nums, i+1)
		}
	}
	if len(panes) == 0 {
		return
	}
	// Panes that would read the same, as three shells, get a detail:
	// their folder within the session's, else their number, as the
	// palette's Ctrl+B 1, 2, 3 (council round 5, visual M2).
	h := a.hostOf(s.ID)
	details := make([]string, len(panes))
	seen := map[string]int{}
	for k, p := range panes {
		details[k] = paneFolder(s, p, h)
		seen[p.PaneName()+"\x00"+details[k]]++
	}
	for k, p := range panes {
		if seen[p.PaneName()+"\x00"+details[k]] > 1 {
			if details[k] != "" {
				details[k] += " · "
			}
			// The number alone said nothing (QM-005): it is the pane's
			// number, as Ctrl+B and the palette count them.
			details[k] += locales.SidebarPaneNumber(strconv.Itoa(nums[k]))
		}
	}
	focus := s.Focus
	if f := a.focus[s.ID]; f != "" {
		focus = f
	}
	ui.Column(c.Key("panes-"+s.ID)).Margin(0, 0, 2, 0).Children(func() {
		for k, p := range panes {
			if k == sbPanesMax {
				sid := s.ID
				a.moreRow(c, pal, "panes-more", locales.SidebarMorePanes(len(panes)-sbPanesMax), func() { a.selectSession(sid) })
				return
			}
			a.paneRow(c, pal, s, p, details[k], p.ID == focus && s.ID == a.selected, h)
		}
	})
}

// paneFolder is a pane's folder within its session's, as "docs" or
// "src/api", "" for the session's own; a folder outside it goes by its
// last name, and the home folder of h is "~", as the header and the TUI
// name it.
func paneFolder(s *proto.Session, p *proto.Pane, h *host) string {
	d, root := p.WorkDir(), s.WorkDir()
	if d == "" || d == root {
		return ""
	}
	if h != nil && h.shortDir(d) == "~" {
		return "~"
	}
	if r, err := filepath.Rel(root, d); err == nil && root != "" && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return proto.DirName(d)
}

// paneRow is one pane under its session: a mark of its state, what runs
// in it, and detail, what tells it from the others (paneRows).
func (a *App) paneRow(c *ui.Context, pal *palette, s *proto.Session, p *proto.Pane, detail string, focused bool, h *host) {
	row := ui.Row(c.Key("pn-"+p.ID)).Height(sbPaneH).Padding(0, 10, 0, rowInset+4).Gap(8).AlignItems(ui.Center).Radius(rRow).
		Cursor(ui.CursorPointer).Label(p.PaneName())
	if row.Hovered() {
		row.Background(pal.rowHover)
	}
	if row.Clicked() {
		c.Invalidate()
		sid, pid := s.ID, p.ID
		if a.selected != sid {
			a.selectSession(sid)
		}
		if t := a.session(sid); t != nil {
			a.setFocus(t, pid)
			a.focusTerminal()
		}
	}
	rowTooltip(c, row, "pn-tip-"+p.ID, h.shortDir(p.WorkDir()))
	row.Children(func() {
		ui.Row(c).Width(12).Shrink(0).Justify(ui.Center).Children(func() {
			if p.Status == proto.Idle && !p.Unread {
				ui.Icon(c, terminalIcon).Size(12, 12).TextColor(pal.textFaint)
			} else {
				statusIconP(c, pal, p.Status, p.Unread, p.Progress, 12)
			}
		})
		name := oneLine(c, p.PaneName(), fsSmall, wMedium).TextColor(pal.textMuted)
		if focused {
			name.TextColor(pal.text)
		}
		ui.Spacer(c)
		if detail != "" {
			ui.Text(c, detail).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Ellipsis("…").MinWidth(0).Shrink(2).MaxWidth(110)
		}
	})
}

// newSessionRow is New session at the end of the session list, as the
// TUI rail has it.
func (a *App) newSessionRow(c *ui.Context, pal *palette) {
	row := ui.Row(c.Key("rail-new-session")).Height(hControl).Margin(6, 0, 0, 0).Padding(0, 10, 0, rowInset).Gap(8).AlignItems(ui.Center).
		Radius(rRow).Cursor(ui.CursorPointer).Label(locales.ActionNewSession())
	hovered := row.Hovered()
	if hovered {
		row.Background(pal.rowHover)
	}
	pointerTooltip(c, row, "rail-new-tip", a.tip(locales.ActionNewSession(), "session.new"))
	if row.Clicked() {
		c.Invalidate()
		a.newSession("")
	}
	row.Children(func() {
		fg := pal.textMuted
		if hovered {
			fg = pal.text
		}
		ui.Icon(c, plusIcon).Size(14, 14).TextColor(fg).Shrink(0)
		oneLine(c, locales.ActionNewSession(), fsBody, 0).TextColor(fg)
	})
}

// filesRow is Files in the sidebar: the shown session's folder, which a
// click opens in the Files tab of the right sidebar.
func (a *App) filesRow(c *ui.Context, pal *palette) {
	s := a.session(a.selected)
	if s == nil {
		return
	}
	on := a.rightShown() && a.prefs.Side == sideFiles
	row := ui.Row(c.Key("rail-files")).Height(hControl).Margin(10, 0, 0, 0).Padding(0, 10, 0, rowInset).Gap(8).AlignItems(ui.Center).
		Radius(rRow).Cursor(ui.CursorPointer).Label(locales.SideFiles())
	switch {
	case on:
		row.Background(pal.rowSelected)
	case row.Hovered():
		row.Background(pal.rowHover)
	}
	pointerTooltip(c, row, "rail-files-tip", a.tip(locales.SidebarFilesTip(), "files.toggle"))
	if row.Clicked() {
		c.Invalidate()
		a.toggleSide(sideFiles)
	}
	row.Children(func() {
		ui.Icon(c, folderIcon).Size(14, 14).TextColor(pal.textMuted).Shrink(0)
		oneLine(c, locales.SideFiles(), fsBody, wMedium).TextColor(pal.text).Shrink(0)
		ui.Text(c, a.hostOf(s.ID).shortDir(s.WorkDir())).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
	})
}

// worktreeOrder puts each session in a linked worktree under the session
// of its repository's main checkout, as the TUI rail nests them, and says
// which are nested. A linked one with no such session keeps its place.
//
// letter names a fan attempt's letter: a fan's attempts sit in its order,
// A before B, as the agents list has them (council round 5, visual M2).
func worktreeOrder(in []*proto.Session, letter func(*proto.Session) string) (out []*proto.Session, nested map[string]bool) {
	mains := map[string]bool{}
	for _, s := range in {
		if s.Checkout != nil && !s.Checkout.Linked {
			mains[s.Checkout.RepoRoot] = true
		}
	}
	under := map[string][]*proto.Session{}
	for _, s := range in {
		if k := s.Checkout; k != nil && k.Linked && mains[k.RepoRoot] {
			under[k.RepoRoot] = append(under[k.RepoRoot], s)
		}
	}
	if len(under) == 0 {
		return in, nil
	}
	for _, ss := range under {
		slices.SortStableFunc(ss, func(x, y *proto.Session) int {
			lx, ly := letter(x), letter(y)
			if lx == "" || ly == "" {
				return 0
			}
			return strings.Compare(lx, ly)
		})
	}
	nested = map[string]bool{}
	out = make([]*proto.Session, 0, len(in))
	// Each repository's linked sessions follow its first main session, as
	// in the TUI rail.
	first := map[string]int{}
	for i, s := range in {
		if k := s.Checkout; k != nil && !k.Linked {
			if _, ok := first[k.RepoRoot]; !ok {
				first[k.RepoRoot] = i
			}
		}
	}
	for i, s := range in {
		if k := s.Checkout; k != nil && k.Linked && mains[k.RepoRoot] {
			continue
		}
		out = append(out, s)
		if k := s.Checkout; k != nil && !k.Linked && first[k.RepoRoot] == i {
			for _, w := range under[k.RepoRoot] {
				out = append(out, w)
				nested[w.ID] = true
			}
		}
	}
	return out, nested
}
