package main

import (
	"sort"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/session"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The sidebar's agent rows: under a session, one row per agent pane, with
// its state, its name and what it needs, those that need the person first,
// as the TUI's rail lists them. They read the list the badge reads
// (cockpit_needs.go). A click selects the agent in the agents view, a
// double click opens its pane.

// sbAgentsMax is how many agents a session lists before "+N more".
const sbAgentsMax = 5

// sbAgentH is an agent row's height.
const sbAgentH = 24

// sbAgent is one agent row: the agent, and what needs the person, if
// anything does.
type sbAgent struct {
	i    int
	need *ckNeed
}

// sessionAgents are the agents of a session of this machine, those that
// need the person first, the oldest wait first.
func (a *App) sessionAgents(s *proto.Session, needs []ckNeed) []sbAgent {
	ck := &a.ck
	if !ck.loaded || a.hostOf(s.ID).remote() {
		return nil
	}
	in := map[string]bool{}
	for _, p := range s.Panes {
		in[p.ID] = true
	}
	for _, p := range s.Others {
		in[p.ID] = true
	}
	byAgent := map[int]*ckNeed{}
	for k := range needs {
		if needs[k].agent >= 0 {
			byAgent[needs[k].agent] = &needs[k]
		}
	}
	var out []sbAgent
	for i, ag := range ck.agents {
		if ag.Host == "" && in[ag.Window] {
			out = append(out, sbAgent{i: i, need: byAgent[i]})
		}
	}
	sort.SliceStable(out, func(x, y int) bool {
		nx, ny := out[x].need, out[y].need
		switch {
		case (nx != nil) != (ny != nil):
			return nx != nil
		case nx != nil:
			if nx.tier != ny.tier {
				return nx.tier < ny.tier
			}
			return nx.since < ny.since
		}
		return ck.agents[out[x].i].Name < ck.agents[out[y].i].Name
	})
	return out
}

// agentRows draws a session's agent rows.
func (a *App) agentRows(c *ui.Context, pal *palette, s *proto.Session, needs []ckNeed) {
	rows := a.sessionAgents(s, needs)
	if len(rows) == 0 {
		return
	}
	ui.Column(c.Key("agents-"+s.ID)).Margin(0, 0, 4, 0).Children(func() {
		for k, r := range rows {
			if k == sbAgentsMax {
				more := ui.Row(c.Key("more")).Height(sbAgentH).Padding(0, 10, 0, rowInset+22).AlignItems(ui.Center).Radius(rRow).Cursor(ui.CursorPointer)
				if more.Hovered() {
					more.Background(pal.rowHover)
				}
				if more.Clicked() {
					c.Invalidate()
					a.showInCockpit("")
				}
				more.Children(func() {
					ui.Text(c, locales.SidebarMoreAgents(len(rows)-sbAgentsMax)).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine()
				})
				return
			}
			a.agentRow(c, pal, a.ck.agents[r.i], r.need)
		}
	})
}

// agentRow is one agent under its session.
func (a *App) agentRow(c *ui.Context, pal *palette, ag client.Agent, need *ckNeed) {
	var it *session.AttentionItem
	if need != nil {
		it = need.item
	}
	row := ui.Row(c.Key("ag-"+ag.Window)).Height(sbAgentH).Padding(0, 10, 0, rowInset+4).Gap(8).AlignItems(ui.Center).Radius(rRow).
		Cursor(ui.CursorPointer).Label(ag.Name)
	if row.Hovered() {
		row.Background(pal.rowHover)
	}
	if row.DoubleClicked() {
		a.goToAgent(ag)
	} else if row.Clicked() {
		c.Invalidate()
		a.showInCockpit(ag.Key())
	}
	verb, rest := ckLine2(ag, it)
	if need == nil {
		verb, rest = "", ""
	}
	pointerTooltip(c, row, "ag-tip-"+ag.Window, locales.SidebarAgentTip())
	st, unread := ckStatus(ag, it)
	if need != nil && st != proto.Failed {
		st, unread = proto.Waiting, true
	}
	row.Children(func() {
		ckMark(c, pal, st, unread, 12)
		name := oneLine(c, ag.Name, fsSmall, wMedium).TextColor(pal.text).Shrink(0).MaxWidth(130)
		if need != nil {
			name.FontWeight(wSemibold)
		}
		if t, r, ok := cutTool(rest); ok {
			rest = t + " " + r
		}
		line := rest
		if verb != "" {
			line = verb + " " + rest
		}
		if line != "" {
			ui.Text(c, flat(line)).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		}
	})
}

// cutTool splits a second line that names a tool (ckLine2's "\x00").
func cutTool(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

// showInCockpit opens the agents view on an agent, by its key, or on the
// list for "".
func (a *App) showInCockpit(key string) {
	ck := &a.ck
	if !ck.open {
		a.toggleCockpit()
	}
	ck.view = viewAgents
	if key != "" {
		ck.jumpTo = &ckJump{agent: key}
	}
	ck.fleetFocus.Focus()
}
