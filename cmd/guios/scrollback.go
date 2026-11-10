package main

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The scrollback browser (the prefix and s), as the TUI's: the pane's past
// commands, found by its shell's OSC 133 marks, newest first, with what
// each printed. j and k move, J and K scroll the output, / filters, y
// copies the output, c the command line, Enter types the command line at
// the prompt again, q or Escape leaves. The daemon's capture-pane cuts the
// history into commands (its commands parameter).

type scrollCommand struct {
	cmdline  string
	exit     int
	finished bool
	output   []string
}

type scrollBrowser struct {
	open     bool
	pane     string
	cmds     []scrollCommand
	sel      int
	skip     int // the output's first row shown
	query    string
	filter   bool // the filter is being typed
	loading  bool
	message  string
	listRefs ui.ScrollState
}

// openScrollback reads the pane's last commands from the daemon.
func (a *App) openScrollback() {
	s := a.session(a.selected)
	if s == nil {
		return
	}
	pane := a.focusOf(s)
	a.sbr = scrollBrowser{open: true, pane: pane, loading: true}
	a.verbOn(s.ID, "capture-pane", map[string]any{"window": pane, "commands": 200, "lines": 20000}, func(raw json.RawMessage, err error) {
		if a.sbr.pane != pane {
			return
		}
		a.sbr.loading = false
		var res struct {
			Content  string `json:"content"`
			Marks    bool   `json:"command_marks"`
			Commands []struct {
				Cmdline  string `json:"cmdline"`
				Exit     *int   `json:"exit_code"`
				Finished bool   `json:"finished"`
				Lines    int    `json:"lines"`
			} `json:"commands"`
		}
		if err != nil || json.Unmarshal(raw, &res) != nil {
			a.sbr.message = locales.ScrollbackNoMarks()
			return
		}
		if !res.Marks || len(res.Commands) == 0 {
			a.sbr.message = locales.ScrollbackNoMarks()
			return
		}
		rows := strings.Split(strings.TrimSuffix(res.Content, "\n"), "\n")
		at := 0
		var cmds []scrollCommand
		for _, c := range res.Commands {
			end := min(at+c.Lines, len(rows))
			out := rows[at:end]
			at = end
			// The first row is the prompt with the command line on it.
			if len(out) > 0 {
				out = out[1:]
			}
			sc := scrollCommand{cmdline: c.Cmdline, finished: c.Finished, exit: -1, output: out}
			if c.Exit != nil {
				sc.exit = *c.Exit
			}
			if sc.cmdline == "" && !sc.finished {
				continue // the prompt waiting now
			}
			cmds = append(cmds, sc)
		}
		// Newest first.
		for i, j := 0, len(cmds)-1; i < j; i, j = i+1, j-1 {
			cmds[i], cmds[j] = cmds[j], cmds[i]
		}
		a.sbr.cmds = cmds
		if len(cmds) == 0 {
			a.sbr.message = locales.ScrollbackNoMarks()
		}
	})
}

// shown are the commands the filter keeps.
func (b *scrollBrowser) shown() []int {
	var out []int
	for i, c := range b.cmds {
		if b.query == "" || fuzzy.Match(b.query, c.cmdline) {
			out = append(out, i)
		}
	}
	return out
}

// scrollbackKey takes a key in the browser.
func (a *App) scrollbackKey(k string) {
	b := &a.sbr
	b.message = ""
	if b.filter {
		switch k {
		case "esc":
			b.filter, b.query = false, ""
		case "enter":
			b.filter = false
		case "backspace":
			if r := []rune(b.query); len(r) > 0 {
				b.query = string(r[:len(r)-1])
			}
		case "space":
			b.query += " "
		default:
			if len([]rune(k)) == 1 {
				b.query += k
			}
		}
		b.sel, b.skip = 0, 0
		return
	}
	list := b.shown()
	cur := func() *scrollCommand {
		if b.sel < len(list) {
			return &b.cmds[list[b.sel]]
		}
		return nil
	}
	switch k {
	case "q", "esc":
		a.sbr.open = false
		a.defer_(a.focusTerminal)
	case "j", "down":
		b.sel, b.skip = min(b.sel+1, max(len(list)-1, 0)), 0
	case "k", "up":
		b.sel, b.skip = max(b.sel-1, 0), 0
	case "g", "home":
		b.sel, b.skip = 0, 0
	case "G", "end":
		b.sel, b.skip = max(len(list)-1, 0), 0
	case "J", "ctrl+e", "ctrl+d":
		if c := cur(); c != nil {
			b.skip = min(b.skip+3, max(len(c.output)-1, 0))
		}
	case "K", "ctrl+y", "ctrl+u":
		b.skip = max(b.skip-3, 0)
	case "/":
		b.filter, b.query = true, ""
	case "y":
		if c := cur(); c != nil {
			text := strings.Join(c.output, "\n")
			a.copyText = text
			b.message = locales.ScrollbackCopied(strconv.Itoa(len([]rune(text))))
		}
	case "c":
		if c := cur(); c != nil && c.cmdline != "" {
			a.copyText = c.cmdline
			b.message = locales.ScrollbackCopied(strconv.Itoa(len([]rune(c.cmdline))))
		}
	case "enter":
		if c := cur(); c != nil && c.cmdline != "" {
			if t := a.termFor(b.pane); t != nil {
				t.Paste(c.cmdline)
			}
			a.sbr.open = false
			a.defer_(a.focusTerminal)
		}
	}
}

