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
				switch {
				case g.Title != "":
					title += " · " + groupWords(g.Title)
				case m != "":
					// A menu with no groups is a prefix group too, and says
					// so as the others do: "Prefix · Minimize".
					title = locales.KeysMenuPrefix() + " · " + title
				}
				for _, b := range g.Bindings {
					if !nativeActions[b.Action] && !daemonActions[b.Action] {
						continue
					}
					what := paneWords(b)
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
		what := strings.TrimSuffix(actionTitle(act.ID), "…")
		if act.ID == "pane.close" {
			// The menu's "Close" is the pane's on this sheet, as the
			// prefix's Close pane is.
			what = locales.PaneClose()
		}
		rows = append(rows, helpRow{"guios · " + menuTitle(act.Menu), k, what})
	}
	// The splits go right, then down, in every group, as guios's own
	// menu lists them.
	for i := 0; i+1 < len(rows); i++ {
		if rows[i].what == locales.ActionSplitDown() && rows[i+1].what == locales.ActionSplitRight() && rows[i].group == rows[i+1].group {
			rows[i], rows[i+1] = rows[i+1], rows[i]
		}
	}
	return rows
}

// paneWords is a tuios key's description in guios's words. The TUI names
// a pane a window and says "Split horizontal" for the split below. guios
// says pane and "Split down" on every other screen, so its keys say so too.
// A key binding names the first action of its row.
func paneWords(b config.Keybinding) string {
	switch b.Action {
	case "prefix_new_window", "window_prefix_new":
		return locales.ActionNewPane()
	case "prefix_close_window", "window_prefix_close":
		return locales.PaneClose()
	case "prefix_rename_window", "window_prefix_rename":
		return locales.KeysRenamePane()
	case "prefix_next_window":
		return locales.KeysNextPrevPane()
	case "window_prefix_next":
		return locales.ActionNextPane()
	case "window_prefix_prev":
		return locales.ActionPreviousPane()
	case "prefix_select_0":
		return locales.KeysJumpPane()
	case "prefix_split_horizontal", "split_horizontal":
		return locales.ActionSplitDown()
	case "prefix_split_vertical", "split_vertical":
		return locales.ActionSplitRight()
	case "workspace_prefix_move_1":
		return locales.MenuMovePaneToWorkspace()
	case "minimize_window", "minimize_prefix_focused":
		return locales.KeysMinimizePane()
	case "minimize_prefix_restore_1":
		// The TUI adds the count of minimized panes: "Restore window (2 minimized)".
		if i := strings.IndexByte(b.Description, '('); i > 0 {
			return locales.KeysRestorePane() + " " + b.Description[i:]
		}
		return locales.KeysRestorePane()
	case "snap_corner_1":
		return locales.KeysSnapPane()
	case "prefix_window":
		return locales.KeysMenuWindow()
	case "resize_width_50":
		return locales.KeysPaneWidth()
	case "resize_height_50":
		return locales.KeysPaneHeight()
	}
	return b.Description
}

// groupWords is a tuios menu group's title in guios's words: its Windows
// are guios's panes.
func groupWords(title string) string {
	if title == "Windows" {
		return locales.KeysGroupPanes()
	}
	return title
}

func (a *App) openHelp() {
	a.help = helpState{open: true}
}

