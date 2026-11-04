package main

import (
	"cmp"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/mygo/ui"
	"github.com/Gaurav-Gosain/mygo/ui/chart"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The resource views draw the sampler's readings (usage.go) in the app's
// type: a figure beside a sparkline of its last minute, kero's quiet
// grounds, the accent for CPU and the attention orange for memory.

// sparkline draws s as a line over a soft area, w by h, scaled to top (the
// largest reading when top is 0).
func sparkline(c *ui.Context, key string, s series, top float32, col ui.Color, w, h float32) ui.Element {
	box := ui.Box(c.Key(key)).Height(h).Shrink(0).Unselectable()
	if w > 0 {
		box.Width(w)
	}
	if len(s) == 0 {
		return box
	}
	if top <= 0 {
		// A figure with no top of its own, as memory: its peak sits at
		// three quarters of the height, so a steady figure reads as a line.
		top = slices.Max(s) * 4 / 3
	}
	top = max(top, 1e-6)
	n := len(s)
	xs := make([]float32, n)
	ys := make([]float32, n)
	for i, v := range s {
		// The readings so far across the whole width, the newest at the
		// right edge.
		xs[i] = float32(i) / float32(max(n-1, 1))
		ys[i] = min(v/top, 1)*0.9 + 0.05
	}
	box.Radius(rChip).Background(col.Alpha(0.06))
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		if n == 1 {
			p.Line(chart.X(r, xs[0]), chart.Y(r, ys[0]), r.X+r.W, chart.Y(r, ys[0]), 1.5, col)
			return
		}
		p.FillPath(chart.Area(r, xs, nil, ys, chart.CurveMonotone), col.Alpha(0.14))
		p.StrokePath(chart.Line(r, xs, ys, chart.CurveMonotone), 1.5, col)
	})
	return box
}

// trendline is a small line of a figure's readings over a hairline
// baseline, with no fill and no ground, so that it does not read as a
// progress bar (council round 1, visual 25).
func trendline(c *ui.Context, pal *palette, key string, s series, top float32, w, h float32) ui.Element {
	box := ui.Box(c.Key(key)).Size(w, h).Shrink(0).Unselectable()
	if top <= 0 && len(s) > 0 {
		top = slices.Max(s) * 4 / 3
	}
	top = max(top, 1e-6)
	n := len(s)
	xs := make([]float32, n)
	ys := make([]float32, n)
	for i, v := range s {
		xs[i] = float32(i) / float32(max(n-1, 1))
		ys[i] = min(v/top, 1)*0.85 + 0.08
	}
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Line(r.X, r.Y+r.H-0.5, r.X+r.W, r.Y+r.H-0.5, 1, pal.textFaint.Alpha(0.5))
		switch {
		case n == 1:
			p.Line(r.X, chart.Y(r, ys[0]), r.X+r.W, chart.Y(r, ys[0]), 1.25, pal.textMuted)
		case n > 1:
			p.StrokePath(chart.Line(r, xs, ys, chart.CurveMonotone), 1.25, pal.textMuted)
		}
	})
	return box
}

// meter draws a fraction as a short bar.
func meter(c *ui.Context, pal *palette, frac float64, col ui.Color, w float32) {
	frac = min(max(frac, 0), 1)
	ui.Box(c).Size(w, 5).Shrink(0).Radius(rChip).Background(pal.divider).Children(func() {
		ui.Box(c).Width(w * float32(frac)).Height(5).Radius(rChip).Background(col)
	})
}

// usageFigure is a label, a sparkline and its figure, on one line.
func usageFigure(c *ui.Context, pal *palette, key, label string, s series, top float32, figure string) {
	ui.Row(c.Key(key)).Gap(7).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Text(c, label).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine()
		trendline(c, pal, "spark", s, top, 24, 14)
		ui.Text(c, figure).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.text).FontFeatures("tnum").SingleLine()
	})
}

// agentUsage is the selected agent's line in the agents view: its pane's
// CPU and memory, and the context and cost its harness reports.
func (a *App) agentUsage(c *ui.Context, pal *palette, ag client.Agent) {
	var pu *paneUsage
	if u := a.usage; u != nil && ag.Host == "" {
		pu = u.panes[usageKey(ag.Session, ag.Window)]
	}
	ctx, hasCtx := metaPercent(ag.Meta["context"])
	cost, _ := ag.Meta["cost"].(string)
	if pu == nil && !hasCtx && cost == "" {
		return
	}
	ui.Row(c.Key("usage")).GapX(18).GapY(6).AlignItems(ui.Center).Margin(6, 0, 0, 0).Wrap().Children(func() {
		if pu != nil {
			usageFigure(c, pal, "cpu", locales.UsageCPU(), pu.cpu, 100, cpuText(pu.cpuNow))
			usageFigure(c, pal, "mem", locales.UsageMemory(), pu.mem, 0, bytesText(pu.rss))
		}
		if hasCtx {
			col := pal.textMuted
			if ctx >= 80 {
				col = pal.attention
			}
			ui.Row(c.Key("ctx")).Gap(7).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.UsageContext()).FontSize(fsSmall).TextColor(pal.textFaint)
				meter(c, pal, ctx/100, col, 44)
				ui.Text(c, strconv.Itoa(int(ctx))+"%").FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.text).FontFeatures("tnum")
			})
		}
		if cost != "" {
			ui.Row(c.Key("cost")).Gap(7).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.UsageCost()).FontSize(fsSmall).TextColor(pal.textFaint)
				ui.Text(c, cost).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.text).FontFeatures("tnum")
			})
		}
	})
}

