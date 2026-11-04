package main

import (
	"encoding/json"
	"fmt"

	"github.com/egoist/mygo/ui"

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
		ui.Text(c, locales.CockpitDetails()).FontSize(12).FontWeight(650).TextColor(pal.textMuted)
		row := func(k, v string) {
			if v == "" {
				return
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, k).FontSize(12).TextColor(pal.textFaint).Width(84).Shrink(0)
				ui.Text(c, v).FontSize(12).TextColor(pal.text).MinWidth(0)
			})
		}
		row(locales.CockpitDetailAgent(), harnessName(ag.Harness))
		if m, ok := ag.Meta["model"].(string); ok {
			row(locales.CockpitDetailModel(), m)
		}
		row(locales.CockpitDetailSession(), ag.Session)
		row(locales.CockpitDetailState(), ag.State)
		row(locales.CockpitDetailSource(), ag.Source+" · "+ag.Confidence)
		if ag.Unread > 0 {
			row(locales.CockpitDetailMail(), fmt.Sprint(ag.Unread))
		}
		if ag.Queued > 0 {
			row(locales.CockpitDetailQueued(), fmt.Sprint(ag.Queued))
		}
		if len(ag.SubagentList) > 0 {
			ui.Text(c, locales.CockpitSubagents()).FontSize(12).FontWeight(650).TextColor(pal.textMuted).Margin(8, 0, 0, 0)
			for i, s := range ag.SubagentList {
				mark := "├"
				if i == len(ag.SubagentList)-1 {
					mark = "└"
				}
				ui.Row(c).Gap(6).Children(func() {
					ui.Text(c, mark).FontSize(12).TextColor(pal.textFaint)
					ui.Text(c, s.Type).FontSize(12).FontWeight(600).TextColor(pal.text)
					ui.Text(c, s.Label).FontSize(12).TextColor(pal.textMuted).SingleLine().Ellipsis("…").MinWidth(0)
				})
			}
		}
	})
}