func (a *App) scrollbackView(c *ui.Context, pal *palette) {
	b := &a.sbr
	if !b.open {
		return
	}
	width := float32(820)
	if w, _ := c.Size(); w > 0 {
		width = min(width, w-32)
	}
	mono := a.termFamily()
	list := b.shown()
	b.sel = min(b.sel, max(len(list)-1, 0))
	ui.DialogBase(c.Key("scrollback"), &b.open, func(backdrop, panel ui.Element) {
		dialogMotion(backdrop, panel, pal.overlay)
		backdrop.Justify(ui.Start).Padding(64, 0, 0, 0)
		h := float32(560)
		if b.loading || len(list) == 0 {
			h = 120
		}
		floating(panel.Width(width).Height(h).Padding(14).Gap(8).Radius(rSheet).Background(pal.popover), pal).Label(locales.ScrollbackTitle())
		keys := ui.Box(c.Key("keys")).Width(0).Height(0).Focusable().FocusRing(false).AutoFocus()
		keys.HandleInput(func(ev ui.InputEvent) bool {
			switch ev.Kind {
			case ui.InputKeyDown:
				if typesText(ev.Mods, ev.Key) {
					return true
				}
				if k, ok := keyString(ev.Mods, ev.Key); ok {
					a.scrollbackKey(k)
				}
				return true
			case ui.InputText:
				for _, r := range ev.Text {
					k := string(r)
					if r == ' ' {
						k = "space"
					}
					a.scrollbackKey(k)
				}
				return true
			}
			return false
		})
		keys.TextCaret(ui.Rect{})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Text(c, locales.ScrollbackTitle()).FontSize(15).FontWeight(650).TextColor(pal.text)
			ui.Textf(c, "%d", len(list)).FontSize(12.5).TextColor(pal.textFaint)
			if b.filter || b.query != "" {
				ui.Text(c, "/"+b.query).Font(mono).FontSize(12.5).TextColor(pal.text).SingleLine()
			}
			ui.Spacer(c)
			if b.message != "" && len(list) > 0 {
				ui.Text(c, b.message).FontSize(12).TextColor(pal.textMuted).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			}
		})
		if b.loading || len(list) == 0 {
			msg := b.message
			if b.loading {
				msg = locales.PaneAttaching()
			} else if msg == "" {
				msg = locales.PaletteNoMatches()
			}
			ui.Text(c, msg).FontSize(13).TextColor(pal.textMuted).LineHeight(1.4)
			return
		}
		ui.Row(c).Grow(1).MinHeight(0).Gap(12).AlignItems(ui.Stretch).Children(func() {
			ui.Scroll(c.Key("cmds")).Width(width * 0.38).Shrink(0).TrackScroll(&b.listRefs).Children(func() {
				for n, i := range list {
					cm := b.cmds[i]
					row := ui.Row(c.Key(i)).Height(28).Padding(0, 8).Gap(8).AlignItems(ui.Center).Radius(6)
					if n == b.sel {
						row.Background(pal.accent.Alpha(0.15)).ScrollIntoView()
					}
					if row.Clicked() {
						b.sel, b.skip = n, 0
					}
					row.Children(func() {
						mark, col := "✓", pal.linkUp()
						switch {
						case !cm.finished:
							mark, col = "…", pal.textFaint
						case cm.exit > 0:
							mark, col = strconv.Itoa(cm.exit), pal.danger
						}
						ui.Text(c, mark).FontSize(11.5).TextColor(col).Width(22).SingleLine().Shrink(0)
						ui.Text(c, cm.cmdline).Font(mono).FontSize(12.5).TextColor(pal.text).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
					})
				}
			})
			ui.Column(c.Key("out")).Grow(1).MinWidth(0).Clip().Padding(8).Radius(8).Background(pal.card).Children(func() {
				cm := b.cmds[list[b.sel]]
				out := cm.output
				if b.skip < len(out) {
					out = out[b.skip:]
				}
				for i, l := range out {
					if i >= 40 {
						break
					}
					ui.Text(c.Key(i), l+" ").Font(mono).FontSize(12).TextColor(pal.text).NoWrap().Shrink(0)
				}
			})
		})
		ui.Text(c, locales.ScrollbackFooter()).FontSize(11.5).TextColor(pal.textFaint).Shrink(0)
	})
	if !b.open {
		a.defer_(a.focusTerminal)
	}
}