// helpView is the help sheet, over the window. It is the palette's
// shell (council round 1, visual 18): the same top, query row, hairline
// and group labels. Two columns of groups from 1440 wide.
func (a *App) helpView(c *ui.Context, pal *palette) {
	if !a.help.open {
		return
	}
	rows := a.helpRows()
	q := strings.TrimSpace(a.help.query)
	if q != "" {
		kept := rows[:0]
		for _, r := range rows {
			if fuzzy.Match(q, r.what) || strings.Contains(strings.ToLower(r.keys), strings.ToLower(q)) || fuzzy.Match(q, r.group) {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	ww, wh := c.Size()
	two := ww >= 1440 && len(rows) > 12
	// One width, whatever the query finds: the sheet does not jump as
	// the user types (council round 3, visual S7).
	width := float32(980)
	if ww > 0 {
		width = min(width, ww-160)
	}
	// The cap column is as wide as its widest cap, and the labels follow.
	capW := float32(0)
	for _, r := range rows {
		capW = max(capW, natural(c, keyCap(r.keys), fsCaption, wMedium)+2*5+2)
	}
	capW += sp4
	// The groups, each with its rows, split into two columns of about one
	// height when there is room.
	type group struct {
		title string
		rows  []helpRow
	}
	var groups []group
	for _, r := range rows {
		if len(groups) == 0 || groups[len(groups)-1].title != r.group {
			groups = append(groups, group{title: r.group})
		}
		g := &groups[len(groups)-1]
		g.rows = append(g.rows, r)
	}
	split := len(groups)
	if two {
		total, run := 0, 0
		for _, g := range groups {
			total += len(g.rows) + 1
		}
		for i, g := range groups {
			if run >= total/2 {
				split = i
				break
			}
			run += len(g.rows) + 1
		}
	}
	column := func(gs []group) {
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
			for gi, g := range gs {
				ui.Text(c, g.title).FontSize(fsSmall).FontWeight(wMedium).TextColor(pal.textFaint).SingleLine().Padding(10, 10, 4, 10)
				for i, r := range g.rows {
					ui.Row(c.Key(g.title+"-"+strconv.Itoa(gi)+"-"+strconv.Itoa(i))).Height(hRow-4).Padding(0, 10).AlignItems(ui.Center).Children(func() {
						ui.Row(c).Width(capW).Shrink(0).Children(func() { kbd(c, pal, keyCap(r.keys)) })
						ui.RichText(c, helpSpans(r.what, q, pal)...).FontSize(fsBody).TextColor(pal.text).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
					})
				}
			}
		})
	}
	ui.DialogBase(c.Key("help"), &a.help.open, func(backdrop, panel ui.Element) {
		dialogMotion(backdrop, panel, pal.overlay)
		backdrop.Justify(ui.Start).Padding(max(wh*0.12, 40), 0, 0, 0)
		floating(panel.Width(width).MaxHeight(min(620, wh*0.76)).Radius(rSheet).Background(pal.popover), pal).Clip().Label(locales.HelpTitle())
		ui.Row(c).Height(44).Shrink(0).Padding(0, sp4).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, searchIcon).Size(16, 16).TextColor(pal.textMuted).Shrink(0)
			ui.TextInputBase(c.Key("q"), &a.help.query).Grow(1).MinWidth(0).FontSize(fsProse).Placeholder(locales.HelpPlaceholder()).AutoFocus()
			ui.Text(c, strconv.Itoa(len(rows))).FontSize(fsSmall).TextColor(pal.textFaint).FontFeatures("tnum").Shrink(0)
		})
		ui.Box(c).Height(1).Shrink(0).Background(pal.divider)
		ui.Scroll(c.Key("rows")).Grow(1).MinHeight(0).Padding(6, 6, 12, 6).TrackScroll(&a.help.scroll).Children(func() {
			if len(rows) == 0 {
				ui.Text(c, locales.PaletteNoMatches()).FontSize(fsBody).TextColor(pal.textFaint).Padding(12)
				return
			}
			ui.Row(c).Gap(sp4).AlignItems(ui.Start).Children(func() {
				column(groups[:split])
				if split < len(groups) {
					column(groups[split:])
				}
			})
		})
		// A hairline over the footer, so the last row ends on a line.
		ui.Box(c).Height(1).Shrink(0).Background(pal.divider)
		ui.Text(c, locales.HelpFooter()).FontSize(fsSmall).TextColor(pal.textMuted).Padding(10, 16, 12, 16).Shrink(0)
	})
	if !a.help.open {
		a.focusTerminal()
	}
}

// keyCap is a key as its cap reads: two keys a slash apart ("|/\\")
// written with "or", so that they do not run together.
func keyCap(keys string) string {
	i := strings.LastIndexByte(keys, ' ')
	last := keys[i+1:]
	if r := []rune(last); len(r) == 3 && r[1] == '/' {
		return keys[:i+1] + string(r[0]) + " or " + string(r[2])
	}
	return keys
}

// helpSpans is a label with the query's match in the accent, as the
// palette marks its matches.
func helpSpans(what, q string, pal *palette) []ui.Span {
	if q == "" {
		return []ui.Span{{Text: what}}
	}
	i := strings.Index(strings.ToLower(what), strings.ToLower(q))
	if i < 0 {
		return []ui.Span{{Text: what}}
	}
	return []ui.Span{paletteSpan(what[:i], false, pal), paletteSpan(what[i:i+len(q)], true, pal), paletteSpan(what[i+len(q):], false, pal)}
}
