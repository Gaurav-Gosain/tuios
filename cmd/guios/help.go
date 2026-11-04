package main

import (
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/guios/actions"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// Help: the keys, as the TUI's help (prefix then ?) lists them. The tuios
// prefix menus as the config binds them, with the leader before each key,
// and guios's own shortcuts. A filter finds a key by what it does or by
// the key. Escape or the prefix and ? again closes it.

// helpState is the help sheet's.
type helpState struct {
	open   bool
	query  string
	scroll ui.ScrollState
}

// helpRow is one key of the sheet.
type helpRow struct {
	group, keys, what string
}

// helpRows are the sheet's keys: each tuios prefix menu guios runs, then
// guios's shortcuts by menu.
func (a *App) helpRows() []helpRow {
	var rows []helpRow
	tk := &a.tk
	a.loadKeys()
	if a.keysOn() && tk.reg != nil {
		leader := chordLabel(tk.leader)
		for _, m := range []string{"", "workspace", "window", "layout", "minimize"} {
			lead := leader
			if m != "" {
				ks := tk.reg.GetKeys("prefix_" + m)
				if len(ks) == 0 {
					continue
				}
				lead += " " + chordLabel(ks[0])
			}
			for _, g := range config.PrefixMenuGroups(tk.reg, m, config.MenuState{Daemon: true, Minimized: -1}) {
				title := keysTitle(m)
				if g.Title != "" {
					title += " · " + g.Title
				}
				for _, b := range g.Bindings {
					if !nativeActions[b.Action] && !daemonActions[b.Action] {
						continue
					}
					what := b.Description
					if b.Submenu {
						what += " …"
					}
					rows = append(rows, helpRow{title, lead + " " + b.Key, what})
				}
			}
		}
	}
	for _, act := range actions.All {
		k := a.keys(act.ID)
		if k == "" {
			continue
		}
		rows = append(rows, helpRow{"guios · " + act.Menu, k, strings.TrimSuffix(actionTitle(act.ID), "…")})
	}
	return rows
}

func (a *App) openHelp() {
	a.help = helpState{open: true}
}

// helpView is the help sheet, over the window.
func (a *App) helpView(c *ui.Context, pal *palette) {
	if !a.help.open {
		return
	}
	rows := a.helpRows()
	if q := strings.TrimSpace(a.help.query); q != "" {
		kept := rows[:0]
		for _, r := range rows {
			if fuzzy.Match(q, r.what) || strings.Contains(strings.ToLower(r.keys), strings.ToLower(q)) || fuzzy.Match(q, r.group) {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	width := float32(760)
	if w, _ := c.Size(); w > 0 {
		width = min(width, w-32)
	}
	ui.DialogBase(c.Key("help"), &a.help.open, func(backdrop, panel ui.Element) {
		dialogMotion(backdrop, panel, pal.overlay)
		backdrop.Justify(ui.Start).Padding(64, 0, 0, 0)
		floating(panel.Width(width).MaxHeight(620).Radius(14).Background(pal.popover), pal).Clip().Label(locales.HelpTitle())
		ui.Row(c).Height(50).Shrink(0).Padding(0, 16).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, searchIcon).Size(16, 16).TextColor(pal.textMuted)
			ui.TextInputBase(c.Key("q"), &a.help.query).Grow(1).FontSize(15).Placeholder(locales.HelpPlaceholder()).AutoFocus()
			ui.Text(c, strconv.Itoa(len(rows))).FontSize(12).TextColor(pal.textFaint).Shrink(0)
		})
		ui.Scroll(c.Key("rows")).Grow(1).MinHeight(0).Padding(6, 10, 12, 10).TrackScroll(&a.help.scroll).Children(func() {
			if len(rows) == 0 {
				ui.Text(c, locales.PaletteNoMatches()).FontSize(13).TextColor(pal.textFaint).Padding(12)
			}
			last := ""
			for i, r := range rows {
				if r.group != last {
					ui.Text(c, r.group).FontSize(11.5).FontWeight(600).TextColor(pal.textFaint).SingleLine().Padding(10, 6, 4, 6)
					last = r.group
				}
				ui.Row(c.Key(i)).Height(28).Padding(0, 6).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Row(c).Width(170).Shrink(0).Children(func() {
						ui.Text(c, r.keys).FontSize(11).TextColor(pal.textMuted).Padding(1, 5).Radius(4).
							Background(pal.kbd).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
					})
					ui.Text(c, r.what).FontSize(13).TextColor(pal.text).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
				})
			}
		})
		ui.Text(c, locales.HelpFooter()).FontSize(11.5).TextColor(pal.textFaint).Padding(8, 16, 12, 16).Shrink(0)
	})
	if !a.help.open {
		a.focusTerminal()
	}
}
