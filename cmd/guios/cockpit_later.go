package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

func jsonUnmarshal(raw []byte, out any) error { return json.Unmarshal(raw, out) }

// cockpitSide is the side column, in a window wide enough for it: what the
// selected agent is, where its state came from, and what waits for it.
func (a *App) cockpitSide(c *ui.Context, pal *palette) {
	ag, ok := a.ckSelectedAgent()
	ui.Column(c.Key("side")).Width(ckSideW).Shrink(0).Gap(10).Padding(16, 16).Background(ckGround(pal)).Children(func() {
		if !ok {
			return
		}
		ui.Text(c, locales.CockpitDetails()).FontSize(fsSmall).FontWeight(wMedium).TextColor(pal.textMuted).SingleLine()
		row := func(k, v string) {
			if v == "" {
				return
			}
			ui.Row(c).Gap(8).Children(func() {
				oneLine(c, k, fsSmall, 0).TextColor(pal.textFaint).Width(88).Shrink(0)
				oneLine(c, v, fsSmall, 0).TextColor(pal.text)
			})
		}
		// What the header does not say: the header names the agent, its
		// session and folder in short, and the pill its state (Mac round
		// 1, visual S9).
		if m, ok := ag.Meta["model"].(string); ok {
			row(locales.CockpitDetailModel(), m)
		}
		if s := a.sessionOfPane(ag.Window); s != nil && ag.Host == "" {
			g := sessionGit(s)
			if p := s.PaneByID(ag.Window); p != nil && p.Git != nil {
				g = p.Git
			}
			if g != nil {
				row(locales.CockpitDetailBranch(), gitShort(g))
			}
		}
		row(locales.CockpitDetailFolder(), ag.Cwd)
		if ag.StateAt > 0 {
			row(locales.CockpitDetailSince(), time.Unix(0, ag.StateAt).Format("15:04"))
		}
		// Where the state came from matters only when it is not the
		// agent's own report, as the row's tooltip says.
		if ckSourceShown(ag.Source) {
			row(locales.CockpitDetailSource(), ckSource(ag.Source))
		}
		if ag.Unread > 0 {
			row(locales.CockpitDetailMail(), fmt.Sprint(ag.Unread))
		}
		if ag.Queued > 0 {
			row(locales.CockpitDetailQueued(), fmt.Sprint(ag.Queued))
		}
		if len(ag.SubagentList) > 0 {
			ui.Text(c, locales.CockpitSubagents()).FontSize(fsSmall).FontWeight(wMedium).TextColor(pal.textMuted).SingleLine().Margin(8, 0, 0, 0)
			for i, s := range ag.SubagentList {
				mark := "├"
				if i == len(ag.SubagentList)-1 {
					mark = "└"
				}
				ui.Row(c).Gap(6).Children(func() {
					ui.Text(c, mark).FontSize(fsSmall).TextColor(pal.textFaint)
					ui.Text(c, s.Type).FontSize(fsSmall).FontWeight(wMedium).TextColor(pal.text).SingleLine().Shrink(0)
					oneLine(c, s.Label, fsSmall, 0).TextColor(pal.textMuted)
				})
			}
		}
	})
}
