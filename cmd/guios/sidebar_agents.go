package main

import (
	"sort"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The sidebar's agent rows: under a session, one row per agent pane, with
// its state, its name and what it needs, those that need the person first,
// as the TUI's rail lists them. They read the list the badge reads
// (cockpit_needs.go). A click selects the agent in the agents view, a
// double click opens its pane.

// sbAgentsMax is how many agents a session lists before "+N more".
const sbAgentsMax = 5

// sbAgentH is an agent row's height: its name over its harness and what
// it is at, as the TUI rail and the agents list show an agent.
const sbAgentH = 38

// sbMoreH is the height of a "+N more" row.
const sbMoreH = 24

// sbAgent is one agent row: the agent, and what needs the person, if
// anything does.
type sbAgent struct {
	i    int
	need *ckNeed
}

// sessionAgents are the agents of a session of this machine, in the
// session's layout order.
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
	// In the order the tiles show them, as the shells' rows, whatever
	// their state: a state change never moves a row (DESIGN.md, principle
	// 3). Needs you at the top of the rail points to the agents that wait;
	// their rows here keep their place, with their mark (Mac round 1, feel
	// S2, visual S5).
	pos := paneRank(s)
	sort.SliceStable(out, func(x, y int) bool {
		return pos[ck.agents[out[x].i].Window] < pos[ck.agents[out[y].i].Window]
	})
	return out
}

// agentRow is one agent: under its session, or in Needs you. Line 1 is
// its mark, its name and, while it waits for the person, how long; line 2 is its
// harness and what it is at, or what it asks.
func (a *App) agentRow(c *ui.Context, pal *palette, ag client.Agent, need *ckNeed) {
	it := a.ckItemOf(ag)
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
	if ckSilent(ag) && it == nil {
		c.After(time.Second) // the age in line 2 counts up
	}
	pointerTooltip(c, row, "ag-tip-"+ag.Window, locales.SidebarAgentTip())
	st, unread := a.ckShownStatus(ag, it)
	if need != nil && st != proto.Failed {
		st, unread = proto.Waiting, true
	}
	row.Children(func() {
		ui.Column(c).Width(12).Shrink(0).AlignSelf(ui.Start).Padding(4, 0, 0, 0).Children(func() {
			ckMark(c, pal, st, unread, 12)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				if l := a.ckAttemptLetter(ag); l != "" {
					attemptChip(c, pal, l)
				}
				name := oneLine(c, sbAgentName(a.ckAgentName(ag), ag.Harness), fsSmall, wMedium).TextColor(pal.text)
				if need != nil {
					name.FontWeight(wSemibold)
				}
				ui.Spacer(c)
				// How long it has waited, on a row that needs the person
				// alone: ago asks for a frame each time a label changes,
				// and every age on screen changes at its own minute.
				if ag.StateAt > 0 && need != nil {
					ui.Text(c, ago(ag.StateAt/int64(time.Millisecond), c.Now())).FontSize(fsSmall).TextColor(pal.textFaint).FontFeatures("tnum").SingleLine().Shrink(0)
				}
			})
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				if ag.Harness != "" {
					// The program's mark; its name only when the line says
					// nothing else. "claude" on every row of a fleet of one
					// program said nothing (Mac round 2, visual S2). The
					// detail header names it.
					// The mark only among more than one harness (Mac round
					// 5, visual S3).
					if a.ck.agentHarnesses() > 1 {
						ui.Icon(c, harnessIcon(ag.Harness)).Size(12, 12).TextColor(pal.textMuted).Shrink(0).Tooltip(harnessName(ag.Harness))
					}
					if verb == "" && rest == "" {
						ui.Text(c, harnessName(ag.Harness)).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine().Shrink(0)
					}
				}
				if verb != "" {
					ui.Text(c, verb).FontSize(fsSmall).FontWeight(wMedium).TextColor(pal.attnText).SingleLine().Shrink(0)
				}
				if rest != "" {
					col := pal.textMuted
					if ckStopped(ag, it) {
						col = pal.textFaint
					}
					ui.Text(c, flat(rest)).FontSize(fsSmall).TextColor(col).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
				}
			})
		})
	})
}

// sbAgentName is an agent row's name: the agent's, or its harness's while
// the pane's title is only a shell's prompt ("me@host:~/src"), which tells
// nothing.
func sbAgentName(name, harness string) string {
	if at := strings.IndexByte(name, '@'); at > 0 && strings.Contains(name[at:], ":") && !strings.Contains(name[:at], " ") {
		if harness != "" {
			return harnessName(harness)
		}
	}
	if name == "" {
		return locales.CockpitUnnamed()
	}
	return name
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
	a.moveKeys("view")
}

// sessionNeeds is how many agents of a session need the person.
func (a *App) sessionNeeds(s *proto.Session) int {
	n := 0
	for _, r := range a.sessionAgents(s, a.sbNeeds) {
		if r.need != nil {
			n++
		}
	}
	return n
}