// metaPercent reads a meta value such as "42%".
func metaPercent(v any) (float64, bool) {
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "%"), 64)
	return f, err == nil
}

// toggleUsage shows or hides the sidebar's usage panel.
func (a *App) toggleUsage() {
	a.usageOpen = !a.usageOpen
	a.syncUsage()
}

// usagePanel is the sidebar's usage panel: this machine's load and memory,
// the linked hosts' load, and the shown session's panes by CPU.
func (a *App) usagePanel(c *ui.Context, pal *palette) {
	if !a.usageOpen {
		return
	}
	u := a.usage
	ui.Column(c.Key("usage-panel")).Shrink(0).Margin(0, 10, 6, 10).Padding(10, 12, 12, 12).Gap(8).Radius(rCard).
		Background(ckGround(pal)).Border(1, pal.divider).Label(locales.UsageTitle()).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.UsageTitle()).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.textMuted)
			ui.Spacer(c)
			cl := ui.ButtonBase(c.Key("close")).Size(20, 20).Radius(rChip).Center().Label(locales.UsageHide()).Tooltip(locales.UsageHide())
			if cl.Hovered() {
				cl.Background(pal.rowHover)
			}
			cl.Children(func() { ui.Icon(c, closeIcon).Size(12, 12).TextColor(pal.textMuted) })
			if cl.Clicked() {
				c.Invalidate()
				a.toggleUsage()
			}
		})
		switch {
		case u == nil:
			ui.Text(c, locales.UsageReading()).FontSize(fsSmall).TextColor(pal.textFaint)
			return
		case !u.supported:
			ui.Text(c, locales.UsageNotSupported()).FontSize(fsSmall).TextColor(pal.textFaint).LineHeight(1.4)
		}
		// This machine.
		ui.Text(c, locales.UsageMachine()).FontSize(fsSmall).TextColor(pal.textFaint)
		if u.loadOK && u.cores > 0 {
			pct := 100 * u.load1 / float64(u.cores)
			usageRow(c, pal, "load", locales.UsageLoad(), u.load, 100, pal.accent, strconv.Itoa(int(pct+0.5))+"%", locales.UsageCores(u.cores))
		}
		usageRow(c, pal, "mem", locales.UsageMemory(), u.mem, 100, pal.attention, strconv.Itoa(int(u.memPct+0.5))+"%", "")
		// The linked hosts.
		if len(u.hosts) > 0 {
			ui.Text(c, locales.UsageHosts()).FontSize(fsSmall).TextColor(pal.textFaint).Margin(4, 0, 0, 0)
			for _, h := range u.hosts {
				ui.Row(c.Key("host-" + h.name)).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, h.name).FontSize(fsBody).TextColor(pal.text).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
					if !h.connected || !h.ok {
						ui.Text(c, locales.UsageOffline()).FontSize(fsSmall).TextColor(pal.textFaint)
						return
					}
					pct := 100 * h.load1 / float64(h.cores)
					meter(c, pal, pct/100, pal.accent, 48)
					ui.Text(c, strconv.Itoa(int(pct+0.5))+"%").FontSize(fsSmall).TextColor(pal.text).FontFeatures("tnum").Width(34).TextAlign(ui.End)
				})
			}
		}
		// The shown session's panes, the busiest first.
		s := a.workSession()
		if s == nil || !u.supported {
			return
		}
		type row struct {
			p  proto.Pane
			pu *paneUsage
		}
		var rows []row
		for _, p := range s.Panes {
			if pu := u.panes[usageKey(s.Name, p.ID)]; pu != nil {
				rows = append(rows, row{p, pu})
			}
		}
		slices.SortStableFunc(rows, func(x, y row) int { return cmp.Compare(y.pu.cpuNow, x.pu.cpuNow) })
		ui.Text(c, locales.UsagePanes()).FontSize(fsSmall).TextColor(pal.textFaint).Margin(4, 0, 0, 0)
		if len(rows) == 0 {
			ui.Text(c, locales.UsageNoPanes()).FontSize(fsSmall).TextColor(pal.textFaint)
		}
		for i, r := range rows {
			if i >= 6 {
				break
			}
			name := r.p.Process
			if name == "" {
				name = cmp.Or(r.p.Shell, "shell")
				if r.p.Dir != "" {
					name += " · " + filepath.Base(r.p.Dir)
				}
			}
			ui.Column(c.Key("pane-" + r.p.ID)).Gap(3).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, name).FontSize(fsBody).TextColor(pal.text).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
					ui.Text(c, bytesText(r.pu.rss)).FontSize(fsSmall).TextColor(pal.textMuted).FontFeatures("tnum").SingleLine()
				})
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					sparkline(c, "cpu", r.pu.cpu, 100, pal.accent, 0, 16).Grow(1).MinWidth(40)
					ui.Text(c, cpuText(r.pu.cpuNow)).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.text).FontFeatures("tnum").Width(44).TextAlign(ui.End)
				})
			})
		}
	})
}

// usageRow is one of the machine's figures: a label, a sparkline that takes
// the room, the figure and a note.
func usageRow(c *ui.Context, pal *palette, key, label string, s series, top float32, col ui.Color, figure, note string) {
	ui.Row(c.Key(key)).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(fsBody).TextColor(pal.text).Width(58).SingleLine()
		sparkline(c, "spark", s, top, col, 0, 18).Grow(1).MinWidth(40)
		ui.Column(c).AlignItems(ui.End).Children(func() {
			ui.Text(c, figure).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.text).FontFeatures("tnum")
			if note != "" {
				ui.Text(c, note).FontSize(fsCaption).TextColor(pal.textFaint)
			}
		})
	})
}
